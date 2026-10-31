package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// resetUpstreamCatalog drops the merged layer and rebuilds every lookup memo
// from the compiled-in tables, then restores the same clean state at test end.
// Every test that touches the global upstream state starts with it.
func resetUpstreamCatalog(t *testing.T) {
	t.Helper()
	reset := func() {
		upstreamMu.Lock()
		upstreamLists = nil
		upstreamMu.Unlock()
		invalidateCatalogIndexes()
	}
	t.Cleanup(reset)
	reset()
}

// upstreamPage is a minimal hand-written file body for the tests below.
func upstreamPage(models ...CatalogModel) []byte {
	page := struct {
		Version int            `json:"version"`
		Models  []CatalogModel `json:"models"`
	}{Version: 1, Models: models}
	data, err := json.Marshal(page)
	if err != nil {
		panic(err)
	}
	return data
}

// TestApplyUpstreamReplacesCompiledInFacts pins the merge-precedence rule: an
// upstream id the compiled tables know replaces those facts for that id, while
// the row keeps its identity — its ID string and ActiveByDefault. The
// compiled-in slice itself is never touched.
func TestApplyUpstreamReplacesCompiledInFacts(t *testing.T) {
	resetUpstreamCatalog(t)

	before := AnthropicModels[0].InputPricePerM
	m, ok := LookupCatalogModel("claude-fable-5-1")
	if !ok || m.ContextWindow != 1_000_000 || m.OutputPricePerM != 50 {
		t.Fatalf("precondition: compiled-in claude-fable-5-1 facts missing: %+v", m)
	}

	res, err := ApplyUpstreamCatalog(upstreamPage(CatalogModel{
		ID: "claude-fable-5-1", Name: "Claude Fable 5.1",
		InputPricePerM: 7, OutputPricePerM: 33, ContextWindow: 900_000,
		MaxOutputTokens: 100_000, SupportsImages: false, CacheReadPricePerM: 0.2,
	}))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.Updated != 1 || res.Installed != 0 || res.Skipped != 0 {
		t.Fatalf("result = %+v, want Updated=1 Installed=0 Skipped=0", res)
	}

	m, ok = LookupCatalogModel("claude-fable-5-1")
	if !ok {
		t.Fatal("merged lookup lost claude-fable-5-1")
	}
	if m.InputPricePerM != 7 || m.OutputPricePerM != 33 || m.ContextWindow != 900_000 ||
		m.MaxOutputTokens != 100_000 || m.SupportsImages || m.CacheReadPricePerM != 0.2 {
		t.Fatalf("upstream facts did not replace the compiled-in ones: %+v", m)
	}
	// Identity keeps: the compiled-in row was ActiveByDefault: true.
	if !m.ActiveByDefault {
		t.Fatal("merged row lost the compiled-in ActiveByDefault")
	}
	// The package slice the compiled tables are typed on is untouched.
	if AnthropicModels[0].InputPricePerM != before {
		t.Fatalf("compiled-in table mutated: input price %.2f, want %.2f", AnthropicModels[0].InputPricePerM, before)
	}

	// The Anthropic picker shows the replaced facts, still keyed by the
	// compiled-in id and default state.
	p := &AnthropicProvider{model: "claude-fable-5-1"}
	found := false
	for _, mi := range p.Models() {
		if mi.ID != "claude-fable-5-1" {
			continue
		}
		found = true
		if mi.InputPricePerM != 7 || mi.ContextWindow != 900_000 || !mi.ActiveByDefault {
			t.Fatalf("picker row not replaced: %+v", mi)
		}
	}
	if !found {
		t.Fatal("picker lost its compiled-in id after the upstream merge")
	}
}

