-- +goose Up
-- The graph/embedding agentic-memory system has been removed; its per-session
-- token-savings counter is no longer read or written by any code (the SSE
-- event that fed it and the UI that displayed it went with the subsystem).
-- Drop it.
ALTER TABLE session DROP COLUMN memory_tokens_saved;

-- +goose Down
ALTER TABLE session ADD COLUMN memory_tokens_saved INTEGER NOT NULL DEFAULT 0;