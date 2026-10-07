-- +goose Up
-- The native engine's deep-research knobs (ranked pages read in full,
-- characters per page) and the choice of which native engine leads (the HTTP
-- path, or Safari in front of it). These were env-only after 045 dropped them;
-- they are persisted again so the settings screen can own them, with the
-- OGCODE_SEARCH_* variables still overriding the stored values. browser is
-- empty for the built-in ordering (HTTP first, Safari as the fallback).
ALTER TABLE search_config ADD COLUMN fetch_top_k INTEGER NOT NULL DEFAULT 4;
ALTER TABLE search_config ADD COLUMN page_chars  INTEGER NOT NULL DEFAULT 6000;
ALTER TABLE search_config ADD COLUMN browser     TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE search_config DROP COLUMN fetch_top_k;
ALTER TABLE search_config DROP COLUMN page_chars;
ALTER TABLE search_config DROP COLUMN browser;
