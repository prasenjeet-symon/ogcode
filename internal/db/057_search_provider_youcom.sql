-- +goose Up
-- You.com search provider selection. Adds a slot for the You.com API key so
-- 'youcom' can be stored as the search provider, mirroring the Tavily columns
-- from 035. The key lives in this row for the same reason the Tavily one does:
-- it configures search, not an LLM.
ALTER TABLE search_config ADD COLUMN youcom_api_key TEXT NOT NULL DEFAULT '';

-- +goose Down
-- SQLite does not support DROP COLUMN on older versions; leave the column in place.
