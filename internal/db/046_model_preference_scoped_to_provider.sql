-- +goose Up
-- Model preferences are keyed by model id alone, but the same model id can be
-- served by two providers — glm-5.3-flash exists under both the OGX plan and a
-- custom OpenAI-compatible endpoint. A row written for one provider then
-- overrode the other's default on read, and INSERT OR REPLACE made a toggle on
-- one destroy the other's row. Re-key on (id, provider_id) so each provider's
-- toggle is its own.
--
-- Legacy rows migrate to (id, stored provider_id). Rows whose provider has
-- since unregistered keep working: the read treats a stored row as an
-- explicit choice, and the settings screen only lists rows for available
-- providers, so an orphaned one stays invisible and harmless.
CREATE TABLE IF NOT EXISTS model_preference_new (
    id           TEXT NOT NULL,
    enabled      INTEGER NOT NULL DEFAULT 1,
    provider_id  TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    is_custom    INTEGER NOT NULL DEFAULT 0,
    collection   TEXT NOT NULL DEFAULT '',
    time_created INTEGER NOT NULL,
    time_updated INTEGER NOT NULL,
    PRIMARY KEY (id, provider_id)
);

INSERT INTO model_preference_new (id, enabled, provider_id, display_name, is_custom, collection, time_created, time_updated)
    SELECT id, enabled, provider_id, display_name, is_custom, collection, time_created, time_updated
    FROM model_preference;

DROP TABLE model_preference;
ALTER TABLE model_preference_new RENAME TO model_preference;

-- +goose Down
-- The old single-column key loses every row that shared an id across
-- providers — the collision the re-key exists to end. Keep the last row per
-- id, mirroring what the old table was able to hold.
CREATE TABLE IF NOT EXISTS model_preference_old (
    id           TEXT PRIMARY KEY,
    enabled      INTEGER NOT NULL DEFAULT 1,
    provider_id  TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    is_custom    INTEGER NOT NULL DEFAULT 0,
    collection   TEXT NOT NULL DEFAULT '',
    time_created INTEGER NOT NULL,
    time_updated INTEGER NOT NULL
);

INSERT OR REPLACE INTO model_preference_old (id, enabled, provider_id, display_name, is_custom, collection, time_created, time_updated)
    SELECT id, enabled, provider_id, display_name, is_custom, collection, time_created, time_updated
    FROM model_preference
    ORDER BY time_updated ASC;

DROP TABLE model_preference;
ALTER TABLE model_preference_old RENAME TO model_preference;