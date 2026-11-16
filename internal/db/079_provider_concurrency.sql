-- +goose Up
-- Machine-wide ceiling on in-flight provider requests (the process-wide budget
-- in internal/provider/budget.go). 0 means nothing is stored, so the
-- OGCODE_PROVIDER_MAX_CONCURRENT environment variable or the default applies.
ALTER TABLE ui_prefs ADD COLUMN provider_max_concurrent INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE ui_prefs DROP COLUMN provider_max_concurrent;
