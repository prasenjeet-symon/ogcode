package session

import (
	"path/filepath"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

func TestSearchConfigClampParams(t *testing.T) {
	cases := []struct {
		name string
		in   SearchConfig
		want SearchConfig
	}{
		{"zero → defaults", SearchConfig{}, SearchConfig{FetchTopK: DefaultSearchFetchTopK, PageChars: DefaultSearchPageChars}},
		{"above max → clamped", SearchConfig{FetchTopK: 100, PageChars: 999999}, SearchConfig{FetchTopK: 10, PageChars: 20000}},
		{"below min / negative", SearchConfig{FetchTopK: -1, PageChars: 500}, SearchConfig{FetchTopK: DefaultSearchFetchTopK, PageChars: 1000}},
		{"in range unchanged", SearchConfig{FetchTopK: 3, PageChars: 8000}, SearchConfig{FetchTopK: 3, PageChars: 8000}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.in
			got.clampParams()
			if got.FetchTopK != c.want.FetchTopK || got.PageChars != c.want.PageChars {
				t.Errorf("clampParams(%+v) = {%d,%d}, want {%d,%d}",
					c.in, got.FetchTopK, got.PageChars,
					c.want.FetchTopK, c.want.PageChars)
			}
		})
	}
}

// newSearchTestDB opens a migrated, empty database in a temp dir.
func newSearchTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

// TestSearchConfigDefaultsToEnabled pins the on-by-default contract.
//
// Web search used to require a Node/Playwright install, so shipping it off by
// default was the honest thing to do — enabling it without that setup produced
// a broken tool. The backend is now compiled into the binary, so there is no
// setup step left to gate on, and a fresh install should have deep_search
// working with nothing configured.
func TestSearchConfigDefaultsToEnabled(t *testing.T) {
	cfg, err := GetSearchConfig(newSearchTestDB(t))
	if err != nil {
		t.Fatalf("get search config: %v", err)
	}
	if !cfg.Enabled {
		t.Error("web search should be enabled by default on a fresh database")
	}
	// The defaults must also be usable, not just present.
	if cfg.FetchTopK != DefaultSearchFetchTopK || cfg.PageChars != DefaultSearchPageChars {
		t.Errorf("default params = {%d,%d}, want {%d,%d}",
			cfg.FetchTopK, cfg.PageChars, DefaultSearchFetchTopK, DefaultSearchPageChars)
	}
}

// TestSearchConfigRespectsExplicitDisable is the other half of the contract:
// on-by-default must not mean impossible-to-turn-off. A stored false has to
// survive a round trip, or the settings toggle would silently do nothing.
func TestSearchConfigRespectsExplicitDisable(t *testing.T) {
	database := newSearchTestDB(t)

	if err := SetSearchConfig(database, &SearchConfig{Enabled: false, FetchTopK: 5, PageChars: 7000}); err != nil {
		t.Fatalf("set search config: %v", err)
	}
	cfg, err := GetSearchConfig(database)
	if err != nil {
		t.Fatalf("get search config: %v", err)
	}
	if cfg.Enabled {
		t.Error("an explicitly disabled config came back enabled")
	}
	if cfg.FetchTopK != 5 || cfg.PageChars != 7000 {
		t.Errorf("params = {%d,%d}, want {5,7000}", cfg.FetchTopK, cfg.PageChars)
	}
}
