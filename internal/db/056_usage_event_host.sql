-- +goose Up
-- The host each call was sent to, when the provider slot points somewhere other
-- than its own default endpoint (the OpenAI slot at Z.ai, the Anthropic slot at
-- DeepSeek). The provider id alone names only the protocol, and a slot can be
-- repointed later, so the host is kept with the row it describes. Empty on rows
-- written before this column existed and on default endpoints.
ALTER TABLE usage_event ADD COLUMN host TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE usage_event DROP COLUMN host;
