package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// countingGateway is an httptest endpoint that records how many times its
// /models route is hit, so a test can prove a code path did (or did not) reach
// the network.
func countingGateway(t *testing.T, hits *atomic.Int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			hits.Add(1)
			_, _ = w.Write([]byte(`{"data":[{"id":"m1"},{"id":"m2"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestModelsNeverFetches is the invariant the whole change exists for: Models()
// is a pure read. Before this, the first Models() call on a cloud provider did a
// synchronous network fetch behind a sync.Once, and every caller — the picker,
// the per-turn ContextWindow/MaxOutputTokens lookups — could block on it. A read
// path that hits the endpoint at all is the regression this pins.
func TestModelsNeverFetches(t *testing.T) {
	var hits atomic.Int64
	srv := countingGateway(t, &hits)

	p := &OpenAIProvider{id: "openrouter", baseURL: srv.URL, model: "m"}

	// Many reads, including the first: none may touch the endpoint.
	for i := 0; i < 5; i++ {
		if list := p.Models(); len(list) == 0 {
			t.Fatalf("read %d: Models() returned nothing; the compiled-in fallback must answer before any fetch", i)
		}
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("Models() hit the network %d times, want 0 — it must be a pure cache read", got)
	}
}

// TestModelsReturnsFallbackUntilCatalogLoaded pins that a provider with no
// fetched or seeded catalogue answers from its compiled-in list rather than an
// empty one, so a cold start still shows the user something.
func TestModelsReturnsFallbackUntilCatalogLoaded(t *testing.T) {
	p := &OpenAIProvider{id: "openrouter", model: "m"}
	list := p.Models()
	if len(list) == 0 {
		t.Fatal("a provider with no catalogue must answer from its compiled-in fallback")
	}
	for _, m := range list {
		if m.ProviderID != "openrouter" {
			t.Errorf("fallback model %q providerId = %q, want openrouter", m.ID, m.ProviderID)
		}
	}
}

// TestSetCatalogSeedsInMemoryList pins the seeding path the server uses on
// startup: a persisted catalogue is installed without any network access, and a
// later Models() read reflects it.
func TestSetCatalogSeedsInMemoryList(t *testing.T) {
	var hits atomic.Int64
	srv := countingGateway(t, &hits)
	p := &OpenAIProvider{id: "openrouter", baseURL: srv.URL, model: "m"}

	p.SetCatalog([]ModelInfo{
		{ID: "seeded-a", ProviderID: "openrouter", ActiveByDefault: true},
		{ID: "seeded-b", ProviderID: "openrouter"},
	})
	list := p.Models()
	if len(list) != 2 {
		t.Fatalf("got %d models, want the 2 seeded: %+v", len(list), list)
	}
	if list[0].ID != "seeded-a" {
		t.Errorf("first seeded model = %q, want seeded-a", list[0].ID)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("SetCatalog/Models hit the network %d times, want 0", got)
	}
}

// TestSetCatalogIgnoresEmpty pins that seeding an empty list does not wipe a
// catalogue already loaded: a provider whose persisted copy was somehow empty
// must keep answering from what it has.
func TestSetCatalogIgnoresEmpty(t *testing.T) {
	p := &OpenAIProvider{id: "openrouter", model: "m"}
	p.SetCatalog([]ModelInfo{{ID: "kept", ProviderID: "openrouter", ActiveByDefault: true}})
	p.SetCatalog(nil)
	p.SetCatalog([]ModelInfo{})
	if list := p.Models(); len(list) != 1 || list[0].ID != "kept" {
		t.Fatalf("empty seed wiped the catalogue: %+v", list)
	}
}

// TestRefreshCatalogInstallsAndReturnsLiveList pins the one network path: an
// explicit refresh fetches, installs, and returns the live list for persistence.
func TestRefreshCatalogInstallsAndReturnsLiveList(t *testing.T) {
	var hits atomic.Int64
	srv := countingGateway(t, &hits)

	p := &OpenAIProvider{id: "openrouter", baseURL: srv.URL, model: "m"}
	got := p.RefreshCatalog(context.Background())
	if len(got) != 2 {
		t.Fatalf("RefreshCatalog returned %d models, want 2: %+v", len(got), got)
	}
	if hits.Load() != 1 {
		t.Fatalf("RefreshCatalog hit the network %d times, want 1", hits.Load())
	}
	// And the live list is now what Models() reads.
	if list := p.Models(); len(list) != 2 {
		t.Fatalf("after refresh Models() = %d models, want 2", len(list))
	}
}

// TestRefreshCatalogFailureKeepsLastKnown pins that a failed refresh returns nil
// (so the caller persists nothing) and leaves the previous catalogue in place —
// a transient network blip must never blank the picker.
func TestRefreshCatalogFailureKeepsLastKnown(t *testing.T) {
	p := &OpenAIProvider{id: "openrouter", baseURL: "http://127.0.0.1:1/v1", model: "m"}
	p.SetCatalog([]ModelInfo{{ID: "keep-me", ProviderID: "openrouter", ActiveByDefault: true}})

	if got := p.RefreshCatalog(context.Background()); got != nil {
		t.Fatalf("a failed refresh returned %+v, want nil (nothing to persist)", got)
	}
	if list := p.Models(); len(list) != 1 || list[0].ID != "keep-me" {
		t.Fatalf("a failed refresh wiped the catalogue: %+v", list)
	}
}

// TestModelsDefaultFlagIsNotSharedAcrossCalls pins that stamping the default
// does not mutate the cached catalogue or a package-level fallback: two reads
// must return independent slices, or a concurrent reader could observe one
// provider's default leaking onto another's list.
func TestModelsDefaultFlagIsNotSharedAcrossCalls(t *testing.T) {
	p := &OpenAIProvider{id: "openrouter", model: "m"}
	first := p.Models()
	second := p.Models()
	if len(first) == 0 || len(second) == 0 {
		t.Fatal("expected fallback models")
	}
	// Mutating what one read handed back must not change what the next returns.
	first[0].Default = true
	first[0].ID = "mutated"
	for _, m := range p.Models() {
		if m.ID == "mutated" {
			t.Fatal("Models() handed back a shared slice — a caller's mutation leaked into the catalogue")
		}
	}
}
