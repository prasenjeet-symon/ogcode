-- +goose Up
-- User-editable reasoning efforts for custom models (and listed-model overrides).
-- Empty efforts mean unconfigured and fall back to catalogue/host facts, matching
-- the other min/max/value columns' "0 means unknown" convention.
ALTER TABLE model_preference ADD COLUMN efforts TEXT NOT NULL DEFAULT '';
ALTER TABLE model_preference ADD COLUMN default_effort TEXT NOT NULL DEFAULT '';
ALTER TABLE model_preference ADD COLUMN effort_field TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE model_preference DROP COLUMN effort_field;
ALTER TABLE model_preference DROP COLUMN default_effort;
ALTER TABLE model_preference DROP COLUMN efforts;
