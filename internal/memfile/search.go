package memfile

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/project"
)

// bodyCapBytes bounds the text stored per indexed document. It is 16 KB rather
// than a round 8 because 14.3% of this project's live turn summaries exceed
// 8 KB, and it is the later half of a long turn — where the "what did this
// become about" evidence sits — that an 8 KB cap truncated. 16 KB truncates only
// a handful of files and still bounds the index (internal/db/073_session_search.sql).
const bodyCapBytes = 16 * 1024

// Document kinds. They are the three shapes a conversation leaves on disk, and
// they rank differently: a 'turn' is one turn of a conversation, a 'memory' file
// is the conversation's own curated notes rewritten every turn, and a 'session'
// row is its title plus compaction summary.
const (
	kindTurn    = "turn"
	kindMemory  = "memory"
	kindSession = "session"
)

// refs are the primary keys of memory_search. Each names one document and, for
// the two file kinds, its absolute path — so a row can be found again by path
// without a second column to keep in sync.
func turnRef(path string) string         { return kindTurn + ":" + path }
func memoryRef(path string) string       { return kindMemory + ":" + path }
func sessionRef(sessionID string) string { return kindSession + ":" + sessionID }

// Search limits mirror the HTTP handler's: a palette asks for a screenful, and
// a client must not be able to ask for the whole corpus. Clamping here as well
// keeps the store honest on its own.
const (
	defaultSearchLimit = 20
	maxSearchLimit     = 50
)

// Hit is one ranked answer to a content search: a conversation, not a turn,
// because "which session was this about?" is the question, and a conversation
// has many turns that can each match.
type Hit struct {
	SessionID    string    `json:"sessionId"`
	Title        string    `json:"title"`
	Model        string    `json:"model"`
	UpdatedAt    int64     `json:"updatedAt"` // unix millis
	Score        float64   `json:"score"`
	MatchedTurns int       `json:"matchedTurns"`
	Source       string    `json:"source"` // "title" | "turn" | "memory"
	Labels       []string  `json:"labels"`
	Snippet      string    `json:"snippet"`
	Turns        []HitTurn `json:"turns"`
}

// HitTurn is one of the best-matching turns behind a Hit, carrying just enough
// to render a row: when it happened, what it was about, and the matched words in
// context.
type HitTurn struct {
	At      int64    `json:"at"`
	Labels  []string `json:"labels"`
	Snippet string   `json:"snippet"`
}

// ReindexStats reports what a project backfill sweep did.
type ReindexStats struct{ Scanned, Indexed, Purged int }

// SearchStore is the full-text index over a project's markdown memory corpus:
// one row per turn summary, per session memory file, and per conversation's
// title/summary, searched as sessions.
//
// It is separate from Store (the memory_turn_index the memory_map tool reads)
// because the two have different freshness contracts — see the migration.
type SearchStore struct{ db *db.DB }

// NewSearchStore creates a SearchStore backed by the given (project-local)
// database.
func NewSearchStore(database *db.DB) *SearchStore { return &SearchStore{db: database} }

// searchDoc is one document on its way into the index.
type searchDoc struct {
	ref       string
	kind      string
	sessionID string
	projectID string
	path      string
	title     string
	labels    []string
	body      string
	createdAt int64 // unix millis
}

