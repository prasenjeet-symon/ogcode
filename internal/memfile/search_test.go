package memfile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/project"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// searchEnv is one project on disk with its own database: a memory corpus to
// index and a session table to join against.
type searchEnv struct {
	t    *testing.T
	dir  string // the project directory as the caller spells it
	pid  string // the same directory as project.Resolve spells it, which is what rows store
	db   *db.DB
	sess *session.Store
	idx  *SearchStore
}

func newSearchEnv(t *testing.T) *searchEnv {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return &searchEnv{
		t:    t,
		dir:  dir,
		pid:  project.Resolve(dir),
		db:   database,
		sess: session.NewStore(database),
		idx:  NewSearchStore(database),
	}
}

// seedSession puts a conversation in the session table. Every hit is rendered
// from this row, so a document without one is invisible by design.
func (e *searchEnv) seedSession(id, title, model string) {
	e.t.Helper()
	e.seedSessionAs(id, title, model, "")
}

// seedSessionAs puts a conversation of a given kind in the session table, so a
// test can seed the sub-agent and headless rows search must skip.
func (e *searchEnv) seedSessionAs(id, title, model, sessionType string) {
	e.t.Helper()
	if err := e.sess.Create(&session.Session{
		ID:          session.SessionID(id),
		ProjectID:   e.pid,
		Directory:   e.pid,
		Title:       title,
		Model:       model,
		SessionType: sessionType,
		CreatedAt:   session.Now(),
		UpdatedAt:   session.Now(),
	}); err != nil {
		e.t.Fatalf("seed session %s: %v", id, err)
	}
}

// turnSummaryBody is what the memory scribe writes: one H1, its Topics: line,
// then the sections.
func turnSummaryBody(title, topics, section string) string {
	return "# " + title + "\n\nTopics: " + topics + "\n\n## What was done\n\n" + section + "\n"
}

// writeTurnFile writes a turn summary onto disk through the real writer but
// leaves it out of the index, so a test can drive the backfill itself.
func (e *searchEnv) writeTurnFile(sessionID, title, topics, section string, at time.Time) string {
	e.t.Helper()
	path, err := Write(e.dir, Meta{
		SessionID:   sessionID,
		ProjectID:   e.pid,
		SessionType: "build",
		Title:       title,
		CreatedAt:   at,
	}, turnSummaryBody(title, topics, section))
	if err != nil {
		e.t.Fatalf("write turn summary: %v", err)
	}
	return path
}

// writeTurn writes a turn summary and indexes it, the way a finished turn does.
func (e *searchEnv) writeTurn(sessionID, title, topics, section string, at time.Time) string {
	e.t.Helper()
	path := e.writeTurnFile(sessionID, title, topics, section, at)
	if err := e.idx.IndexTurn(path, Meta{SessionID: sessionID, ProjectID: e.pid}); err != nil {
		e.t.Fatalf("index turn summary: %v", err)
	}
	return path
}

