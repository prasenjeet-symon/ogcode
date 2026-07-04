-- +goose Up
CREATE TABLE IF NOT EXISTS posthog_config (
    id           INTEGER PRIMARY KEY CHECK (id = 1),
    enabled      INTEGER NOT NULL DEFAULT 0,
    api_key      TEXT NOT NULL DEFAULT '',
    api_host     TEXT NOT NULL DEFAULT 'https://app.posthog.com',
    time_updated INTEGER NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS posthog_config;