// TestApplyUpstreamAliasesMatchAndUnion pins the alias rules: an upstream file
// may address a compiled-in entry by one of its aliases and still replace that
// row, the compiled-in aliases stand, and new upstream aliases join the union —
// while an alias another compiled-in entry claims is never stolen.
func TestApplyUpstreamAliasesMatchAndUnion(t *testing.T) {
	resetUpstreamCatalog(t)

	// claude-haiku-4-5-20251001 answers "claude-haiku-4-5"; address it by the
	// alias, replace its facts, and add a brand-new alias.
	res, err := ApplyUpstreamCatalog(upstreamPage(CatalogModel{
		ID: "claude-haiku-4-5", Aliases: []string{"haiku-fresh"},
		Name: "Claude Haiku 4.5", InputPricePerM: 2, ContextWindow: 111_000,
		SupportsImages: true,
	}))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.Updated != 1 || res.Installed != 0 {
		t.Fatalf("result = %+v, want Updated=1 Installed=0", res)
	}
	// The row kept its compiled-in ID string, and all three spellings resolve
	// to the replaced facts.
	for _, spelling := range []string{
		"claude-haiku-4-5-20251001", "claude-haiku-4-5", "haiku-fresh",
	} {
		m, ok := LookupCatalogModel(spelling)
		if !ok || m.InputPricePerM != 2 || m.ContextWindow != 111_000 {
			t.Fatalf("lookup %q after merge = (%+v, %v), want the upstream facts", spelling, m, ok)
		}
	}
	if !lookupOne("claude-haiku-4-5-20251001").SupportsImages {
		t.Fatal("compiled-in SupportsImages was dropped by the upstream replacement")
	}
	// A sibling's alias is never stolen: claude-sonnet-4-5-20250929 claims
	// "claude-sonnet-4-5", so an upstream file offering that spelling for the
	// haiku row gets it dropped, and first-claim-wins still answers the opus.
	res, err = ApplyUpstreamCatalog(upstreamPage(CatalogModel{
		ID: "claude-haiku-4-5", Aliases: []string{"claude-sonnet-4-5"},
		InputPricePerM: 9,
	}))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.Updated != 1 {
		t.Fatalf("result = %+v, want Updated=1", res)
	}
	if got := lookupOne("claude-sonnet-4-5").InputPricePerM; got != 3 {
		t.Fatalf("alias stolen from its compiled-in owner: input %.2f, want the Sonnet 4.5 price 3.00", got)
	}
}

// TestApplyUpstreamAddsNewModelsToPickers pins the release-free picker rule: a
// model the compiled tables do not know is added to the picker its collection
// names — Anthropic into AnthropicProvider.Models(), OpenAI into openAICatalog()
// with the ResponsesOnly filter still applied — while a collection-less model
// enriches lookups only and lands in neither picker.
func TestApplyUpstreamAddsNewModelsToPickers(t *testing.T) {
	resetUpstreamCatalog(t)

	res, err := ApplyUpstreamCatalog(upstreamPage(
		CatalogModel{ID: "claude-quanta-6", Name: "Claude Quanta 6", Collection: "anthropic",
			ActiveByDefault: true, InputPricePerM: 1, ContextWindow: 400_000},
		CatalogModel{ID: "gpt-quanta", Name: "GPT Quanta", Collection: "openai",
			InputPricePerM: 1, ContextWindow: 400_000},
		CatalogModel{ID: "gpt-quanta-resp", Collection: "openai", ResponsesOnly: true,
			ActiveByDefault: true, ContextWindow: 400_000},
		CatalogModel{ID: "opaque-model-x", ContextWindow: 100},
	))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.Installed != 4 {
		t.Fatalf("result = %+v, want Installed=4", res)
	}

	anthropicIDs := func() map[string]bool {
		ids := make(map[string]bool)
		for _, mi := range (&AnthropicProvider{}).Models() {
			ids[mi.ID] = true
		}
		return ids
	}()
	if !anthropicIDs["claude-quanta-6"] {
		t.Fatal("a synced Anthropic model must surface in the Anthropic picker without a release")
	}
	if anthropicIDs["gpt-quanta"] || anthropicIDs["opaque-model-x"] {
		t.Fatal("non-Anthropic additions leaked into the Anthropic picker")
	}

	openaiIDs := func() map[string]bool {
		ids := make(map[string]bool)
		for _, mi := range openAICatalog() {
			ids[mi.ID] = true
		}
		return ids
	}()
	if !openaiIDs["gpt-quanta"] {
		t.Fatal("a synced OpenAI model must surface in the OpenAI picker without a release")
	}
	if openaiIDs["gpt-quanta-resp"] {
		t.Fatal("a synced ResponsesOnly model must stay filtered out of the Chat Completions picker")
	}

	// The collection-less id answers only lookups.
	if _, ok := openaiIDs["opaque-model-x"]; ok {
		t.Fatal("a collection-less model must not appear in either first-party picker")
	}
	if _, ok := anthropicIDs["opaque-model-x"]; ok {
		t.Fatal("a collection-less model must not appear in either first-party picker")
	}
	m, ok := LookupCatalogModel("opaque-model-x")
	if !ok || m.ContextWindow != 100 {
		t.Fatalf("lookup-only addition missing from the index: (%+v, %v)", m, ok)
	}
}