// writeMemoryFile writes a session memory file but does not index it.
func (e *searchEnv) writeMemoryFile(sessionID, body string) string {
	e.t.Helper()
	path := SessionMemoryPath(e.dir, sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatalf("create session memory dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		e.t.Fatalf("write session memory: %v", err)
	}
	return path
}

// writeSessionMemory writes and indexes a session memory file, the way a turn
// writing its own notes does.
func (e *searchEnv) writeSessionMemory(sessionID, body string) string {
	e.t.Helper()
	path := e.writeMemoryFile(sessionID, body)
	if err := e.idx.IndexSessionMemory(e.dir, sessionID); err != nil {
		e.t.Fatalf("index session memory: %v", err)
	}
	return path
}

// search asks the store a query and insists the answer is never nil.
func (e *searchEnv) search(query string) []Hit {
	e.t.Helper()
	hits, err := e.idx.Search(context.Background(), "", e.dir, query, 0)
	if err != nil {
		e.t.Fatalf("search %q: %v", query, err)
	}
	if hits == nil {
		e.t.Fatalf("search %q returned a nil slice; the handler marshals it", query)
	}
	return hits
}

// indexedAt reads one document's freshness bookmark, the value a backfill must
// leave alone when nothing changed.
func (e *searchEnv) indexedAt(ref string) int64 {
	e.t.Helper()
	var at int64
	if err := e.db.QueryRow(`SELECT indexed_at FROM memory_search WHERE ref = ?`, ref).Scan(&at); err != nil {
		e.t.Fatalf("read indexed_at for %s: %v", ref, err)
	}
	return at
}

// seedBoardEdge registers a session on the project board, the way board.Open
// registration does. Roots register with an empty spawned_by; teammates and
// task children carry the handle that spawned them — the edges the root-chain
// walk (rootOf) follows.
func (e *searchEnv) seedBoardEdge(handle, sessionID, spawnedBy, kind string) {
	e.t.Helper()
	_, err := e.db.Exec(`INSERT INTO board_agent
		(id, directory, handle, session_id, kind, spawned_by, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 'idle', ?, ?)`,
		"board_"+handle, e.pid, handle, sessionID, kind, spawnedBy, session.Now(), session.Now())
	if err != nil {
		e.t.Fatalf("seed board edge %s: %v", handle, err)
	}
}

// hitFor finds a hit by session id, so an assertion does not depend on the
// order equal-scoring conversations come back in.
func hitFor(hits []Hit, sessionID string) (Hit, bool) {
	for _, hit := range hits {
		if hit.SessionID == sessionID {
			return hit, true
		}
	}
	return Hit{}, false
}

// The index holds one document per turn summary, per session memory file and
// per conversation: three kinds, each written by a different code path.
func TestSearchIndexesAllThreeKinds(t *testing.T) {
	e := newSearchEnv(t)
	e.seedSession("ses_kinds", "Palette wiring", "ogx-deepseek-v4.1-flash")
	e.writeTurn("ses_kinds", "Wired the palette", "palette, keyboard", "Added the shortcut.", time.Now())
	e.writeSessionMemory("ses_kinds", "# Palette notes\n\nTopics: palette\n\nThe shortcut is guarded.\n")
	if err := e.idx.IndexSession("ses_kinds", "Palette wiring", "", e.pid, session.Now()); err != nil {
		t.Fatalf("index session: %v", err)
	}

	rows, err := e.db.Query(`SELECT kind, count(*) FROM memory_search GROUP BY kind`)
	if err != nil {
		t.Fatalf("count stored kinds: %v", err)
	}
	defer rows.Close()
	got := map[string]int{}
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			t.Fatalf("scan kind count: %v", err)
		}
		got[kind] = n
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("scan kind counts: %v", err)
	}
	for _, kind := range []string{kindTurn, kindMemory, kindSession} {
		if got[kind] != 1 {
			t.Errorf("kind %s: stored %d documents, want 1 (all: %v)", kind, got[kind], got)
		}
	}

	var mirrored int
	if err := e.db.QueryRow(`SELECT count(*) FROM memory_search_fts`).Scan(&mirrored); err != nil {
		t.Fatalf("count fts rows: %v", err)
	}
	if mirrored != 3 {
		t.Errorf("fts mirror holds %d rows, want 3 (one per document)", mirrored)
	}
}

// A query must find text in a turn's body, hand back a snippet with fts5's
// sentinels around the matched word, and never match frontmatter metadata: a
// file's YAML header is not something the conversation was about.
func TestSearchFindsByBodyAndSnippet(t *testing.T) {
	e := newSearchEnv(t)
	e.seedSession("ses_body", "Flux work", "ogx-deepseek-v4.1-flash")
	e.writeTurn("ses_body", "Flux work", "storage",
		"The quantum flux capacitor design is settled.", time.Now())

	hits := e.search("capacitor")
	if len(hits) != 1 {
		t.Fatalf("search found %d hits, want 1", len(hits))
	}
	hit := hits[0]
	if hit.SessionID != "ses_body" {
		t.Errorf("hit session = %q, want ses_body", hit.SessionID)
	}
	if hit.Source != kindTurn {
		t.Errorf("hit source = %q, want %q", hit.Source, kindTurn)
	}
	if !strings.Contains(hit.Snippet, "capacitor") {
		t.Errorf("snippet %q does not contain the matched word", hit.Snippet)
	}
	if !strings.Contains(hit.Snippet, "\x02") || !strings.Contains(hit.Snippet, "\x03") {
		t.Errorf("snippet %q lacks the U+0002/U+0003 match sentinels", hit.Snippet)
	}

	// The frontmatter carries session_type, which no reader sees. Indexing it
	// would make a summary findable by a word that never appears in its text.
	if hits := e.search("sentinelzone"); len(hits) != 0 {
		t.Errorf("frontmatter-only text matched: %d hits", len(hits))
	}
}

