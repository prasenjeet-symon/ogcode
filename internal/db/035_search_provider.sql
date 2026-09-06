-- +goose Up
-- Third-party search provider selection. 'native' keeps the built-in engine
-- (the default, so existing installs are unaffected); 'tavily' routes web_search
-- and fetch_page through the Tavily API using the stored key. The key lives in
-- this row rather than provider_config because it configures search, not an LLM.
ALTER TABLE search_config ADD COLUMN provider       TEXT NOT NULL DEFAULT 'native';
ALTER TABLE search_config ADD COLUMN tavily_api_key TEXT NOT NULL DEFAULT '';

-- +goose Down
-- SQLite does not support DROP COLUMN on older versions; leave the columns in place.