// upsert writes one document, mirrored into the FTS table with the same rowid.
//
// The FTS row is deleted and re-inserted rather than updated in place: an fts5
// table's rowid is the only handle the ranker has back to memory_search, so it
// has to stay equal to the main row's — and on the INSERT path that rowid does
// not exist until the main row is written. Doing the three statements in one
// transaction means a crash can never leave the mirror pointing at text the row
// no longer holds.
func (s *SearchStore) upsert(d searchDoc) error {
	if s == nil || s.db == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("memfile: begin search upsert: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM memory_search_fts
		WHERE rowid IN (SELECT rowid FROM memory_search WHERE ref = ?)`, d.ref); err != nil {
		return fmt.Errorf("memfile: clear search fts row: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO memory_search
			(ref, kind, session_id, project_id, path, title, labels, body, created_at, indexed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(ref) DO UPDATE SET
			kind = excluded.kind, session_id = excluded.session_id,
			project_id = excluded.project_id, path = excluded.path,
			title = excluded.title, labels = excluded.labels, body = excluded.body,
			created_at = excluded.created_at, indexed_at = excluded.indexed_at`,
		d.ref, d.kind, d.sessionID, d.projectID, d.path, d.title,
		encodeLabels(d.labels), d.body, d.createdAt, time.Now().UnixMilli()); err != nil {
		return fmt.Errorf("memfile: upsert search row: %w", err)
	}

	var rowid int64
	if err := tx.QueryRow(`SELECT rowid FROM memory_search WHERE ref = ?`, d.ref).Scan(&rowid); err != nil {
		return fmt.Errorf("memfile: read search rowid: %w", err)
	}
	// The labels column of the FTS table holds the labels space-joined: fts5
	// indexes text, while the main row keeps the JSON array.
	if _, err := tx.Exec(`INSERT INTO memory_search_fts (rowid, title, labels, body) VALUES (?, ?, ?, ?)`,
		rowid, d.title, strings.Join(d.labels, " "), d.body); err != nil {
		return fmt.Errorf("memfile: insert search fts row: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("memfile: commit search upsert: %w", err)
	}
	return nil
}

// Delete drops one document and its FTS mirror. A ref that is not indexed is not
// an error: the caller is usually reconciling against the filesystem, where the
// file is already gone.
func (s *SearchStore) Delete(ref string) error {
	if s == nil || s.db == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("memfile: begin search delete: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM memory_search_fts
		WHERE rowid IN (SELECT rowid FROM memory_search WHERE ref = ?)`, ref); err != nil {
		return fmt.Errorf("memfile: delete search fts row: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM memory_search WHERE ref = ?`, ref); err != nil {
		return fmt.Errorf("memfile: delete search row: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("memfile: commit search delete: %w", err)
	}
	return nil
}

// IndexTurn indexes one turn summary file as a 'turn' document. meta is the
// context the caller already knows; the file's own frontmatter wins over it,
// since it is what the summary was written with. body is capped and its
// frontmatter stripped so the indexed text is what a reader would see.
func (s *SearchStore) IndexTurn(path string, meta Meta) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("memfile: read summary %s: %w", path, err)
	}
	fields, body := splitFrontmatter(string(raw))
	created := meta.CreatedAt
	if t, ok := parseFrontmatterTime(fields.createdAt); ok {
		created = t
	}
	return s.upsert(searchDoc{
		ref:       turnRef(path),
		kind:      kindTurn,
		sessionID: firstNonEmpty(fields.sessionID, meta.SessionID),
		projectID: firstNonEmpty(fields.projectID, meta.ProjectID),
		path:      path,
		// The summary's own H1 titles the file better than a fallback when the
		// frontmatter carries none.
		title:     firstNonEmpty(fields.title, firstH1(body), meta.Title),
		labels:    extractTopics(body),
		body:      capBody(body),
		createdAt: created.UnixMilli(),
	})
}

// IndexSessionMemory indexes a conversation's session memory file — the notes
// the agent curates and rewrites every turn. A missing file is not an error: it
// just means this conversation has no memory document, so any stale row goes.
func (s *SearchStore) IndexSessionMemory(projectDir, sessionID string) error {
	if projectDir == "" || sessionID == "" {
		return nil
	}
	path := SessionMemoryPath(projectDir, sessionID)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s.Delete(memoryRef(path))
	}
	if err != nil {
		return fmt.Errorf("memfile: read session memory %s: %w", path, err)
	}
	fields, body := splitFrontmatter(string(raw))
	// An agent writes this file through the ordinary write tool, so it carries
	// frontmatter only sometimes: fall back to the file's own H1 and mtime.
	created := time.Now()
	if info, err := os.Stat(path); err == nil {
		created = info.ModTime()
	}
	if t, ok := parseFrontmatterTime(fields.createdAt); ok {
		created = t
	}
	return s.upsert(searchDoc{
		ref:       memoryRef(path),
		kind:      kindMemory,
		sessionID: sessionID,
		projectID: project.Resolve(projectDir),
		path:      path,
		title:     firstNonEmpty(fields.title, firstH1(body)),
		labels:    extractTopics(body),
		body:      capBody(body),
		createdAt: created.UnixMilli(),
	})
}

