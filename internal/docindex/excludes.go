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
	// Default marks a pattern ogcode ships rather than one the user wrote. It is
	// derived from the list at read time, never stored, so the panel's label
	// cannot drift from the list it describes.
	Default bool `json:"default"`
}

// defaultExcludePatterns is the list ogcode ships: the installed-dependency
// directories, build output and generated files that are noise on every stack,
// from Node and Python to Go, .NET, Flutter, iOS and Android.
//
// Every entry has to earn its place: a name belongs here only if it is
// generated rather than authored AND holds files the index would otherwise
// read. The extension allowlist (indexer.IsTextFile) already puts __pycache__,
// *.log, .env and .DS_Store out of reach, so patterns for those would exclude
// nothing and make the list look thorough while doing it — as would bin/ and
// site/, which are as often hand-authored as generated.
//
// A pattern matches a directory or file name anywhere in the tree, not only at
// the root (see indexer.isExcluded), so a name here is skipped in every
// directory that carries it. That is what a dependency directory wants, and it
// is why the handful of ambiguous names — bin, site, and the like — stay out.
//
// This list was once removed, and the reason it was removed still governs it: a
// skip list a project cannot see, cannot change and does not agree with is a
// second exclusion policy, written behind its back. What was unacceptable was
// not that ogcode had an opinion — it was that the opinion hid. So the entries
// are seeded into index_excludes as ordinary rows: the panel lists them, marks
// each one as a default, and lets any of them be deleted. A deletion is
// remembered (see SeedDefaultExcludes), so a default removed on purpose does not
// return on the next run.
//
// .gitignore stays the primary, project-owned statement of what is noise. This
// is the floor beneath it, for the generated directory a .gitignore forgot.
var defaultExcludePatterns = []string{
	// Installed dependencies: copies of code the project did not author, and
	// each holds indexable source of its own — vendor/ .go and .php and .rb,
	// Pods/ .swift and .h and .m, bower_components/ .js, .dart_tool/ Dart's
	// package config, and .tox/.nox whole virtualenvs of third-party .py.
	"node_modules", "vendor", "bower_components", "Pods",
	".venv", "venv", ".tox", ".nox", ".dart_tool",
	// Build and test output: regenerated on every build, and several carry
	// generated source the index would otherwise read a second time — target/
	// and obj/ emit .java and .cs, .next/ and .nuxt/ and .output/ emit .js and
	// .json, _build/ its Erlang app terms, htmlcov/ coverage .html.
	"dist", "build", "out", "target", "obj", "_build",
	".next", ".nuxt", ".output", ".svelte-kit", ".astro",
	".gradle", ".terraform", "DerivedData", "htmlcov", "coverage",
	// Tool caches and packaging metadata: small, generated, and full of .json.
	".mypy_cache", ".pytest_cache", ".ruff_cache", "*.egg-info", ".eggs",
	// Editor state a project rarely means to track.
	".idea", ".vscode",
	// Generated bundles, source maps and dependency lockfiles.
	"*.min.js", "*.min.css", "*.map", "*.lock", "*.sum",
	"package-lock.json", "yarn.lock",
}

// DefaultExcludePatterns returns the shipped list, for callers that need to
// describe or apply it without going through the store.
func DefaultExcludePatterns() []string {
	return append([]string(nil), defaultExcludePatterns...)
}

// IsDefaultExclude reports whether pattern is one ogcode ships. Keeping the
// answer in Go rather than a column means the shipped list stays the single
// source of truth, and a release that changes it is reflected the next time the
// panel reads — with no migration and no rows carrying a stale origin.
func IsDefaultExclude(pattern string) bool {
	for _, p := range defaultExcludePatterns {
		if p == pattern {
			return true
		}
	}
	return false
}

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
		e.Default = IsDefaultExclude(e.Pattern)
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

// SeedDefaultExcludes inserts the shipped patterns for a directory, once.
//
// The unit of "once" is a row in index_exclude_seed, not the number of exclude
// rows the directory happens to hold. Seeding is therefore idempotent, and — the
// point of the marker — a default the user deletes stays deleted: seeding keys
// on whether this directory was ever seeded, so a later run does not read a
// shorter list and helpfully put back what was removed.
//
// INSERT OR IGNORE keeps a pattern the user already wrote exactly as they wrote
// it, so seeding can only add.
func (s *Store) SeedDefaultExcludes(dir string) error {
	var seeded int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM index_exclude_seed WHERE directory = ?`, dir).Scan(&seeded); err != nil {
		return fmt.Errorf("check exclude seed: %w", err)
	}
	if seeded > 0 {
		return nil
	}
	for _, pattern := range defaultExcludePatterns {
		if _, err := s.AddExclude(dir, pattern); err != nil {
			return err
		}
	}
	if _, err := s.db.Exec(
		`INSERT OR IGNORE INTO index_exclude_seed (directory, seeded_at) VALUES (?, ?)`,
		dir, time.Now().UnixMilli(),
	); err != nil {
		return fmt.Errorf("record exclude seed: %w", err)
	}
	return nil
}
