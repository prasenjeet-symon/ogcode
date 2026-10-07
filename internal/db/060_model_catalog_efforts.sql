-- +goose Up
-- The effort levels a host reports for a model it serves (Ollama's /api/show
-- thinking metadata, OpenRouter's reasoning object), comma-separated, lowest
-- first, and the one it applies by default. Kept with the rest of the fetched
-- catalogue so the effort picker is right on the first render after a restart,
-- not only after the next refresh. Empty where the host reports none.
ALTER TABLE model_catalog ADD COLUMN efforts        TEXT NOT NULL DEFAULT '';
ALTER TABLE model_catalog ADD COLUMN default_effort TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE model_catalog DROP COLUMN default_effort;
ALTER TABLE model_catalog DROP COLUMN efforts;