// IndexSession indexes a conversation itself: its title and compaction summary.
// A conversation with neither is dropped from the index rather than stored as an
// empty row that would rank on nothing.
func (s *SearchStore) IndexSession(sessionID, title, summary, projectID string, updatedAt int64) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	title = strings.TrimSpace(title)
	summary = strings.TrimSpace(summary)
	if title == "" && summary == "" {
		return s.Delete(sessionRef(sessionID))
	}
	_, body := splitFrontmatter(summary)
	return s.upsert(searchDoc{
		ref:       sessionRef(sessionID),
		kind:      kindSession,
		sessionID: sessionID,
		projectID: projectID,
		title:     title,
		labels:    extractTopics(summary),
		body:      capBody(body),
		createdAt: updatedAt,
	})
}

// splitFrontmatter separates a memory file's leading YAML frontmatter block from
// its markdown body. A file with no frontmatter comes back whole, which is the
// normal shape of the session memory files the agent writes by hand.
func splitFrontmatter(raw string) (summaryFields, string) {
	var f summaryFields
	if !strings.HasPrefix(raw, "---\n") {
		return f, raw
	}
	end := strings.Index(raw[4:], "\n---")
	if end < 0 {
		return f, raw
	}
	block := raw[4 : 4+end]
	body := strings.TrimLeft(raw[4+end+4:], "\r\n")
	for _, line := range strings.Split(block, "\n") {
		key, value, ok := strings.Cut(strings.TrimRight(line, "\r"), ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "title":
			f.title = yamlValue(value)
		case "session_id":
			f.sessionID = yamlValue(value)
		case "project_id":
			f.projectID = yamlValue(value)
		case "session_type":
			f.sessionType = yamlValue(value)
		case "created_at":
			f.createdAt = yamlValue(value)
		}
	}
	return f, body
}

// summaryFields is the frontmatter a summary file may carry (writer.Write emits
// every one of these; hand-written files carry none).
type summaryFields struct {
	title       string
	sessionID   string
	projectID   string
	sessionType string
	createdAt   string
}

// yamlValue unwraps the double-quoted scalar writer.Write emits.
func yamlValue(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		if unquoted, err := strconv.Unquote(v); err == nil {
			return unquoted
		}
		return v[1 : len(v)-1]
	}
	return v
}

// parseFrontmatterTime reads the RFC3339 created_at stamp; an absent or
// malformed one is not an error, it just leaves the caller's own time in place.
func parseFrontmatterTime(v string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(v))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// firstH1 returns a body's first level-one heading, the summary scribe's title.
func firstH1(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		}
	}
	return ""
}

