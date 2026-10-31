package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
)

// The upstream catalogue seam is package-level in provider (the persisted copy
// and the in-memory sync state live there), so these tests isolate the real
// machine before anything touches it: HOME into a temp dir so the persisted
// ~/.ogcode/catalog-upstream.json lands somewhere disposable — the same trap
// serve_lifecycle_test.go learned when HOME's defaults leak — and the sync
// opt-out cleared, so the developer's shell cannot silently disable the calls
// these tests are here to exercise.
func isolateUpstreamCatalog(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OGCODE_CATALOG_SYNC", "")
	t.Setenv("OGCODE_CATALOG_URL", "")
}

// upstreamStub stands in for the catalogue endpoint the seam fetches: it counts
// hits and can fail on command, so the TTL and error shapes are observable.
type upstreamStub struct {
	handler func(w http.ResponseWriter, r *http.Request)
	hits    atomic.Int64
}

func newUpstreamStub(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *upstreamStub {
	t.Helper()
	stub := &upstreamStub{handler: handler}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		stub.hits.Add(1)
		handler(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("OGCODE_CATALOG_URL", srv.URL)
	return stub
}

// TestCatalogSyncRouteReturnsResultShape pins the manual sync endpoint end to
// end: the POST reaches the provider seam (the stub is fetched), and the
// response carries exactly the fields the models page reads.
func TestCatalogSyncRouteReturnsResultShape(t *testing.T) {
	isolateUpstreamCatalog(t)
	stub := newUpstreamStub(t, func(w http.ResponseWriter, r *http.Request) {
		// The wire format the seam parses (upstreamCatalogFile): version 1
		// first — an unsupported version refuses the whole file.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":1,"models":[]}`))
	})
	srv := newTestServer(t)
	h := srv.routes()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/models/catalog/sync", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/models/catalog/sync = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := stub.hits.Load(); got != 1 {
		t.Fatalf("sync endpoint fetched the upstream %d times, want 1", got)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode sync response: %v (body: %s)", err, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("content type = %q, want application/json", rec.Header().Get("Content-Type"))
	}
	want := []string{"syncedAt", "source", "installed", "updated", "skipped", "modelCount"}
	for _, k := range want {
		if _, ok := body[k]; !ok {
			t.Fatalf("sync response missing %q: %v", k, body)
		}
	}
	// The timestamp is wire time (RFC3339), parseable by any client.
	if _, err := time.Parse(time.RFC3339, body["syncedAt"].(string)); err != nil {
		t.Fatalf("syncedAt = %v, want an RFC3339 timestamp: %v", body["syncedAt"], err)
	}
}

// TestCatalogSyncBootSkipsFreshState pins the TTL: a sync that just happened
// satisfies the boot check, so starting the server does NOT fetch the upstream
// again — the persisted state is young, and re-fetching eagerly would burn the
// endpoint every boot for a list that cannot have changed in minutes.
func TestCatalogSyncBootSkipsFreshState(t *testing.T) {
	isolateUpstreamCatalog(t)
	stub := newUpstreamStub(t, func(w http.ResponseWriter, r *http.Request) {
		// The wire format the seam parses (upstreamCatalogFile): version 1
		// first — an unsupported version refuses the whole file.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":1,"models":[]}`))
	})

	if _, err := provider.SyncUpstreamCatalog(context.Background()); err != nil {
		t.Fatalf("seed sync: %v", err)
	}
	if got := stub.hits.Load(); got != 1 {
		t.Fatalf("seed sync fetched %d times, want 1", got)
	}

	syncUpstreamCatalogIfStale(context.Background())

	if got := stub.hits.Load(); got != 1 {
		t.Fatalf("a fresh last-synced state was re-fetched %d times, want 0 extra calls", got)
	}
}

// TestCatalogSyncBootFetchesWhenMissing pins the other branch of the TTL: with
// nothing synced yet, the boot check must run a sync.
func TestCatalogSyncBootFetchesWhenMissing(t *testing.T) {
	isolateUpstreamCatalog(t)
	stub := newUpstreamStub(t, func(w http.ResponseWriter, r *http.Request) {
		// The wire format the seam parses (upstreamCatalogFile): version 1
		// first — an unsupported version refuses the whole file.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":1,"models":[]}`))
	})

	syncUpstreamCatalogIfStale(context.Background())

	if got := stub.hits.Load(); got != 1 {
		t.Fatalf("a never-synced state triggered %d fetches, want 1", got)
	}
	syncedAt, _, _, ok := provider.UpstreamCatalogInfo()
	if !ok || time.Since(syncedAt) > time.Minute {
		t.Fatalf("after boot sync the state is not fresh: ok=%v syncedAt=%v", ok, syncedAt)
	}
}

// TestCatalogSyncRouteFailureShape pins the error path: an upstream that
// refuses returns a 5xx carrying the JSON error object the package's routes use,
// never a 200 dressed as success.
func TestCatalogSyncRouteFailureShape(t *testing.T) {
	isolateUpstreamCatalog(t)
	_ = newUpstreamStub(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream exploded", http.StatusInternalServerError)
	})
	srv := newTestServer(t)
	h := srv.routes()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/models/catalog/sync", nil))
	if rec.Code < 500 || rec.Code > 599 {
		t.Fatalf("POST /api/models/catalog/sync = %d on a failing upstream, want a 5xx (body: %s)", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v (body: %s)", err, rec.Body.String())
	}
	if body["error"] == "" {
		t.Fatalf("error body carries no error message: %v", body)
	}
}

// TestCatalogStatusRouteReportsUnsynced pins the status endpoint on a fresh
// server: 200, synced=false, zero counts — the shape the models page renders
// "Never" from.
func TestCatalogStatusRouteReportsUnsynced(t *testing.T) {
	isolateUpstreamCatalog(t)
	srv := newTestServer(t)
	h := srv.routes()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/models/catalog/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/models/catalog/status = %d, want 200", rec.Code)
	}
	var body struct {
		Synced     bool   `json:"synced"`
		SyncedAt   string `json:"syncedAt"`
		Source     string `json:"source"`
		ModelCount int    `json:"modelCount"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status: %v (body: %s)", err, rec.Body.String())
	}
	if body.Synced || body.SyncedAt != "" || body.ModelCount != 0 {
		t.Fatalf("unsynced status = %+v, want synced=false, empty syncedAt, 0 models", body)
	}
}
