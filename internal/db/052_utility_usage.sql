-- +goose Up
-- Utility calls — title generation, command risk assessment, context
-- compaction — spend tokens of their own, but their usage is reported on the
-- stream of a call the main-turn accounting never sees, so it was silently
-- dropped from every total. These columns accumulate that usage on the session
-- row so it can be surfaced as a separate figure and folded into the totals.
-- Additive columns, so pre-existing sessions read 0.
ALTER TABLE session ADD COLUMN utility_input INTEGER NOT NULL DEFAULT 0;
ALTER TABLE session ADD COLUMN utility_output INTEGER NOT NULL DEFAULT 0;
ALTER TABLE session ADD COLUMN utility_reasoning INTEGER NOT NULL DEFAULT 0;
ALTER TABLE session ADD COLUMN utility_cache_read INTEGER NOT NULL DEFAULT 0;
ALTER TABLE session ADD COLUMN utility_cache_write INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE session DROP COLUMN utility_input;
ALTER TABLE session DROP COLUMN utility_output;
ALTER TABLE session DROP COLUMN utility_reasoning;
ALTER TABLE session DROP COLUMN utility_cache_read;
ALTER TABLE session DROP COLUMN utility_cache_write;