// The backfill is a sweep, not an append: a second run over an untouched corpus
// must write nothing and must not move a single bookmark.
func TestReindexProjectIsIdempotent(t *testing.T) {
	e := newSearchEnv(t)
	e.seedSession("ses_sweep", "Sweep target", "ogx-deepseek-v4.1-flash")
	first := e.writeTurnFile("ses_sweep", "First turn", "sweep", "One thing.", time.Now().Add(-time.Hour))
	e.writeTurnFile("ses_sweep", "Second turn", "sweep", "Another thing.", time.Now())
	e.writeMemoryFile("ses_sweep", "# Sweep notes\n\nNotes about the sweep.\n")

	stats, err := e.idx.ReindexProject(e.dir, "")
	if err != nil {
		t.Fatalf("first reindex: %v", err)
	}
	// two turn summaries, one session memory file, one conversation
	if stats.Indexed != 4 {
		t.Errorf("first sweep indexed %d documents, want 4", stats.Indexed)
	}
	if stats.Scanned < 4 {
		t.Errorf("first sweep scanned %d sources, want at least 4", stats.Scanned)
	}
	before := e.indexedAt(turnRef(first))

	stats, err = e.idx.ReindexProject(e.dir, "")
	if err != nil {
		t.Fatalf("second reindex: %v", err)
	}
	if stats.Indexed != 0 {
		t.Errorf("second sweep re-indexed %d unchanged documents, want 0", stats.Indexed)
	}
	if after := e.indexedAt(turnRef(first)); after != before {
		t.Errorf("bookmark moved on an unchanged corpus: %d -> %d", before, after)
	}
}

// The first query against a project that has never been indexed backfills on
// its own: a fresh process must answer the first keystroke, not the second.
func TestSearchBackfillsOnFirstQuery(t *testing.T) {
	e := newSearchEnv(t)
	e.seedSession("ses_cold", "Cold start", "ogx-deepseek-v4.1-flash")
	e.writeTurnFile("ses_cold", "Cold start", "capybara", "The capybara escaped.", time.Now())

	hits := e.search("capybara")
	if len(hits) != 1 {
		t.Fatalf("first query found %d hits, want 1", len(hits))
	}
	if hits[0].SessionID != "ses_cold" {
		t.Errorf("hit session = %q, want ses_cold", hits[0].SessionID)
	}
}

// A session memory file is rewritten as the conversation goes on, so the index
// has to notice a changed file and forget the old text.
func TestRefreshStaleReindexesARewrittenMemoryFile(t *testing.T) {
	e := newSearchEnv(t)
	e.seedSession("ses_stale", "Notes that change", "ogx-deepseek-v4.1-flash")
	path := e.writeSessionMemory("ses_stale", "# Notes\n\nTopics: notes\n\nalpha state.\n")

	if hits := e.search("alpha"); len(hits) != 1 {
		t.Fatalf("first text found %d hits, want 1", len(hits))
	}

	if err := os.WriteFile(path, []byte("# Notes\n\nTopics: notes\n\nbanana state.\n"), 0o644); err != nil {
		t.Fatalf("rewrite session memory: %v", err)
	}
	// An explicit future mtime keeps the assertion independent of the
	// filesystem's timestamp resolution.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("touch session memory: %v", err)
	}

	refreshed, err := e.idx.RefreshStale(e.dir)
	if err != nil {
		t.Fatalf("refresh stale: %v", err)
	}
	if refreshed != 1 {
		t.Errorf("refresh re-indexed %d documents, want 1", refreshed)
	}
	if hits := e.search("banana"); len(hits) != 1 {
		t.Errorf("rewritten text found %d hits, want 1", len(hits))
	}
	if hits := e.search("alpha"); len(hits) != 0 {
		t.Errorf("old text still matches: %d hits", len(hits))
	}
}

