-- +goose Up
-- Full-text index over a project's markdown memory corpus, answering "which
-- conversation was this about?" for the session palette. One row per document:
-- 'turn' (one dated summary file per turn), 'memory' (the per-session memory
-- file the agent rewrites every turn) and 'session' (a conversation's own title
-- and compaction summary). The rows stay per document — the per-session answers
-- the palette shows are computed by the ranker over them, so there is no second
-- store that can drift out of date.
--
-- Deliberately NOT columns on memory_turn_index: that table is the contract for
-- the agent's memory_map tool, whose design rests on "one row per immutable
-- file, indexed exactly once". Session memory files are rewritten every turn and
-- need mtime freshness, a different invariant; keeping them apart means neither
-- contract bends (internal/memfile/search.go).
--
-- indexed_at doubles as the freshness bookmark: a file whose modification time is
-- later than its row's indexed_at has been rewritten since it was read, which is
-- what RefreshStale stats for.
CREATE TABLE IF NOT EXISTS memory_search (
    ref        TEXT PRIMARY KEY,          -- 'turn:<abs path>' | 'memory:<abs path>' | 'session:<id>'
    kind       TEXT NOT NULL,             -- 'turn' | 'memory' | 'session'
    session_id TEXT NOT NULL,             -- attribution; a 'memory' file resolves via its file name
    project_id TEXT NOT NULL,
    path       TEXT NOT NULL DEFAULT '',
    title      TEXT NOT NULL DEFAULT '',  -- session title / summary H1
    labels     TEXT NOT NULL DEFAULT '[]',-- JSON array of the document's topic labels
    body       TEXT NOT NULL DEFAULT '',  -- capped at 16 KB, frontmatter stripped
    created_at INTEGER NOT NULL DEFAULT 0,-- unix millis
    indexed_at INTEGER NOT NULL           -- unix millis
);
CREATE INDEX IF NOT EXISTS idx_memory_search_project ON memory_search(project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_memory_search_session ON memory_search(session_id);

-- Rowids are stable because memory_search has an implicit integer rowid and the
-- FTS rows are mirrored from Go with that same rowid; removing_diacritics keeps
-- "café" findable from "cafe". The labels column holds the labels space-joined,
-- since the main table stores them as JSON.
CREATE VIRTUAL TABLE IF NOT EXISTS memory_search_fts USING fts5(
    title, labels, body,
    tokenize = 'unicode61 remove_diacritics 2'
);

-- +goose Down
DROP TABLE IF EXISTS memory_search_fts;
DROP TABLE IF EXISTS memory_search;
