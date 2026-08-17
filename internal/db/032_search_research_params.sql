-- +goose Up
-- Deep-research pipeline tuning knobs, surfaced in the settings screen. Defaults
-- match the built-in constants so existing installs behave identically until changed.
ALTER TABLE search_config ADD COLUMN fetch_top_k INTEGER NOT NULL DEFAULT 4;
ALTER TABLE search_config ADD COLUMN page_chars  INTEGER NOT NULL DEFAULT 6000;

-- +goose Down
-- SQLite does not support DROP COLUMN on older versions; leave the columns in place.