// A hit is rendered from the session row, so a document whose conversation is
// gone must never surface — deleted, not merely unranked.
func TestSearchDropsGhostSessions(t *testing.T) {
	e := newSearchEnv(t)
	e.seedSession("ses_ghost", "Ghost", "ogx-deepseek-v4.1-flash")
	e.writeTurn("ses_ghost", "Ghost", "ghost", "A phantom turn about kelp.", time.Now())

	if hits := e.search("kelp"); len(hits) != 1 {
		t.Fatalf("before delete: %d hits, want 1", len(hits))
	}
	if _, err := e.db.Exec(`DELETE FROM session WHERE id = ?`, "ses_ghost"); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if hits := e.search("kelp"); len(hits) != 0 {
		t.Errorf("orphaned document surfaced: %d hits", len(hits))
	}

	// The same rule holds for the pure ranker, independently of the join.
	hits := rankHits(
		[]rankRow{{RowID: 1, SessionID: "ses_missing", Kind: kindTurn, Scores: map[int]float64{0: 5}}},
		map[string]sessionMeta{},
		[]string{"kelp"},
		time.Now(),
	)
	if hits == nil {
		t.Fatal("rankHits returned nil")
	}
	if len(hits) != 0 {
		t.Errorf("rankHits kept %d rows with no conversation behind them", len(hits))
	}
}

// A spawned child's documents — its turns, its curated notes and its title —
// are attributed to the root of its spawn chain at index time, so the person's
// search shows one answer for the conversation they actually opened. The kinds
// that keep them visible elsewhere (the memory corpus, result rows) are
// unchanged: only search attribution moves.
func TestSearchExcludesSubAgentSessions(t *testing.T) {
	e := newSearchEnv(t)
	e.seedSessionAs("ses_main", "Kelp loader", "ogx-deepseek-v4.1-flash", "build")
	e.writeTurn("ses_main", "Kelp loader", "kelp", "The kelp loader reads the manifest.", time.Now())
	e.seedBoardEdge("main", "ses_main", "", "session")
	e.seedSessionAs("ses_child", "Task: find kelp", "ogx-deepseek-v4.1-flash", "subagent")
	e.seedBoardEdge("task_1", "ses_child", "main", "task")

	// The child's documents index while the edges above exist: attribution
	// happens at write time, which is what survives the child's deletion.
	e.writeTurn("ses_child", "Task: find kelp", "kelp", "Grepped for kelp across the tree.", time.Now())
	e.writeSessionMemory("ses_child", "# Kelp child notes\n\nTopics: kelp\n\nKelp lives in the loader.\n")
	if err := e.idx.IndexSession("ses_child", "Task: find kelp", "", e.pid, session.Now()); err != nil {
		t.Fatalf("index child session: %v", err)
	}

	hits := e.search("kelp")
	hit, found := hitFor(hits, "ses_main")
	if !found {
		t.Fatalf("root session missing from hits: %+v", hits)
	}
	// The child's matched turn is the root's evidence: it counts on the
	// root's row, the way the roll-up promises.
	if hit.MatchedTurns < 2 {
		t.Errorf("root hit matched %d turns, want the main and child turns merged: %+v", hit.MatchedTurns, hit)
	}
	if child, found := hitFor(hits, "ses_child"); found {
		t.Errorf("sub-agent session surfaced in hits: %+v", child)
	}
	if len(hits) != 1 {
		t.Errorf("query found %d hits, want exactly the root conversation: %+v", len(hits), hits)
	}
}

