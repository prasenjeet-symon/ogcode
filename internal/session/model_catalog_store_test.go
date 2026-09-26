package session

import (
	"path/filepath"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

func newCatalogDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// TestModelCatalogRoundTrips pins that every field a picker needs survives the
// persistence round trip, so a process seeded from the store renders the same
// list the live fetch would have.
func TestModelCatalogRoundTrips(t *testing.T) {
	database := newCatalogDB(t)

	in := []CatalogModel{
		{
			ID: "m1", Name: "Model One", ProviderID: "openrouter",
			ActiveByDefault: true, SupportsImages: true,
			ContextWindow: 200000, MaxOutputTokens: 8192,
			Collection: "OpenRouter", InputPricePerM: 3, OutputPricePerM: 15,
		},
		{ID: "m2", Name: "Model Two", ProviderID: "openrouter"},
	}
	if err := SetModelCatalog(database, "openrouter", in); err != nil {
		t.Fatalf("set: %v", err)
	}

	got, err := GetModelCatalog(database)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(got), got)
	}
	byID := map[string]CatalogModel{}
	for _, m := range got {
		byID[m.ID] = m
	}
	one := byID["m1"]
	if one.Name != "Model One" || !one.ActiveByDefault || !one.SupportsImages ||
		one.ContextWindow != 200000 || one.MaxOutputTokens != 8192 ||
		one.Collection != "OpenRouter" || one.InputPricePerM != 3 || one.OutputPricePerM != 15 {
		t.Errorf("m1 round trip lost a field: %+v", one)
	}
	if two := byID["m2"]; two.ActiveByDefault || two.SupportsImages || two.ContextWindow != 0 {
		t.Errorf("m2 defaults not preserved: %+v", two)
	}
}

// TestSetModelCatalogReplacesPerProvider pins that a refresh replaces a
// provider's catalogue rather than appending to it: a model the endpoint stopped
// serving must disappear, while another provider's rows are untouched.
func TestSetModelCatalogReplacesPerProvider(t *testing.T) {
	database := newCatalogDB(t)

	if err := SetModelCatalog(database, "openrouter", []CatalogModel{
		{ID: "old", ProviderID: "openrouter"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := SetModelCatalog(database, "ollama", []CatalogModel{
		{ID: "local", ProviderID: "ollama"},
	}); err != nil {
		t.Fatalf("seed ollama: %v", err)
	}

	if err := SetModelCatalog(database, "openrouter", []CatalogModel{
		{ID: "new", ProviderID: "openrouter"},
	}); err != nil {
		t.Fatalf("replace: %v", err)
	}

	got, err := GetModelCatalog(database)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	seen := map[string]bool{}
	for _, m := range got {
		seen[m.ProviderID+"/"+m.ID] = true
	}
	if seen["openrouter/old"] {
		t.Error("a model dropped from the live list lingered after a replace")
	}
	if !seen["openrouter/new"] {
		t.Error("the replacement model is missing")
	}
	if !seen["ollama/local"] {
		t.Error("replacing one provider's catalogue disturbed another's")
	}
}

// TestDeleteModelCatalog pins the removal path a pruned provider takes.
func TestDeleteModelCatalog(t *testing.T) {
	database := newCatalogDB(t)
	if err := SetModelCatalog(database, "ogx", []CatalogModel{{ID: "m", ProviderID: "ogx"}}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := DeleteModelCatalog(database, "ogx"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, err := GetModelCatalog(database)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no rows after delete, got %+v", got)
	}
	// Deleting an absent provider is a no-op, not an error.
	if err := DeleteModelCatalog(database, "ogx"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
}
