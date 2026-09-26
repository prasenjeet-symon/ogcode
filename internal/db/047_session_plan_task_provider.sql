-- +goose Up
-- Records which provider serves the row's model. A model id can be served by
-- more than one provider (glm-5.3-flash by both OGX and a Z.ai OpenAI-compatible
-- endpoint), so the id alone is ambiguous and resolving it by catalog search
-- picked a different provider run to run. Empty means no provider was recorded —
-- a legacy row — and the registry resolves it by model id.
ALTER TABLE session ADD COLUMN provider TEXT NOT NULL DEFAULT '';
ALTER TABLE plan ADD COLUMN provider TEXT NOT NULL DEFAULT '';
ALTER TABLE task ADD COLUMN provider TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE task DROP COLUMN provider;
ALTER TABLE plan DROP COLUMN provider;
ALTER TABLE session DROP COLUMN provider;