// A team spawned by a teammate — a chain three deep — rolls all the way to the
// top: the session a human opened, not the middle teammate, owns the answer.
func TestSearchRollsNestedTeamsUpToRoot(t *testing.T) {
	e := newSearchEnv(t)
	e.seedSession("ses_root", "Anchor repair", "ogx-deepseek-v4.1-flash")
	e.writeTurn("ses_root", "Anchor repair", "anchor", "The anchor test flaked twice.", time.Now())
	e.seedBoardEdge("main", "ses_root", "", "session")

	e.seedSessionAs("ses_mid", "teammate: fix flake", "ogx-deepseek-v4.1-flash", "subagent")
	e.seedBoardEdge("mid_team", "ses_mid", "main", "teammate")
	e.writeTurn("ses_mid", "Fix flake", "anchor", "Mid teammate rewrote the retry loop.", time.Now())

	e.seedSessionAs("ses_leaf", "teammate: run race", "ogx-deepseek-v4.1-flash", "subagent")
	e.seedBoardEdge("leaf_team", "ses_leaf", "mid_team", "teammate")
	e.writeTurn("ses_leaf", "Run race", "anchor", "Leaf teammate ran the race detector.", time.Now())

	hits := e.search("anchor")
	if len(hits) != 1 {
		t.Fatalf("nested-team query found %d hits, want 1 at the root: %+v", len(hits), hits)
	}
	if hits[0].SessionID != "ses_root" {
		t.Errorf("hit session = %q, want ses_root", hits[0].SessionID)
	}
	if hits[0].MatchedTurns != 3 {
		t.Errorf("matched turns = %d, want all three levels merged: %+v", hits[0].MatchedTurns, hits[0])
	}
}

// Ephemeral children die with their session row, but their evidence must not:
// the roll-up already attributed the child's documents to the root, so the
// backfill keeps them after the child's session is deleted.
func TestSearchChildContentSurvivesChildSessionDeletion(t *testing.T) {
	e := newSearchEnv(t)
	e.seedSession("ses_root", "Board polish", "ogx-deepseek-v4.1-flash")
	e.writeTurn("ses_root", "Board polish", "polish", "Polished the board claims.", time.Now())
	e.seedBoardEdge("main", "ses_root", "", "session")

	e.seedSessionAs("ses_child", "task: sweep posts", "ogx-deepseek-v4.1-flash", "subagent")
	e.seedBoardEdge("task_9", "ses_child", "main", "task")
	e.writeTurn("ses_child", "Sweep posts", "polish", "The sweep pruned stale claims.", time.Now())

	if hits := e.search("polish"); len(hits) != 1 {
		t.Fatalf("before delete: %d hits, want 1", len(hits))
	}
	if _, err := e.db.Exec(`DELETE FROM session WHERE id = ?`, "ses_child"); err != nil {
		t.Fatalf("delete child session: %v", err)
	}
	stats, err := e.idx.ReindexProject(e.dir, "")
	if err != nil {
		t.Fatalf("reindex after child deletion: %v", err)
	}
	if stats.Purged != 0 {
		t.Errorf("reindex purged %d rows, want 0: the child's evidence belongs to the root", stats.Purged)
	}
	hits := e.search("polish")
	if len(hits) != 1 || hits[0].SessionID != "ses_root" {
		t.Fatalf("after child deletion: %+v, want the root only", hits)
	}
}

// Rows indexed before the roll-up carried the child's own id. The next sweep
// repairs them: purge drops every child-attributed row, and reindexing the
// same files puts the attribution where it now belongs.
func TestSearchReindexesLegacyChildAttribution(t *testing.T) {
	e := newSearchEnv(t)
	e.seedSession("ses_root", "Palette ranker", "ogx-deepseek-v4.1-flash")
	e.writeTurnFile("ses_root", "Palette ranker", "ranker", "Tuned the palette ranker weights.", time.Now())
	e.seedBoardEdge("main", "ses_root", "", "session")

	// No board edge yet: the child is indexed before registration, the
	// legacy shape this test repairs.
	e.seedSessionAs("ses_child", "task: rank probe", "ogx-deepseek-v4.1-flash", "subagent")
	e.writeTurn("ses_child", "Rank probe", "ranker", "The probe measured bm25 spread.", time.Now())
	e.seedBoardEdge("task_5", "ses_child", "main", "task")

	stats, err := e.idx.ReindexProject(e.dir, "")
	if err != nil {
		t.Fatalf("reindex: %v", err)
	}
	if stats.Purged < 1 {
		t.Errorf("reindex purged %d rows, want the legacy child-attributed row", stats.Purged)
	}
	hits := e.search("ranker")
	if len(hits) != 1 || hits[0].SessionID != "ses_root" {
		t.Fatalf("after repair: %+v, want one root hit", hits)
	}
}

