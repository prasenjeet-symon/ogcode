package session

import (
	"path/filepath"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

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
}

// TestSearchConfigRespectsExplicitDisable is the other half of the contract:
// on-by-default must not mean impossible-to-turn-off. A stored false has to
// survive a round trip, or the settings toggle would silently do nothing.
func TestSearchConfigRespectsExplicitDisable(t *testing.T) {
	database := newSearchTestDB(t)

	if err := SetSearchConfig(database, &SearchConfig{Enabled: false}); err != nil {
		t.Fatalf("set search config: %v", err)
	}
	cfg, err := GetSearchConfig(database)
	if err != nil {
		t.Fatalf("get search config: %v", err)
	}
	if cfg.Enabled {
		t.Error("an explicitly disabled config came back enabled")
	}
}