// capBody truncates a document to bodyCapBytes without splitting a rune, so the
// stored text never ends in an invalid UTF-8 sequence.
func capBody(body string) string {
	if len(body) <= bodyCapBytes {
		return body
	}
	cut := bodyCapBytes
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	return body[:cut]
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// ReindexProject is the backfill sweep: it indexes every memory document of a
// project that is missing from the index or has changed on disk, and drops the
// rows whose document (or conversation) is gone. Running it twice over an
// untouched corpus writes nothing the second time — indexed_at is the bookmark
// each source's modification time is compared against.
func (s *SearchStore) ReindexProject(projectDir, projectID string) (ReindexStats, error) {
	var stats ReindexStats
	if s == nil || s.db == nil || projectDir == "" {
		return stats, nil
	}
	if projectID == "" {
		projectID = project.Resolve(projectDir)
	}

	purged, err := s.purgeProject(projectID)
	if err != nil {
		return stats, err
	}
	stats.Purged = purged

	bookmarks, err := s.indexedAtByRef(projectID)
	if err != nil {
		return stats, err
	}

	// One row per turn: .ogcode/memory/*.md, named chronologically.
	dir := MemoryDir(projectDir)
	if entries, err := os.ReadDir(dir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			stats.Scanned++
			path := filepath.Join(dir, entry.Name())
			info, err := entry.Info()
			if err != nil || !isStale(info.ModTime(), bookmarks[turnRef(path)]) {
				continue
			}
			if err := s.IndexTurn(path, Meta{ProjectID: projectID}); err != nil {
				slog.Warn("memfile: backfill turn summary failed", "path", path, "err", err)
				continue
			}
			stats.Indexed++
		}
	}

	sessions, err := s.projectSessions(projectDir, projectID)
	if err != nil {
		return stats, err
	}
	// A session memory file is named by the session key, which is lossy; the
	// conversation it belongs to has to be resolved from the project's sessions.
	// A file whose conversation is gone is skipped, and the purge above drops
	// its row.
	byKey := make(map[string]string, len(sessions))
	for _, sess := range sessions {
		byKey[SessionKey(sess.id)] = sess.id
	}
	if entries, err := os.ReadDir(filepath.Join(dir, sessionSubDir)); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			stats.Scanned++
			sessionID, ok := byKey[strings.TrimSuffix(entry.Name(), ".md")]
			if !ok {
				continue
			}
			path := filepath.Join(dir, sessionSubDir, entry.Name())
			info, err := entry.Info()
			if err != nil || !isStale(info.ModTime(), bookmarks[memoryRef(path)]) {
				continue
			}
			if err := s.IndexSessionMemory(projectDir, sessionID); err != nil {
				slog.Warn("memfile: backfill session memory failed", "path", path, "err", err)
				continue
			}
			stats.Indexed++
		}
	}

	// The conversations themselves, so a session is findable by the name it was
	// given before it ever wrote a turn. The summary is fetched per session and
	// only when stale: compaction summaries are large and mostly unchanged.
	for _, sess := range sessions {
		stats.Scanned++
		if !isStale(time.UnixMilli(sess.updatedAt), bookmarks[sessionRef(sess.id)]) {
			continue
		}
		title, summary, err := s.sessionTitleSummary(sess.id)
		if err != nil {
			slog.Warn("memfile: backfill session failed", "session", sess.id, "err", err)
			continue
		}
		if err := s.IndexSession(sess.id, title, summary, projectID, sess.updatedAt); err != nil {
			slog.Warn("memfile: backfill session failed", "session", sess.id, "err", err)
			continue
		}
		stats.Indexed++
	}
	return stats, nil
}

