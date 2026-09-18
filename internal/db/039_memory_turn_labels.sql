-- +goose Up
-- LLM topic labels for a turn summary, as a JSON array of strings. The summary
-- writer emits a `Topics: a, b, c` line under the H1 (SummarySystemPrompt); the
-- indexer extracts that line into this column, so memory_map can render each
-- conversation as one collapsed line carrying its most common topics — the same
-- folder-label contract codebase_map uses for project folders. '[]' = unlabeled
-- (pre-feature files and turns with no meaningful topics); the title carries
-- the topical signal for those.
ALTER TABLE memory_turn_index ADD COLUMN labels TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE memory_turn_index DROP COLUMN labels;