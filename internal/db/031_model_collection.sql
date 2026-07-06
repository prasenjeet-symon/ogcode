-- +goose Up
-- Collection/group name for custom models so OpenAI-compatible providers
-- (Gemini, DeepSeek, Groq, …) added through the OpenAI provider can be grouped
-- together in the UI instead of all appearing under "OpenAI".
ALTER TABLE model_preference ADD COLUMN collection TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE model_preference DROP COLUMN collection;