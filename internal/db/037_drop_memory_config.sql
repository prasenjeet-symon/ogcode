-- +goose Up
-- The graph/embedding agentic-memory system has been removed; its settings table
-- is no longer read or written by any code. Drop it.
DROP TABLE IF EXISTS memory_config;

-- +goose Down
-- Recreate the table as it stood after migrations 012 + 027, so a rollback that
-- reintroduces the old code has its settings store back.
CREATE TABLE IF NOT EXISTS memory_config (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    enabled INTEGER NOT NULL DEFAULT 0,
    embed_provider_id TEXT NOT NULL DEFAULT '',
    embed_model TEXT NOT NULL DEFAULT '',
    embed_api_key TEXT NOT NULL DEFAULT '',
    chat_provider_id TEXT NOT NULL DEFAULT '',
    chat_model TEXT NOT NULL DEFAULT '',
    chat_api_key TEXT NOT NULL DEFAULT '',
    time_updated INTEGER NOT NULL,
    embed_base_url TEXT NOT NULL DEFAULT '',
    chat_base_url TEXT NOT NULL DEFAULT ''
);
