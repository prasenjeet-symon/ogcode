-- +goose Up
-- compact_context is now offered to every read-capable agent on every provider;
-- it no longer depends on whether an endpoint caches a repeated prefix. The
-- observer that resolved and persisted that verdict has been removed, so nothing
-- reads this table any more. Drop it.
DROP TABLE IF EXISTS model_cache_support;

-- +goose Down
CREATE TABLE IF NOT EXISTS model_cache_support (
    model_id    TEXT NOT NULL,
    endpoint    TEXT NOT NULL,
    verdict     TEXT NOT NULL,
    observed_at INTEGER NOT NULL,
    PRIMARY KEY (model_id, endpoint)
);