// TestApplyUpstreamKeepsExistingRowDefaultsActive pins that ActiveByDefault from
// the file is used for NEW rows only: a compiled-in row it re-names never flips
// its default-active state.
func TestApplyUpstreamKeepsExistingRowDefaultsActive(t *testing.T) {
	resetUpstreamCatalog(t)

	// claude-fable-5-1 is ActiveByDefault: true; send false upstream.
	if _, err := ApplyUpstreamCatalog(upstreamPage(CatalogModel{
		ID: "claude-fable-5-1", ActiveByDefault: false, InputPricePerM: 1,
	})); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := lookupOne("claude-fable-5-1").ActiveByDefault; !got {
		t.Fatal("an upstream file must not flip an existing row's default-active state")
	}
}

// TestApplyUpstreamSkipsImplausibleEntries pins the plausibility guard: an
// entry with an empty id or a negative window, output ceiling, or price is
// refused whole (counted Skipped), 0 = unknown is accepted, an exact duplicate
// id and a date-stripped twin both lose to the first entry, and the good
// entries around them still land.
func TestApplyUpstreamSkipsImplausibleEntries(t *testing.T) {
	resetUpstreamCatalog(t)

	page := upstreamPage(
		CatalogModel{ID: "keep-a", ContextWindow: 0, MaxOutputTokens: 0}, // 0 is acceptable
		CatalogModel{ID: "", ContextWindow: 10},                          // 1: empty id
		CatalogModel{ID: "neg-window", ContextWindow: -1},                // 2
		CatalogModel{ID: "neg-output", MaxOutputTokens: -5},              // 3
		CatalogModel{ID: "neg-price", InputPricePerM: -0.01},             // 4
		CatalogModel{ID: "dupe", ContextWindow: 7},                       // first wins
		CatalogModel{ID: "dupe", ContextWindow: 99},                      // 5: exact duplicate
		CatalogModel{ID: "dupe-20260101", ContextWindow: 99},             // 6: date-stripped twin
	)
	res, err := ApplyUpstreamCatalog(page)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.Skipped != 6 {
		t.Fatalf("result = %+v, want Skipped=6", res)
	}
	if res.Installed != 2 {
		t.Fatalf("result = %+v, want Installed=2 (the zero-unknown entry and the first dupe)", res)
	}
	if got := lookupOne("dupe").ContextWindow; got != 7 {
		t.Fatalf("duplicate took the later entry's facts: window %d, want 7 from the first", got)
	}
	m, ok := LookupCatalogModel("keep-a")
	if !ok || m.ContextWindow != 0 || m.MaxOutputTokens != 0 {
		t.Fatalf("zero-unknown entry refused: (%+v, %v)", m, ok)
	}
}

