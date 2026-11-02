-- +goose Up
-- Two-level web cache: an in-process LRU (internal/webcache) in front of this
-- table, so a research question asked once is cheap to ask again — including
-- after a restart. One row per (kind, key):
--
--   'search'  a provider's ranked result list for one query
--   'page'    the extracted text of one URL
--   'answer'  a synthesised deep-research answer
--
-- key is a sha256 digest of the canonical inputs (see webcache.Key); the raw
-- query and URL are never stored, so the table cannot leak what was searched.
-- expires_at is absolute, in unix millis, and is set once at write time: a row
-- promoted back into memory keeps its original expiry rather than renewing it.
CREATE TABLE IF NOT EXISTS web_cache (
    kind       TEXT    NOT NULL,
    key        TEXT    NOT NULL,
    body       BLOB    NOT NULL,
    bytes      INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    PRIMARY KEY (kind, key)
);
-- The sweep deletes by expiry and the budget trim deletes oldest-first.
CREATE INDEX IF NOT EXISTS idx_web_cache_expires ON web_cache (expires_at);

-- The cache's own settings ride the existing singleton search_config row, the
-- same way the native engine's knobs do. TTLs are stored in minutes; 0 means
-- "unset" and resolves to the default in SearchConfig.normalise.
ALTER TABLE search_config ADD COLUMN cache_enabled            INTEGER NOT NULL DEFAULT 1;
ALTER TABLE search_config ADD COLUMN cache_search_ttl_minutes INTEGER NOT NULL DEFAULT 60;
ALTER TABLE search_config ADD COLUMN cache_page_ttl_minutes   INTEGER NOT NULL DEFAULT 1440;
ALTER TABLE search_config ADD COLUMN cache_answer_ttl_minutes INTEGER NOT NULL DEFAULT 360;

-- +goose Down
DROP INDEX IF EXISTS idx_web_cache_expires;
DROP TABLE IF EXISTS web_cache;
-- The search_config columns are left in place: SQLite cannot drop a column
-- before 3.35, and an older binary simply ignores them.
