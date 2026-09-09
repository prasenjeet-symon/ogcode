package memfile

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/codemap"
	"github.com/prasenjeet-symon/ogcode/internal/db"
)

// Entry is one indexed turn summary. path is the primary key and holds the
// absolute file path; outline is the rendered markdown heading tree (with line
// ranges) that lets recall jump straight to a section.
type Entry struct {
	Path      string
	SessionID string
	ProjectID string
	Title     string
	Outline   string
	CreatedAt int64 // unix millis; the turn's time, used for temporal ordering
	IndexedAt int64 // unix millis; when this row was written
}

// Store is the incremental index over a project's turn-summary folder. It
// mirrors internal/docindex: existence-keyed, one row per file. Turn summaries
// are immutable once written, so an existence check is a complete freshness
// check — a file is indexed exactly once and never re-parsed.
type Store struct {
	db *db.DB
}

// NewStore creates a Store backed by the given (project-local) database.
func NewStore(database *db.DB) *Store {
	return &Store{db: database}
}

// IsIndexed reports whether a row already exists for path.
func (s *Store) IsIndexed(path string) (bool, error) {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM memory_turn_index WHERE path = ?`, path).Scan(&count); err != nil {
		return false, fmt.Errorf("memfile: check indexed: %w", err)
	}
	return count > 0, nil
}

// Upsert inserts or replaces one index row.
func (s *Store) Upsert(e *Entry) error {
	if e.IndexedAt == 0 {
		e.IndexedAt = time.Now().UnixMilli()
	}
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO memory_turn_index (path, session_id, project_id, title, outline, created_at, indexed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.Path, e.SessionID, e.ProjectID, e.Title, e.Outline, e.CreatedAt, e.IndexedAt,
	)
	if err != nil {
		return fmt.Errorf("memfile: upsert: %w", err)
	}
	return nil
}

// IndexFile parses one summary file's heading outline and upserts its row. It is
// the incremental unit: called once for a freshly written file, it never touches
// any other row. meta supplies the scoping/attribution the index stores.
func (s *Store) IndexFile(path string, meta Meta) error {
	outline := ""
	if fm, err := codemap.Outline(path); err == nil {
		outline = renderOutline(fm)
	}
	created := meta.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	return s.Upsert(&Entry{
		Path:      path,
		SessionID: meta.SessionID,
		ProjectID: meta.ProjectID,
		Title:     meta.Title,
		Outline:   outline,
		CreatedAt: created.UTC().UnixMilli(),
	})
}

// ListByProject returns every indexed summary for a project, newest first.
func (s *Store) ListByProject(projectID string) ([]*Entry, error) {
	return s.query(
		`SELECT path, session_id, project_id, title, outline, created_at, indexed_at
		 FROM memory_turn_index WHERE project_id = ? ORDER BY created_at DESC, path DESC`,
		projectID,
	)
}

// ListBySession returns every indexed summary for one conversation, newest first.
func (s *Store) ListBySession(sessionID string) ([]*Entry, error) {
	return s.query(
		`SELECT path, session_id, project_id, title, outline, created_at, indexed_at
		 FROM memory_turn_index WHERE session_id = ? ORDER BY created_at DESC, path DESC`,
		sessionID,
	)
}

func (s *Store) query(sqlStr string, args ...any) ([]*Entry, error) {
	rows, err := s.db.Query(sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("memfile: list: %w", err)
	}
	defer rows.Close()
	var entries []*Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// PurgeMissing drops rows for a project whose files no longer exist on disk, so
// a deleted summary stops showing up in recall. Cheap to run: it stats one file
// per indexed row.
func (s *Store) PurgeMissing(projectID string) error {
	entries, err := s.ListByProject(projectID)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if _, err := os.Stat(e.Path); os.IsNotExist(err) {
			if _, err := s.db.Exec(`DELETE FROM memory_turn_index WHERE path = ?`, e.Path); err != nil {
				return fmt.Errorf("memfile: purge: %w", err)
			}
		}
	}
	return nil
}

func scanEntry(rows *sql.Rows) (*Entry, error) {
	var e Entry
	if err := rows.Scan(&e.Path, &e.SessionID, &e.ProjectID, &e.Title, &e.Outline, &e.CreatedAt, &e.IndexedAt); err != nil {
		return nil, fmt.Errorf("memfile: scan: %w", err)
	}
	return &e, nil
}

// renderOutline renders a file's heading symbols as an indented outline with
// line ranges — the same shape file_map prints, so the recall agent reads a
// familiar format and can jump with read(path, start_line, end_line).
func renderOutline(fm *codemap.FileMap) string {
	if fm == nil || len(fm.Symbols) == 0 {
		return ""
	}
	var b strings.Builder
	for _, sym := range fm.Symbols {
		sig := sym.Signature
		if sig == "" {
			sig = sym.Name
		}
		if sym.StartLine == sym.EndLine {
			fmt.Fprintf(&b, "%d  %s\n", sym.StartLine, sig)
		} else {
			fmt.Fprintf(&b, "%d-%d  %s\n", sym.StartLine, sym.EndLine, sig)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
