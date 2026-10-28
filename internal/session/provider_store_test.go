package session

import (
	"path/filepath"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

// TestDeleteProviderConfig pins the removal half of the provider_config CRUD:
// a delete drops exactly the row it names, leaves the others untouched, and a
// delete of an id with no row is a no-op (the caller-facing DELETE route is
// idempotent on top of this).
func TestDeleteProviderConfig(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	for _, id := range []string{"vendor-a", "vendor-b"} {
		if err := SetProviderConfig(database, &ProviderConfig{
			ProviderID: id,
			APIKey:     "sk-" + id,
			BaseURL:    "https://" + id + ".example/v1",
		}); err != nil {
			t.Fatalf("set %s: %v", id, err)
		}
	}

	if err := DeleteProviderConfig(database, "vendor-a"); err != nil {
		t.Fatalf("delete vendor-a: %v", err)
	}
	rows, err := GetAllProviderConfigs(database)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].ProviderID != "vendor-b" {
		t.Fatalf("expected only vendor-b to remain, got %d rows", len(rows))
	}

	// Deleting an id with no row is a no-op, not an error.
	if err := DeleteProviderConfig(database, "vendor-a"); err != nil {
		t.Fatalf("delete missing row: %v", err)
	}
}
