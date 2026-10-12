package docindex

import (
	"database/sql"
	"fmt"
	"time"
)

// AutoIndexAllowed reports whether the project at dir has consented to being
// indexed automatically once it passes the file limit the caller enforces.
//
// The answer is per directory and defaults to false: a project nobody has
// answered for is asked, not assumed, so the missing-row case and a stored 0
// both read the same way. A nil store or an empty dir is likewise false rather
// than an error — the callers treat "no answer" and "no project" the same way.
func (s *Store) AutoIndexAllowed(dir string) (bool, error) {
	if s == nil || s.db == nil || dir == "" {
		return false, nil
	}
	var allowed int
	err := s.db.QueryRow(
		`SELECT allowed FROM index_consent WHERE directory = ?`, dir,
	).Scan(&allowed)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("read index consent: %w", err)
	}
	return allowed != 0, nil
}

// SetAutoIndexAllowed records the project's answer. It replaces any prior
// answer for the directory, so a toggle is last-write-wins, and it is safe on a
// nil store or an empty dir (a no-op).
func (s *Store) SetAutoIndexAllowed(dir string, allowed bool) error {
	if s == nil || s.db == nil || dir == "" {
		return nil
	}
	v := 0
	if allowed {
		v = 1
	}
	if _, err := s.db.Exec(
		`INSERT OR REPLACE INTO index_consent (directory, allowed, time_updated) VALUES (?, ?, ?)`,
		dir, v, time.Now().UnixMilli(),
	); err != nil {
		return fmt.Errorf("write index consent: %w", err)
	}
	return nil
}
