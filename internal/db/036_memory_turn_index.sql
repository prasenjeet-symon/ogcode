-- +goose Up
CREATE TABLE IF NOT EXISTS memory_turn_index (
    path TEXT PRIMARY KEY,
    session_id TEXT NOT NULL DEFAULT '',
    project_id TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    outline TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL DEFAULT 0,
    indexed_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_memory_turn_project ON memory_turn_index(project_id);
CREATE INDEX IF NOT EXISTS idx_memory_turn_session ON memory_turn_index(session_id);

-- +goose Down
DROP TABLE IF EXISTS memory_turn_index;
