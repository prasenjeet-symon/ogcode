package session

import (
	"path/filepath"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
)

func TestProviderConcurrencyStore(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	// Nothing stored yet: 0 means "fall back to the environment or the default".
	if got, err := GetProviderConcurrency(database); err != nil || got != 0 {
		t.Fatalf("initial GetProviderConcurrency = %d, %v; want 0, nil", got, err)
	}

	if err := SetProviderConcurrency(database, 12); err != nil {
		t.Fatalf("SetProviderConcurrency: %v", err)
	}
	if got, err := GetProviderConcurrency(database); err != nil || got != 12 {
		t.Fatalf("GetProviderConcurrency after set = %d, %v; want 12, nil", got, err)
	}

	// A second set updates the singleton row rather than failing on the key.
	if err := SetProviderConcurrency(database, 5); err != nil {
		t.Fatalf("SetProviderConcurrency update: %v", err)
	}
	if got, err := GetProviderConcurrency(database); err != nil || got != 5 {
		t.Fatalf("GetProviderConcurrency after update = %d, %v; want 5, nil", got, err)
	}

	// A value below the minimum clears the stored ceiling.
	if err := SetProviderConcurrency(database, 1); err != nil {
		t.Fatalf("SetProviderConcurrency clear: %v", err)
	}
	if got, err := GetProviderConcurrency(database); err != nil || got != 0 {
		t.Fatalf("GetProviderConcurrency after clear = %d, %v; want 0, nil", got, err)
	}

	// A nil database is the no-database case of the CLI paths.
	if got, err := GetProviderConcurrency(nil); err != nil || got != 0 {
		t.Fatalf("nil GetProviderConcurrency = %d, %v; want 0, nil", got, err)
	}
	if err := SetProviderConcurrency(nil, 4); err != nil {
		t.Fatalf("nil SetProviderConcurrency: %v", err)
	}
}

func TestApplyProviderConcurrencyUsesDefaultWhenEmpty(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	t.Setenv("OGCODE_PROVIDER_MAX_CONCURRENT", "")
	t.Cleanup(func() { provider.ApplyConcurrency(0) })

	got, err := ApplyProviderConcurrency(database)
	if err != nil {
		t.Fatalf("ApplyProviderConcurrency: %v", err)
	}
	if got != provider.DefaultMaxConcurrentRequests {
		t.Fatalf("capacity = %d, want default %d", got, provider.DefaultMaxConcurrentRequests)
	}
}
