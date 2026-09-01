package docindex

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/id"
)

// Store provides persistence for doc_page_index entries.
type Store struct {
	db *db.DB
}

// NewStore creates a new Store backed by the given database.
func NewStore(database *db.DB) *Store {
	return &Store{db: database}
}

// Upsert inserts or replaces a PageEntry in the database.
func (s *Store) Upsert(entry *PageEntry) error {
	if entry.ID == "" {
		entry.ID = id.NewNoteID() // reuse the ULID generator for a unique ID
	}
	if entry.IndexedAt == 0 {
		entry.IndexedAt = time.Now().UnixMilli()
	}
	if entry.Keywords == nil {
		entry.Keywords = []string{}
	}
	if entry.Labels == nil {
		entry.Labels = []string{}
	}
	keywordsJSON, err := json.Marshal(entry.Keywords)
	if err != nil {
		return fmt.Errorf("marshal keywords: %w", err)
	}
	labelsJSON, err := json.Marshal(entry.Labels)
	if err != nil {
		return fmt.Errorf("marshal labels: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT OR REPLACE INTO doc_page_index (id, doc_path, page_num, keywords, labels, indexed_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.DocPath, entry.PageNum,
		string(keywordsJSON), string(labelsJSON), entry.IndexedAt,
	)
	return err
}

// GetByDoc returns all PageEntry rows for a given document path.
func (s *Store) GetByDoc(docPath string) ([]*PageEntry, error) {
	rows, err := s.db.Query(
		`SELECT id, doc_path, page_num, keywords, labels, indexed_at
		 FROM doc_page_index WHERE doc_path = ? ORDER BY page_num ASC`, docPath,
	)
	if err != nil {
		return nil, fmt.Errorf("get by doc: %w", err)
	}
	defer rows.Close()

	var entries []*PageEntry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// DeleteByDoc removes all PageEntry rows for a given document path.
func (s *Store) DeleteByDoc(docPath string) error {
	_, err := s.db.Exec(`DELETE FROM doc_page_index WHERE doc_path = ?`, docPath)
	return err
}

// UpdateLabels updates only the labels field for a specific page.
func (s *Store) UpdateLabels(docPath string, pageNum int, labels []string) error {
	if labels == nil {
		labels = []string{}
	}
	labelsJSON, err := json.Marshal(labels)
	if err != nil {
		return fmt.Errorf("marshal labels: %w", err)
	}
	_, err = s.db.Exec(
		`UPDATE doc_page_index SET labels = ? WHERE doc_path = ? AND page_num = ?`,
		string(labelsJSON), docPath, pageNum,
	)
	return err
}

// dirPrefixFilter returns the SQL LIKE pattern that matches all entries whose
// doc_path lies inside dirPrefix itself, not just under a sibling whose name
// shares the same string prefix — "/proj/internal/tool" must not also match
// "/proj/internal/toolbox". doc_path values are stored as filesystem paths
// (filepath.Join output), so the directory boundary is the OS separator. The
// root prefix "/proj" needs no boundary and must keep matching "/proj/x": a
// separator is appended only when dirPrefix is not already the filesystem
// root. An empty prefix falls back to "%", matching everything, as before.
// LIKE wildcards in real paths ("my_dir", "a%b") are escaped so a folder name
// can never widen the match.
func dirPrefixFilter(dirPrefix string) string {
	if dirPrefix == "" {
		return "%" // preserve the old behaviour: an empty prefix matches everything
	}
	like := dirPrefix
	if !os.IsPathSeparator(dirPrefix[len(dirPrefix)-1]) {
		like += string(os.PathSeparator)
	}
	like = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(like)
	return like + "%"
}

// ListDocsSummary returns one DocSummary per unique doc_path whose path is
// under dirPrefix. It aggregates in a single query and does not load per-page
// label data — callers that need full pages should use GetByDoc.
func (s *Store) ListDocsSummary(dirPrefix string) ([]*DocSummary, error) {
	rows, err := s.db.Query(
		`SELECT doc_path, COUNT(*) AS page_count, MAX(indexed_at) AS indexed_at
		 FROM doc_page_index WHERE doc_path LIKE ? ESCAPE '\'
		 GROUP BY doc_path ORDER BY doc_path`,
		dirPrefixFilter(dirPrefix),
	)
	if err != nil {
		return nil, fmt.Errorf("list docs summary: %w", err)
	}
	defer rows.Close()

	var summaries []*DocSummary
	for rows.Next() {
		var sum DocSummary
		if err := rows.Scan(&sum.DocPath, &sum.PageCount, &sum.IndexedAt); err != nil {
			return nil, fmt.Errorf("scan doc summary: %w", err)
		}
		summaries = append(summaries, &sum)
	}
	return summaries, rows.Err()
}

// ListTextFiles returns all indexed non-PDF, non-DOCX entries whose doc_path
// is under dirPrefix (the boundary is the directory separator, so a sibling
// folder sharing the prefix never matches), ordered by path. Each text/code
// file has exactly one entry (page 1).
func (s *Store) ListTextFiles(dirPrefix string) ([]*PageEntry, error) {
	rows, err := s.db.Query(
		`SELECT id, doc_path, page_num, keywords, labels, indexed_at
		 FROM doc_page_index
		 WHERE doc_path LIKE ? ESCAPE '\' AND LOWER(doc_path) NOT LIKE '%.pdf' AND LOWER(doc_path) NOT LIKE '%.docx'
		 ORDER BY doc_path ASC`,
		dirPrefixFilter(dirPrefix),
	)
	if err != nil {
		return nil, fmt.Errorf("list text files: %w", err)
	}
	defer rows.Close()

	var entries []*PageEntry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// ListPDFFiles returns all indexed PDF entries (page-level) whose doc_path is
// under dirPrefix (directory-boundary semantics, see dirPrefixFilter), ordered
// by path then page number. Each PDF document
// will have one entry per page; callers group them by DocPath.
func (s *Store) ListPDFFiles(dirPrefix string) ([]*PageEntry, error) {
	rows, err := s.db.Query(
		`SELECT id, doc_path, page_num, keywords, labels, indexed_at
		 FROM doc_page_index
		 WHERE doc_path LIKE ? ESCAPE '\' AND LOWER(doc_path) LIKE '%.pdf'
		 ORDER BY doc_path ASC, page_num ASC`,
		dirPrefixFilter(dirPrefix),
	)
	if err != nil {
		return nil, fmt.Errorf("list pdf files: %w", err)
	}
	defer rows.Close()

	var entries []*PageEntry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// ListDocxFiles returns all indexed DOCX entries (page-level) whose doc_path
// is under dirPrefix (directory-boundary semantics, see dirPrefixFilter),
// ordered by path then page number. Each DOCX document
// will have one entry per pseudo-page; callers group them by DocPath.
func (s *Store) ListDocxFiles(dirPrefix string) ([]*PageEntry, error) {
	rows, err := s.db.Query(
		`SELECT id, doc_path, page_num, keywords, labels, indexed_at
		 FROM doc_page_index
		 WHERE doc_path LIKE ? ESCAPE '\' AND LOWER(doc_path) LIKE '%.docx'
		 ORDER BY doc_path ASC, page_num ASC`,
		dirPrefixFilter(dirPrefix),
	)
	if err != nil {
		return nil, fmt.Errorf("list docx files: %w", err)
	}
	defer rows.Close()

	var entries []*PageEntry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// IsDocIndexed reports whether any pages for the given document path exist in the index.
func (s *Store) IsDocIndexed(docPath string) (bool, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM doc_page_index WHERE doc_path = ?`, docPath,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check doc indexed: %w", err)
	}
	return count > 0, nil
}

// ListDocPaths returns the distinct doc_path values under dirPrefix (directory
// boundary semantics, see dirPrefixFilter). It is used by the indexer to detect
// stale entries for files that no longer exist on disk.
func (s *Store) ListDocPaths(dirPrefix string) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT DISTINCT doc_path FROM doc_page_index WHERE doc_path LIKE ? ESCAPE '\' ORDER BY doc_path`,
		dirPrefixFilter(dirPrefix),
	)
	if err != nil {
		return nil, fmt.Errorf("list doc paths: %w", err)
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("scan doc path: %w", err)
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

// DeleteAllByPrefix deletes all entries for docs under dirPrefix (directory
// boundary semantics, see dirPrefixFilter).
func (s *Store) DeleteAllByPrefix(dirPrefix string) error {
	_, err := s.db.Exec(`DELETE FROM doc_page_index WHERE doc_path LIKE ? ESCAPE '\'`, dirPrefixFilter(dirPrefix))
	return err
}

func scanEntry(rows *sql.Rows) (*PageEntry, error) {
	var e PageEntry
	var keywordsJSON, labelsJSON string
	if err := rows.Scan(&e.ID, &e.DocPath, &e.PageNum, &keywordsJSON, &labelsJSON, &e.IndexedAt); err != nil {
		return nil, fmt.Errorf("scan entry: %w", err)
	}
	if err := json.Unmarshal([]byte(keywordsJSON), &e.Keywords); err != nil {
		e.Keywords = []string{}
	}
	if err := json.Unmarshal([]byte(labelsJSON), &e.Labels); err != nil {
		e.Labels = []string{}
	}
	return &e, nil
}