// Headless housekeeping sessions are not conversations either: an index run
// must not answer a content search. The ordinary session is the positive
// control — without it a search that returned nothing at all would pass.
func TestSearchExcludesHeadlessSessionTypes(t *testing.T) {
	e := newSearchEnv(t)
	e.seedSessionAs("ses_main", "Kelp loader", "ogx-deepseek-v4.1-flash", "build")
	e.writeTurn("ses_main", "Kelp loader", "kelp", "The kelp loader reads the manifest.", time.Now())

	for i, sessionType := range []string{"note", "index", "search", "memory-recall"} {
		id := "ses_headless_" + string(rune('a'+i))
		e.seedSessionAs(id, "Housekeeping", "ogx-deepseek-v4.1-flash", sessionType)
		e.writeTurn(id, "Housekeeping", sessionType, "A kelp note from housekeeping.", time.Now())
	}

	hits := e.search("kelp")
	if _, found := hitFor(hits, "ses_main"); !found {
		t.Fatalf("the ordinary session is missing from hits, so the exclusion below proves nothing: %+v", hits)
	}
	if len(hits) != 1 {
		t.Errorf("query found %d hits, want only the ordinary session: %+v", len(hits), hits)
	}
	for _, h := range hits {
		if h.SessionID != "ses_main" {
			t.Errorf("headless session %q surfaced in hits: %+v", h.SessionID, h)
		}
	}
}

// Terms that landed in different turns of one conversation still make it an
// answer: a multi-term AND inside one per-turn document would miss this.
func TestSearchMergesTermsFromDifferentTurns(t *testing.T) {
	e := newSearchEnv(t)
	e.seedSession("ses_split", "Two subjects", "ogx-deepseek-v4.1-flash")
	e.writeTurn("ses_split", "Cache work", "cache", "Built the cache layer.", time.Now().Add(-time.Hour))
	e.writeTurn("ses_split", "Rollback work", "migration", "Wrote the migration rollback.", time.Now())

	hits := e.search("cache migration")
	if len(hits) != 1 {
		t.Fatalf("cross-turn query found %d hits, want 1", len(hits))
	}
	if hits[0].MatchedTurns != 2 {
		t.Errorf("matched turns = %d, want 2", hits[0].MatchedTurns)
	}
}

// The singular fallback rescues the plural query nothing exact answered, and is
// gated off once the exact word is common enough that expanding it can only add
// noise.
func TestSearchPluralFallbackIsGated(t *testing.T) {
	t.Run("fires when the exact term is rare", func(t *testing.T) {
		e := newSearchEnv(t)
		e.seedSession("ses_rare", "Rare word", "ogx-deepseek-v4.1-flash")
		e.writeTurn("ses_rare", "Rare word", "schema", "Wrote the migration by hand.", time.Now())

		// The document says "migration"; the index has no stemming, so only the
		// query-side fallback can reach it from "migrations".
		hits := e.search("migrations")
		if len(hits) != 1 {
			t.Fatalf("plural query found %d hits, want 1 via the singular fallback", len(hits))
		}
		if hits[0].SessionID != "ses_rare" {
			t.Errorf("hit session = %q, want ses_rare", hits[0].SessionID)
		}
	})

	t.Run("stays off when the exact term is common", func(t *testing.T) {
		e := newSearchEnv(t)
		at := time.Now()
		for i := 0; i < pluralGateSessions; i++ {
			id := "ses_common_" + string(rune('a'+i))
			e.seedSession(id, "Common word", "ogx-deepseek-v4.1-flash")
			e.writeTurn(id, "Common word", "schema", "Ran the migrations.", at)
		}
		e.seedSession("ses_singular", "Singular word", "ogx-deepseek-v4.1-flash")
		e.writeTurn("ses_singular", "Singular word", "schema", "Ran the migration.", at)

		hits := e.search("migrations")
		if len(hits) != pluralGateSessions {
			t.Fatalf("gated plural query found %d hits, want %d", len(hits), pluralGateSessions)
		}
		if _, found := hitFor(hits, "ses_singular"); found {
			t.Error("the gate did not fire: the singular-only session came back")
		}
	})
}

