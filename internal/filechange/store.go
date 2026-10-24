package filechange

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// Store is one project directory's mutation history and read bookmarks. Every
// query carries the directory, so a server in one project never reads another
// project's changes.
type Store struct {
	database *db.DB
	dir      string
}

// Open returns the store for dir. The schema comes from migration 066, which
// runs when the project database opens.
func Open(database *db.DB, dir string) *Store {
	return &Store{database: database, dir: dir}
}

// Dir is the project directory this store covers.
func (s *Store) Dir() string { return s.dir }

// Append writes one permanent history row. The revision is the caller's: the
// in-memory gate has already assigned it, and the table records what the gate
// acted on rather than inventing a second sequence.
func (s *Store) Append(ctx context.Context, c Change) error {
	c = c.capped()
	if c.At.IsZero() {
		c.At = time.Now()
	}
	omitted := 0
	if c.DiffOmitted {
		omitted = 1
	}
	_, err := s.database.ExecContext(ctx,
		`INSERT INTO file_change (directory, path, rev, session_id, agent, tool, note, diff, diff_omitted, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.dir, c.Path, c.Rev, string(c.Session), c.Agent, c.Tool, c.Note, c.Diff, omitted, c.At.Unix())
	if err != nil {
		return fmt.Errorf("record file change: %w", err)
	}
	return nil
}

// Query selects history rows. Path is a FileKey, empty for every file; Before
// pages backwards from a row id.
type Query struct {
	Path   string
	Before int64
	Limit  int
}

// List returns matching changes newest first. It is the review surface: the
// route serves it, and the agent's own catch-up comes from the ring instead.
func (s *Store) List(ctx context.Context, q Query) ([]Change, error) {
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	where := []string{"directory = ?"}
	args := []any{s.dir}
	if q.Path != "" {
		where = append(where, "path = ?")
		args = append(args, q.Path)
	}
	if q.Before > 0 {
		where = append(where, "id < ?")
		args = append(args, q.Before)
	}
	args = append(args, limit)

	rows, err := s.database.QueryContext(ctx,
		`SELECT id, path, rev, session_id, agent, tool, note, diff, diff_omitted, created_at
		 FROM file_change WHERE `+strings.Join(where, " AND ")+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("read file changes: %w", err)
	}
	defer rows.Close()

	changes := []Change{}
	for rows.Next() {
		c, err := scanChange(rows, s.dir)
		if err != nil {
			return nil, err
		}
		changes = append(changes, c)
	}
	return changes, rows.Err()
}

// After returns the changes recorded above rev for one file, oldest first. It
// is the gate's fallback when its in-memory ring cannot cover the window it has
// to explain — after a restart, or more than RingPerFile changes ago.
func (s *Store) After(ctx context.Context, path string, rev int64, limit int) ([]Change, error) {
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	rows, err := s.database.QueryContext(ctx,
		`SELECT id, path, rev, session_id, agent, tool, note, diff, diff_omitted, created_at
		 FROM file_change WHERE directory = ? AND path = ? AND rev > ? ORDER BY rev ASC LIMIT ?`,
		s.dir, path, rev, limit)
	if err != nil {
		return nil, fmt.Errorf("read file changes after rev: %w", err)
	}
	defer rows.Close()

	changes := []Change{}
	for rows.Next() {
		c, err := scanChange(rows, s.dir)
		if err != nil {
			return nil, err
		}
		changes = append(changes, c)
	}
	return changes, rows.Err()
}

// scanChange reads one row of the shared file_change column list.
func scanChange(rows *sql.Rows, dir string) (Change, error) {
	var (
		c       Change
		sess    string
		omitted int
		created int64
	)
	if err := rows.Scan(&c.ID, &c.Path, &c.Rev, &sess, &c.Agent, &c.Tool,
		&c.Note, &c.Diff, &omitted, &created); err != nil {
		return Change{}, fmt.Errorf("scan file change: %w", err)
	}
	c.Directory = dir
	c.Session = session.SessionID(sess)
	c.DiffOmitted = omitted != 0
	c.At = time.Unix(created, 0)
	return c, nil
}

// SaveBookmark records the revision one session last read a file at. It is an
// idempotent upsert and does nothing when the row already carries that rev, so
// a repeat is free even if it slips past the caller's write-through check.
func (s *Store) SaveBookmark(ctx context.Context, b Bookmark) error {
	at := b.At
	if at.IsZero() {
		at = time.Now()
	}
	_, err := s.database.ExecContext(ctx,
		`INSERT INTO file_bookmark (directory, path, session_id, rev, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(directory, path, session_id)
		 DO UPDATE SET rev = excluded.rev, updated_at = excluded.updated_at
		 WHERE file_bookmark.rev <> excluded.rev`,
		s.dir, b.Path, string(b.Session), b.Rev, at.Unix())
	if err != nil {
		return fmt.Errorf("save read bookmark: %w", err)
	}
	return nil
}

// Bookmarks returns every stored bookmark as file → session → revision, which
// is the shape Log restores into.
func (s *Store) Bookmarks(ctx context.Context) (map[string]map[session.SessionID]int64, error) {
	rows, err := s.database.QueryContext(ctx,
		`SELECT path, session_id, rev FROM file_bookmark WHERE directory = ?`, s.dir)
	if err != nil {
		return nil, fmt.Errorf("read bookmarks: %w", err)
	}
	defer rows.Close()

	out := map[string]map[session.SessionID]int64{}
	for rows.Next() {
		var (
			path, sessionID string
			rev             int64
		)
		if err := rows.Scan(&path, &sessionID, &rev); err != nil {
			return nil, fmt.Errorf("scan bookmark: %w", err)
		}
		if out[path] == nil {
			out[path] = map[session.SessionID]int64{}
		}
		out[path][session.SessionID(sessionID)] = rev
	}
	return out, rows.Err()
}

// Revisions returns the highest revision recorded per file, which is what the
// gate re-seeds from so rev keeps counting up across restarts instead of
// repeating numbers in a table meant to be read later.
func (s *Store) Revisions(ctx context.Context) (map[string]int64, error) {
	rows, err := s.database.QueryContext(ctx,
		`SELECT path, MAX(rev) FROM file_change WHERE directory = ? GROUP BY path`, s.dir)
	if err != nil {
		return nil, fmt.Errorf("read revisions: %w", err)
	}
	defer rows.Close()

	out := map[string]int64{}
	for rows.Next() {
		var (
			path string
			rev  int64
		)
		if err := rows.Scan(&path, &rev); err != nil {
			return nil, fmt.Errorf("scan revision: %w", err)
		}
		out[path] = rev
	}
	return out, rows.Err()
}

// PruneBookmarks drops bookmarks whose session no longer exists. A change row
// is history and is never pruned; a bookmark can only protect a session that
// is still there, so keeping the rest would grow the table with the whole
// project's past rather than with its live work.
func (s *Store) PruneBookmarks(ctx context.Context) (int64, error) {
	res, err := s.database.ExecContext(ctx,
		`DELETE FROM file_bookmark
		 WHERE directory = ? AND session_id NOT IN (SELECT id FROM session)`, s.dir)
	if err != nil {
		return 0, fmt.Errorf("prune bookmarks: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
