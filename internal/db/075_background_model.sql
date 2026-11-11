-- +goose Up
-- Machine-wide override for behind-the-scenes LLM calls (title generation,
-- turn memory summaries, context compaction, command risk assessment, intent
-- tool matching, auto-indexing, deep search, recall, and task sub-agents).
-- Empty strings mean "inherit from the active session".
ALTER TABLE ui_prefs ADD COLUMN background_model    TEXT NOT NULL DEFAULT '';
ALTER TABLE ui_prefs ADD COLUMN background_provider TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE ui_prefs DROP COLUMN background_provider;
ALTER TABLE ui_prefs DROP COLUMN background_model;
