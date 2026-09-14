-- +goose Up
-- Context window learned from context-overflow errors. The agent loop parses
-- the provider's "maximum context length is N tokens" style bodies and persists
-- the window here, so the next run sizes compaction from a real figure instead
-- of the 128k fallback when the model catalog is silent (Ollama, dynamic
-- OpenAI-compatible endpoints). 0 = unknown. Kept on the model_capability
-- record so one row carries everything known about a model; the upsert-merge
-- in SetModelCapability keeps an image-probe write from clobbering it.
ALTER TABLE model_capability ADD COLUMN context_window INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE model_capability DROP COLUMN context_window;