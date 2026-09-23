-- +goose Up
-- The link between this ogcode install and an OG Lab account holding an OGX
-- plan. Subscription, payment and plan state all live on the web side; what
-- ogcode keeps is the token that proves the connection, plus the display
-- fields the settings screen shows. Lives in the GLOBAL config DB
-- (~/.ogcode/config.db) next to provider_config: the plan belongs to the
-- user, not to any one project. Singleton: one row, id = 1 — one account per
-- install, connecting again replaces it.
CREATE TABLE IF NOT EXISTS ogx_account (
    id             INTEGER PRIMARY KEY CHECK (id = 1),
    token          TEXT NOT NULL,
    email          TEXT NOT NULL DEFAULT '',
    plan           TEXT NOT NULL DEFAULT '',
    time_connected INTEGER NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS ogx_account;