// RefreshStale re-indexes this project's file-backed documents whose file has
// changed since it was read, and drops the rows whose file is gone. It is the
// cheap half of ReindexProject — a stat per indexed file — run before every
// search so an agent that edited its own session memory mid-turn is searchable
// immediately, the invariant memory_turn_index deliberately does not have.
func (s *SearchStore) RefreshStale(projectDir string) (int, error) {
	if s == nil || s.db == nil || projectDir == "" {
		return 0, nil
	}
	rows, err := s.db.Query(`SELECT ref, kind, path, session_id, indexed_at
		FROM memory_search WHERE project_id = ? AND kind IN (?, ?)`,
		project.Resolve(projectDir), kindTurn, kindMemory)
	if err != nil {
		return 0, fmt.Errorf("memfile: read search bookmarks: %w", err)
	}
	type staleRow struct {
		ref, kind, path, sessionID string
		indexedAt                  int64
	}
	var stale []staleRow
	for rows.Next() {
		var r staleRow
		if err := rows.Scan(&r.ref, &r.kind, &r.path, &r.sessionID, &r.indexedAt); err != nil {
			rows.Close()
			return 0, fmt.Errorf("memfile: scan search bookmark: %w", err)
		}
		stale = append(stale, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, fmt.Errorf("memfile: scan search bookmarks: %w", err)
	}

	projectID := project.Resolve(projectDir)
	refreshed := 0
	for _, r := range stale {
		info, err := os.Stat(r.path)
		if os.IsNotExist(err) {
			if s.Delete(r.ref) == nil {
				refreshed++
			}
			continue
		}
		if err != nil || !isStale(info.ModTime(), r.indexedAt) {
			continue
		}
		var indexErr error
		switch r.kind {
		case kindTurn:
			indexErr = s.IndexTurn(r.path, Meta{SessionID: r.sessionID, ProjectID: projectID})
		case kindMemory:
			indexErr = s.IndexSessionMemory(projectDir, r.sessionID)
		}
		if indexErr != nil {
			slog.Warn("memfile: refresh search document failed", "path", r.path, "err", indexErr)
			continue
		}
		refreshed++
	}
	return refreshed, nil
}

// Search answers a query with the conversations it most likely means, best
// first. An empty (never nil) slice means "no match", and the project index is
// built on demand the first time so a fresh process answers the first keystroke
// instead of showing an empty palette.
func (s *SearchStore) Search(ctx context.Context, projectID, projectDir, query string, limit int) ([]Hit, error) {
	if s == nil || s.db == nil {
		return []Hit{}, nil
	}
	if projectID == "" {
		projectID = project.Resolve(projectDir)
	}
	terms := tokenizeQuery(query)
	if len(terms) == 0 {
		return []Hit{}, nil
	}
	limit = clampSearchLimit(limit)

	if projectDir != "" {
		var indexed int
		if err := s.db.QueryRowContext(ctx,
			`SELECT count(*) FROM memory_search WHERE project_id = ?`, projectID).Scan(&indexed); err != nil {
			return nil, fmt.Errorf("memfile: count search rows: %w", err)
		}
		if indexed == 0 {
			// Never indexed in this process: the caller gets a real answer on the
			// first keystroke rather than an empty palette, and the next one is
			// served from the index.
			if _, err := s.ReindexProject(projectDir, projectID); err != nil {
				slog.Warn("memfile: search backfill failed", "dir", projectDir, "err", err)
			}
		} else if _, err := s.RefreshStale(projectDir); err != nil {
			slog.Warn("memfile: search refresh failed", "dir", projectDir, "err", err)
		}
	}

	rows, sessions, err := s.matches(ctx, projectID, terms)
	if err != nil {
		return nil, err
	}
	hits := rankHits(rows, sessions, terms, time.Now())
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// termMatchSQL is one term's candidate query. It returns every document that
// matches, with its per-term score and its snippet, joined to the session table:
// the INNER JOIN drops rows whose conversation no longer exists and hands back
// the title, model and timestamp the palette shows in one trip.
const termMatchSQL = `SELECT m.rowid, m.session_id, m.kind, m.title, m.labels, m.created_at,
       -bm25(memory_search_fts) AS score,
       snippet(memory_search_fts, 2, char(2), char(3), '…', 12) AS snip,
       s.title AS session_title, s.model, s.time_updated
  FROM memory_search_fts
  JOIN memory_search m ON m.rowid = memory_search_fts.rowid
  JOIN session s ON s.id = m.session_id
 WHERE memory_search_fts MATCH ? AND m.project_id = ?`

// matches runs one MATCH per query term and unions the results. The terms are
// intersected per session by rankHits, never ANDed in one MATCH: a per-turn row
// holds one turn, so a multi-term AND would demand that every word appear in the
// SAME turn — which is exactly the evidence a conversation usually splits across
// turns (9 of 60 cross-turn queries found vs 57 with the union).
func (s *SearchStore) matches(ctx context.Context, projectID string, terms []string) ([]rankRow, map[string]sessionMeta, error) {
	rowIndex := make(map[int64]*rankRow)
	sessions := make(map[string]sessionMeta)
	merge := func(found []termRow, term int, weight float64) {
		for _, tr := range found {
			row, ok := rowIndex[tr.rowid]
			if !ok {
				row = &rankRow{
					RowID:     tr.rowid,
					SessionID: tr.sessionID,
					Kind:      tr.kind,
					Title:     tr.title,
					Labels:    tr.labels,
					CreatedAt: tr.createdAt,
					Snippet:   tr.snippet,
					Scores:    make(map[int]float64, len(terms)),
				}
				rowIndex[tr.rowid] = row
			}
			// Best of the two variants for this term, never their sum: a singular
			// fallback is the same evidence at a discount, not extra evidence.
			if weighted := tr.score * weight; weighted > row.Scores[term] {
				row.Scores[term] = weighted
			}
			if _, seen := sessions[tr.sessionID]; !seen {
				sessions[tr.sessionID] = sessionMeta{
					Title:     tr.sessionTitle,
					Model:     tr.model,
					UpdatedAt: tr.updatedAt,
				}
			}
		}
	}

	for i, term := range terms {
		exact, err := s.termMatches(ctx, projectID, ftsExpr(term))
		if err != nil {
			return nil, nil, err
		}
		merge(exact, i, 1)

		// Gated singular fallback: a user typing "migrations" means the session
		// that says "migration", and that query found it 6.7% of the time. Always
		// expanding costs 0.6–4 points on exact-word queries, so it fires only
		// when the exact term is rare enough that nothing precise is lost.
		distinct := make(map[string]struct{}, len(exact))
		for _, tr := range exact {
			distinct[tr.sessionID] = struct{}{}
		}
		if len(distinct) >= pluralGateSessions {
			continue
		}
		singular := singularForm(term)
		if singular == "" || singular == term {
			continue
		}
		fallback, err := s.termMatches(ctx, projectID, ftsExpr(singular))
		if err != nil {
			return nil, nil, err
		}
		merge(fallback, i, pluralWeight)
	}

	rows := make([]rankRow, 0, len(rowIndex))
	for _, row := range rowIndex {
		rows = append(rows, *row)
	}
	return rows, sessions, nil
}

// termMatches runs one MATCH and returns every matching document.
func (s *SearchStore) termMatches(ctx context.Context, projectID, expr string) ([]termRow, error) {
	rows, err := s.db.QueryContext(ctx, termMatchSQL, expr, projectID)
	if err != nil {
		return nil, fmt.Errorf("memfile: search term %s: %w", expr, err)
	}
	defer rows.Close()
	var out []termRow
	for rows.Next() {
		var tr termRow
		var labels string
		if err := rows.Scan(&tr.rowid, &tr.sessionID, &tr.kind, &tr.title, &labels, &tr.createdAt,
			&tr.score, &tr.snippet, &tr.sessionTitle, &tr.model, &tr.updatedAt); err != nil {
			return nil, fmt.Errorf("memfile: scan search hit: %w", err)
		}
		tr.labels = decodeLabels(labels)
		out = append(out, tr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memfile: scan search hits: %w", err)
	}
	return out, nil
}

// purgeProject drops the rows of a project whose document or conversation is
// gone: a deleted session takes its memory files' rows with it, and a deleted or
// renamed file must not keep answering queries.
func (s *SearchStore) purgeProject(projectID string) (int, error) {
	purged := 0

	rows, err := s.db.Query(`SELECT ref FROM memory_search
		WHERE project_id = ? AND session_id NOT IN (SELECT id FROM session)`, projectID)
	if err != nil {
		return 0, fmt.Errorf("memfile: find orphaned search rows: %w", err)
	}
	var orphans []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			rows.Close()
			return 0, fmt.Errorf("memfile: scan orphaned search row: %w", err)
		}
		orphans = append(orphans, ref)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, fmt.Errorf("memfile: scan orphaned search rows: %w", err)
	}

	rows, err = s.db.Query(`SELECT ref, path FROM memory_search
		WHERE project_id = ? AND kind IN (?, ?) AND path <> ''`, projectID, kindTurn, kindMemory)
	if err != nil {
		return 0, fmt.Errorf("memfile: find missing search documents: %w", err)
	}
	for rows.Next() {
		var ref, path string
		if err := rows.Scan(&ref, &path); err != nil {
			rows.Close()
			return 0, fmt.Errorf("memfile: scan search document: %w", err)
		}
		if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
			orphans = append(orphans, ref)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, fmt.Errorf("memfile: scan search documents: %w", err)
	}

	for _, ref := range orphans {
		if err := s.Delete(ref); err != nil {
			slog.Warn("memfile: purge search row failed", "ref", ref, "err", err)
			continue
		}
		purged++
	}
	return purged, nil
}

// indexedAtByRef returns this project's freshness bookmarks, keyed by ref.
func (s *SearchStore) indexedAtByRef(projectID string) (map[string]int64, error) {
	rows, err := s.db.Query(`SELECT ref, indexed_at FROM memory_search WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, fmt.Errorf("memfile: read search bookmarks: %w", err)
	}
	defer rows.Close()
	out := make(map[string]int64)
	for rows.Next() {
		var ref string
		var at int64
		if err := rows.Scan(&ref, &at); err != nil {
			return nil, fmt.Errorf("memfile: scan search bookmark: %w", err)
		}
		out[ref] = at
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memfile: scan search bookmarks: %w", err)
	}
	return out, nil
}

// projectSession is the identity of one conversation in this project, as the
// backfill sweep needs it.
type projectSession struct {
	id        string
	updatedAt int64
}

// projectSessions lists this project's conversations, newest first. A session's
// project_id is the directory it was created in; directory is the same value on
// the rows the server creates, and both spellings are matched so a project
// reached through a symlinked path is not invisible.
func (s *SearchStore) projectSessions(projectDir, projectID string) ([]projectSession, error) {
	rows, err := s.db.Query(`SELECT id, time_updated FROM session
		WHERE directory = ? OR project_id = ? ORDER BY time_updated DESC`, projectDir, projectID)
	if err != nil {
		return nil, fmt.Errorf("memfile: list project sessions: %w", err)
	}
	defer rows.Close()
	var out []projectSession
	for rows.Next() {
		var sess projectSession
		if err := rows.Scan(&sess.id, &sess.updatedAt); err != nil {
			return nil, fmt.Errorf("memfile: scan project session: %w", err)
		}
		out = append(out, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memfile: list project sessions: %w", err)
	}
	return out, nil
}

// sessionTitleSummary reads one conversation's title and compaction summary.
func (s *SearchStore) sessionTitleSummary(sessionID string) (string, string, error) {
	var title, summary string
	err := s.db.QueryRow(`SELECT title, compaction_summary FROM session WHERE id = ?`, sessionID).
		Scan(&title, &summary)
	if err == sql.ErrNoRows {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("memfile: read session %s: %w", sessionID, err)
	}
	return title, summary, nil
}

// isStale reports whether a source changed after its row was written.
//
// The comparison is at millisecond granularity, the resolution indexed_at is
// stored at: a file's mtime carries nanoseconds, so a plain After would read
// every document as newer than the row written moments after it and re-index the
// whole corpus on every search. A missing bookmark (0) reads as stale, so a
// document that was never indexed is refreshed once.
func isStale(source time.Time, indexedAtMillis int64) bool {
	if indexedAtMillis <= 0 {
		return true
	}
	return source.UnixMilli() > indexedAtMillis
}

// clampSearchLimit keeps a caller from asking for more rows than a palette can
// show.
func clampSearchLimit(limit int) int {
	if limit <= 0 {
		return defaultSearchLimit
	}
	if limit > maxSearchLimit {
		return maxSearchLimit
	}
	return limit
}

// hitsByScoreDesc orders final hits: score, then the fresher conversation, then
// the id — the last key only so two equal rows never swap places between runs.
func hitsByScoreDesc(hits []Hit) {
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].UpdatedAt != hits[j].UpdatedAt {
			return hits[i].UpdatedAt > hits[j].UpdatedAt
		}
		return hits[i].SessionID < hits[j].SessionID
	})
}