// TestApplyUpstreamCatalogRoundTripsTheDataFile checks the tracked data file
// against the machinery it exists for: applying it must replace every entry in
// the compiled tables and add nothing, and every id in the file must resolve
// through the index afterwards.
func TestApplyUpstreamCatalogRoundTripsTheDataFile(t *testing.T) {
	resetUpstreamCatalog(t)

	data, err := os.ReadFile("data/catalog.json")
	if err != nil {
		t.Fatalf("read data/catalog.json: %v — the generated file is missing", err)
	}
	var file struct {
		Models []CatalogModel `json:"models"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("data/catalog.json does not parse: %v", err)
	}

	res, err := ApplyUpstreamCatalog(data)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Installed 0: the file carries only ids the compiled tables know.
	// Skipped 0: no entry fails the plausibility guard or duplicates another.
	if res.Installed != 0 || res.Skipped != 0 || res.Updated != len(file.Models) {
		t.Fatalf("round trip = %+v, want Installed=0 Skipped=0 Updated=%d", res, len(file.Models))
	}
	for _, m := range file.Models {
		if _, ok := LookupCatalogModel(m.ID); !ok {
			t.Fatalf("file model %q does not resolve through the index", m.ID)
		}
	}
}

// TestSyncUpstreamCatalogDisabled pins the kill switch: OGCODE_CATALOG_SYNC=false
// makes the sync return ErrUpstreamSyncDisabled without touching the endpoint
// or the merged state.
func TestSyncUpstreamCatalogDisabled(t *testing.T) {
	resetUpstreamCatalog(t)
	t.Setenv("OGCODE_CATALOG_SYNC", "false")

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
	}))
	defer srv.Close()
	t.Setenv("OGCODE_CATALOG_URL", srv.URL)

	res, err := SyncUpstreamCatalog(context.Background())
	if !errors.Is(err, ErrUpstreamSyncDisabled) {
		t.Fatalf("err = %v, want ErrUpstreamSyncDisabled", err)
	}
	if res != (UpstreamSyncResult{}) {
		t.Fatalf("disabled sync returned %+v, want the zero result", res)
	}
	if hits != 0 {
		t.Fatalf("disabled sync hit the endpoint %d times, want 0", hits)
	}
	// State untouched: the merged layer is still absent.
	if _, ok := LookupCatalogModel("claude-fable-5-1"); !ok {
		t.Fatal("compiled-in lookup broken after a disabled sync")
	}
}

// lookupOne reads a fact off the catalog for a model the test just installed;
// a missing id there is a bug in the test itself, so it panics.
func lookupOne(id string) CatalogModel {
	m, ok := LookupCatalogModel(id)
	if !ok {
		panic("test lookup missing " + id)
	}
	return m
}

// lookupOneOpenAI is lookupOne against the OpenAI-side memo lookup.
func lookupOneOpenAI(id string) CatalogModel {
	m, ok := openAICatalogModel(id)
	if !ok {
		panic("test lookup missing " + id)
	}
	return m
}

// TestSyncUpstreamCatalogFetchesMergesAndPersists drives the whole sync end to
// end against httptest with OGCODE_CATALOG_URL: the fetched file merges, a
// stamped copy persists under ~/.ogcode, and UpstreamCatalogInfo reports it.
func TestSyncUpstreamCatalogFetchesMergesAndPersists(t *testing.T) {
	resetUpstreamCatalog(t)
	t.Setenv("OGCODE_CATALOG_URL", "") // exercise UpstreamCatalogURL override only

	body := upstreamPage(
		CatalogModel{ID: "gpt-4o", Name: "GPT-4o", InputPricePerM: 9, ContextWindow: 777_000},
		CatalogModel{ID: "new-sync-model", Collection: "openai", ActiveByDefault: true,
			ContextWindow: 500_000},
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	t.Setenv("OGCODE_CATALOG_URL", srv.URL)

	home := t.TempDir()
	t.Setenv("HOME", home)

	res, err := SyncUpstreamCatalog(context.Background())
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Source != srv.URL {
		t.Fatalf("source = %q, want %q", res.Source, srv.URL)
	}
	if res.FetchedAt.IsZero() || res.Installed != 1 || res.Updated != 1 || res.Skipped != 0 {
		t.Fatalf("result = %+v, want Installed=1 Updated=1 Skipped=0 and a fetched time", res)
	}

	// The merge is live.
	if got := lookupOneOpenAI("gpt-4o").InputPricePerM; got != 9 {
		t.Fatalf("fetched facts not merged: input %.2f, want 9.00", got)
	}
	found := false
	for _, mi := range openAICatalog() {
		if mi.ID == "new-sync-model" {
			found = true
		}
	}
	if !found {
		t.Fatal("a synced OpenAI model must surface in the picker right after the sync")
	}

	// A stamped copy persisted for the next boot.
	persisted := filepath.Join(home, ".ogcode", "catalog-upstream.json")
	data, err := os.ReadFile(persisted)
	if err != nil {
		t.Fatalf("persisted file missing: %v", err)
	}
	var file upstreamCatalogFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("persisted file does not parse: %v", err)
	}
	if file.Source != srv.URL || file.SyncedAt.IsZero() || len(file.Models) != 2 {
		t.Fatalf("persisted file = source %q syncedAt %v models %d, want the stamped copy",
			file.Source, file.SyncedAt, len(file.Models))
	}

	// Info reads the persisted meta.
	when, source, count, ok := UpstreamCatalogInfo()
	if !ok || source != srv.URL || count != 2 || when.IsZero() {
		t.Fatalf("info = (%v, %q, %d, %v), want the synced state", when, source, count, ok)
	}
}

// TestSyncFailureKeepsPreviousState mirrors the RefreshCatalog transient rule:
// a failed fetch leaves the previously merged state untouched.
func TestSyncFailureKeepsPreviousState(t *testing.T) {
	resetUpstreamCatalog(t)

	if _, err := ApplyUpstreamCatalog(upstreamPage(CatalogModel{
		ID: "claude-fable-5-1", InputPricePerM: 7,
	})); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("OGCODE_CATALOG_URL", srv.URL)
	t.Setenv("HOME", "")

	if _, err := SyncUpstreamCatalog(context.Background()); err == nil {
		t.Fatal("a 500 sync must fail")
	}
	if got := lookupOne("claude-fable-5-1").InputPricePerM; got != 7 {
		t.Fatalf("previous merged state disturbed by a failed sync: input %.2f, want 7.00", got)
	}
}

// TestLoadPersistedUpstreamCatalog covers the boot path: no file is a clean
// no-op, a corrupt file is an error (never garbage installed), and a valid
// stamped file reinstalls its models and its meta.
func TestLoadPersistedUpstreamCatalog(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		resetUpstreamCatalog(t)
		t.Setenv("HOME", t.TempDir())
		if err := LoadPersistedUpstreamCatalog(); err != nil {
			t.Fatalf("missing file must be a clean no-op, got %v", err)
		}
		if upstreamLists != nil {
			t.Fatal("a missing file must not install an empty layer")
		}
	})

	t.Run("corrupt file", func(t *testing.T) {
		resetUpstreamCatalog(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		path := filepath.Join(home, ".ogcode", "catalog-upstream.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"version":1,"models":[{"id":"oops"`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := LoadPersistedUpstreamCatalog(); err == nil {
			t.Fatal("a corrupt file must error, never install garbage")
		}
		if upstreamLists != nil {
			t.Fatal("a corrupt file must not install anything")
		}
	})

	t.Run("valid file", func(t *testing.T) {
		resetUpstreamCatalog(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		path := filepath.Join(home, ".ogcode", "catalog-upstream.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		stamped := func() []byte {
			page := upstreamCatalogFile{
				Version: 1, Source: "persisted-test", SyncedAt: time.Now().Add(-time.Hour),
				Models: []CatalogModel{{ID: "claude-fable-5-1", InputPricePerM: 7, ContextWindow: 500_000}},
			}
			// MarshalIndent through the same shape persistUpstreamCatalog writes.
			data, _ := json.MarshalIndent(page, "", "  ")
			return data
		}()
		if err := os.WriteFile(path, stamped, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := LoadPersistedUpstreamCatalog(); err != nil {
			t.Fatalf("load: %v", err)
		}
		if got := lookupOne("claude-fable-5-1").InputPricePerM; got != 7 {
			t.Fatalf("persisted facts not reinstalled: input %.2f, want 7.00", got)
		}
		when, source, count, ok := UpstreamCatalogInfo()
		if !ok || source != "persisted-test" || count != 1 || when.IsZero() {
			t.Fatalf("info after load = (%v, %q, %d, %v)", when, source, count, ok)
		}
	})

	t.Run("no home", func(t *testing.T) {
		t.Setenv("HOME", "")
		if err := LoadPersistedUpstreamCatalog(); err != nil {
			t.Fatalf("no home must be a clean no-op, got %v", err)
		}
	})
}
