-- +goose Up
-- Machine-wide override for the one-shot image-analysis model (the
-- analyze_image tool, run on behalf of sessions whose own model cannot see
-- images). Empty strings mean "inherit from the active session".
ALTER TABLE ui_prefs ADD COLUMN image_model    TEXT NOT NULL DEFAULT '';
ALTER TABLE ui_prefs ADD COLUMN image_provider TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE ui_prefs DROP COLUMN image_provider;
ALTER TABLE ui_prefs DROP COLUMN image_model;
