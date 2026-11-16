package session

import (
	"database/sql"
	"fmt"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
)

// GetProviderConcurrency returns the stored machine-wide ceiling on in-flight
// provider requests, or 0 when nothing is stored — the environment variable or
// the default then applies.
func GetProviderConcurrency(database *db.DB) (int, error) {
	if database == nil {
		return 0, nil
	}
	var n int
	err := database.QueryRow(`SELECT provider_max_concurrent FROM ui_prefs WHERE id = 1`).Scan(&n)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get provider concurrency: %w", err)
	}
	if n < provider.MinConcurrentRequests {
		return 0, nil
	}
	return n, nil
}

// SetProviderConcurrency upserts the ceiling on the singleton ui_prefs row.
// Passing 0 clears it, so the environment variable or the default applies again.
func SetProviderConcurrency(database *db.DB, n int) error {
	if database == nil {
		return nil
	}
	if n < provider.MinConcurrentRequests {
		n = 0
	}
	_, err := database.Exec(`
		INSERT INTO ui_prefs (id, provider_max_concurrent, time_updated)
		VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			provider_max_concurrent = excluded.provider_max_concurrent,
			time_updated            = excluded.time_updated
	`, n, Now())
	if err != nil {
		return fmt.Errorf("set provider concurrency: %w", err)
	}
	return nil
}

// ApplyProviderConcurrency applies the stored ceiling to the process-wide
// provider budget, before the process makes any request. The environment
// variable still wins over the stored value. It returns the capacity in force.
func ApplyProviderConcurrency(database *db.DB) (int, error) {
	stored, err := GetProviderConcurrency(database)
	if err != nil {
		return provider.MaxConcurrentRequests(), err
	}
	return provider.ApplyConcurrency(stored), nil
}