// A conversation is about its strongest turn, not the sum of all of them:
// otherwise a long session wins on volume against a precise short one.
func TestSearchScoresBestTurnNotTheSum(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	sessions := map[string]sessionMeta{
		"ses_one":  {},
		"ses_many": {},
	}
	rows := []rankRow{
		{RowID: 1, SessionID: "ses_one", Kind: kindTurn, Scores: map[int]float64{0: 9}},
		{RowID: 2, SessionID: "ses_many", Kind: kindTurn, Scores: map[int]float64{0: 9}},
		{RowID: 3, SessionID: "ses_many", Kind: kindTurn, Scores: map[int]float64{0: 1}},
	}

	hits := rankHits(rows, sessions, []string{"widget"}, now)
	want := map[string]float64{
		"ses_one":  9 + breadthBonus,   // best turn + one turn of breadth
		"ses_many": 9 + 2*breadthBonus, // still 9: the second turn adds breadth only
	}
	for sessionID, wantScore := range want {
		hit, found := hitFor(hits, sessionID)
		if !found {
			t.Fatalf("%s missing from %d hits", sessionID, len(hits))
		}
		if hit.Score != wantScore {
			t.Errorf("%s score = %v, want %v", sessionID, hit.Score, wantScore)
		}
	}
}

// Coverage weighting discounts a turn that answered only some of the query's
// terms, so a partial match cannot bank a full body score.
func TestSearchCoverageWeighting(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	sessions := map[string]sessionMeta{
		"ses_both":  {},
		"ses_split": {},
	}
	rows := []rankRow{
		{RowID: 1, SessionID: "ses_both", Kind: kindTurn, Scores: map[int]float64{0: 8, 1: 2}},
		{RowID: 2, SessionID: "ses_split", Kind: kindTurn, Scores: map[int]float64{0: 8}},
		{RowID: 3, SessionID: "ses_split", Kind: kindTurn, Scores: map[int]float64{1: 2}},
	}

	hits := rankHits(rows, sessions, []string{"cache", "migration"}, now)
	want := map[string]float64{
		"ses_both":  10 + breadthBonus,      // one turn covering both terms: (8+2) × 2/2 + 1 breadth
		"ses_split": 8*0.5 + 2*breadthBonus, // best turn covers half the terms: 8 × 1/2 + 2 breadth
	}
	for sessionID, wantScore := range want {
		hit, found := hitFor(hits, sessionID)
		if !found {
			t.Fatalf("%s missing from %d hits", sessionID, len(hits))
		}
		if hit.Score != wantScore {
			t.Errorf("%s score = %v, want %v", sessionID, hit.Score, wantScore)
		}
	}
}

// Breadth stops at five turns, so a 64-turn conversation cannot outrank a
// precise two-turn one merely by having matched more often.
func TestTurnBonusIsCapped(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	long := make([]rankRow, 0, 8)
	for i := 0; i < 8; i++ {
		long = append(long, rankRow{RowID: int64(i + 1), SessionID: "ses_long", Kind: kindTurn,
			Scores: map[int]float64{0: 1}})
	}
	short := []rankRow{
		{RowID: 20, SessionID: "ses_short", Kind: kindTurn, Scores: map[int]float64{0: 1}},
		{RowID: 21, SessionID: "ses_short", Kind: kindTurn, Scores: map[int]float64{0: 1}},
	}

	hits := rankHits(append(long, short...), map[string]sessionMeta{"ses_long": {}, "ses_short": {}},
		[]string{"widget"}, now)
	want := map[string]struct {
		score   float64
		matches int
	}{
		"ses_long":  {1 + float64(breadthBonus*breadthTurnCap), 8},
		"ses_short": {1 + float64(breadthBonus*2), 2},
	}
	for sessionID, expected := range want {
		hit, found := hitFor(hits, sessionID)
		if !found {
			t.Fatalf("%s missing from %d hits", sessionID, len(hits))
		}
		if hit.Score != expected.score {
			t.Errorf("%s score = %v, want %v", sessionID, hit.Score, expected.score)
		}
		if hit.MatchedTurns != expected.matches {
			t.Errorf("%s matched turns = %d, want %d", sessionID, hit.MatchedTurns, expected.matches)
		}
	}
}
