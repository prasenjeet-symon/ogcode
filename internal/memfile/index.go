package memfile

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

// Entry is one indexed turn summary. path is the primary key and holds the
// absolute file path. Labels are the topics the summary writer called out on
// its `Topics:` line — what memory_map ranks and shows on a conversation's
// collapsed line.
type Entry struct {
	Path      string
	SessionID string
	ProjectID string
	Labels    []string
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

// Upsert inserts or replaces one index row.
func (s *Store) Upsert(e *Entry) error {
	if e.IndexedAt == 0 {
		e.IndexedAt = time.Now().UnixMilli()
	}
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO memory_turn_index (path, session_id, project_id, labels, created_at, indexed_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		e.Path, e.SessionID, e.ProjectID, encodeLabels(e.Labels), e.CreatedAt, e.IndexedAt,
	)
	if err != nil {
		return fmt.Errorf("memfile: upsert: %w", err)
	}
	return nil
}

// IndexFile upserts one summary file's index row. It is the incremental unit:
// called once for a freshly written file, it never touches any other row. meta
// supplies the scoping/attribution the index stores.
func (s *Store) IndexFile(path string, meta Meta) error {
	created := meta.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	return s.Upsert(&Entry{
		Path:      path,
		SessionID: meta.SessionID,
		ProjectID: meta.ProjectID,
		Labels:    extractTopicsFromFile(path),
		CreatedAt: created.UTC().UnixMilli(),
	})
}

// ListByProject returns every indexed summary for a project, newest first.
func (s *Store) ListByProject(projectID string) ([]*Entry, error) {
	return s.query(
		`SELECT path, session_id, project_id, labels, created_at, indexed_at
		 FROM memory_turn_index WHERE project_id = ? ORDER BY created_at DESC, path DESC`,
		projectID,
	)
}

// ListBySession returns every indexed summary for one conversation, newest first.
func (s *Store) ListBySession(sessionID string) ([]*Entry, error) {
	return s.query(
		`SELECT path, session_id, project_id, labels, created_at, indexed_at
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
	var labelsJSON string
	if err := rows.Scan(&e.Path, &e.SessionID, &e.ProjectID, &labelsJSON, &e.CreatedAt, &e.IndexedAt); err != nil {
		return nil, fmt.Errorf("memfile: scan: %w", err)
	}
	e.Labels = decodeLabels(labelsJSON)
	return &e, nil
}

// topicLabelCap is the maximum number of topics kept from one summary's
// `Topics:` line. Generous for a single turn — memory_map's per-conversation
// line re-ranks these across the conversation's turns and caps again there.
const topicLabelCap = 8

// encodeLabels marshals labels for the labels column, never nil — the column is
// NOT NULL DEFAULT '[]'.
func encodeLabels(labels []string) string {
	if len(labels) == 0 {
		return "[]"
	}
	b, err := json.Marshal(labels)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// decodeLabels unmarshals the labels column, mapping any failure (or a null) to
// an empty slice so one malformed row can never break listing.
func decodeLabels(raw string) []string {
	if raw == "" {
		return []string{}
	}
	var labels []string
	if err := json.Unmarshal([]byte(raw), &labels); err != nil {
		return []string{}
	}
	if labels == nil {
		return []string{}
	}
	return labels
}

// extractTopicsFromFile reads a summary file and pulls the topics its body
// called out on a `Topics:` line (SummarySystemPrompt mandates one right under
// the H1). Read errors and absent lines both yield an empty (unlabeled) entry —
// the title carries the topical signal then.
func extractTopicsFromFile(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return extractTopics(string(raw))
}

// extractTopics finds the first `Topics:` line in a summary body and returns its
// comma-separated items, trimmed, deduplicated (case-insensitively — keep the
// first spelling), and capped. Everything after the first `Topics:` line is
// ignored, so a section heading later in the body can't hijack the parse.
func extractTopics(body string) []string {
	var (
		labels   []string
		seen     = make(map[string]struct{})
		inFM     bool
		fmClosed bool
	)
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if !fmClosed {
			if trimmed == "---" {
				if inFM {
					fmClosed = true
				} else {
					inFM = true
				}
				continue
			}
			if inFM {
				continue // frontmatter keys are not topics
			}
		}
		if !strings.HasPrefix(strings.ToLower(trimmed), "topics:") {
			continue
		}
		items := strings.Split(trimmed[len("topics:"):], ",")
		for _, item := range items {
			label := strings.TrimSpace(item)
			label = strings.Trim(label, `"`)
			if label == "" {
				continue
			}
			key := strings.ToLower(label)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			labels = append(labels, label)
			if len(labels) == topicLabelCap {
				return labels
			}
		}
		return labels // only the first Topics line counts
	}
	return labels
}
