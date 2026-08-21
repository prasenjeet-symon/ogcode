package docindex

import (
	"fmt"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/id"
)

// ExcludeEntry is a pattern that the indexer skips during a walk.
// Patterns are matched against directory names and file basenames.
// Glob wildcards (e.g. *.min.js) are supported via filepath.Match.
type ExcludeEntry struct {
	ID        string `json:"id"`
	Directory string `json:"directory"`
	Pattern   string `json:"pattern"`
	CreatedAt int64  `json:"createdAt"`
}

// defaultExcludePatterns is deliberately empty.
//
// It used to seed a directory with the usual suspects — node_modules, vendor,
// dist, build, .venv and so on — which reads as helpful and is not. Those names
// are a guess about a project made without looking at it, and the guess is
// invisible: a project that tracks its vendor directory found it silently
// missing from every search, with nothing anywhere to say why or where to undo
// it. Worse, the guess was seeded into the user's own store, so it became
// indistinguishable from a rule they had written themselves.
//
// A project has already recorded which of its files are noise, in .gitignore,
// under review, in one place. That is what the indexer reads. This list stays
// as the mechanism for excludes the user adds deliberately, and adds nothing on
// their behalf.
var defaultExcludePatterns = []string{}

// ListExcludes returns all exclude patterns stored for the given directory.
func (s *Store) ListExcludes(dir string) ([]*ExcludeEntry, error) {
	rows, err := s.db.Query(
		`SELECT id, directory, pattern, created_at FROM index_excludes WHERE directory = ? ORDER BY pattern ASC`,
		dir,
	)
	if err != nil {
		return nil, fmt.Errorf("list excludes: %w", err)
	}
	defer rows.Close()

	var entries []*ExcludeEntry
	for rows.Next() {
		var e ExcludeEntry
		if err := rows.Scan(&e.ID, &e.Directory, &e.Pattern, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan exclude: %w", err)
		}
		entries = append(entries, &e)
	}
	return entries, rows.Err()
}

// AddExclude inserts a new exclude pattern for a directory. Duplicate patterns are ignored.
func (s *Store) AddExclude(dir, pattern string) (*ExcludeEntry, error) {
	e := &ExcludeEntry{
		ID:        id.NewNoteID(),
		Directory: dir,
		Pattern:   pattern,
		CreatedAt: time.Now().UnixMilli(),
	}
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO index_excludes (id, directory, pattern, created_at) VALUES (?, ?, ?, ?)`,
		e.ID, e.Directory, e.Pattern, e.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("add exclude: %w", err)
	}
	return e, nil
}

// DeleteExclude removes an exclude entry by ID.
func (s *Store) DeleteExclude(excludeID string) error {
	_, err := s.db.Exec(`DELETE FROM index_excludes WHERE id = ?`, excludeID)
	return err
}

// SeedDefaultExcludes inserts the default patterns for a directory if none exist yet.
func (s *Store) SeedDefaultExcludes(dir string) error {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM index_excludes WHERE directory = ?`, dir).Scan(&count); err != nil {
		return fmt.Errorf("count excludes: %w", err)
	}
	if count > 0 {
		return nil
	}
	for _, pattern := range defaultExcludePatterns {
		if _, err := s.AddExclude(dir, pattern); err != nil {
			return err
		}
	}
	return nil
}
