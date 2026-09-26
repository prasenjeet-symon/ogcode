-- +goose Up
-- The model catalogue a provider last reported is persisted here so a fresh
-- process can render the picker from the last known list instead of blocking on
-- a network fetch. Fetches move off the read path entirely (Models() becomes a
-- pure cache read), so without this every process start — and every credential
-- change that rebuilds the registry — would show an empty or static picker until
-- a background refresh landed.
--
-- Keyed by (provider_id, model_id): one row per model a provider serves, and a
-- row is replaced wholesale when that provider's catalogue is refreshed, so a
-- model the endpoint stopped listing disappears rather than lingering.
--
-- Lives in the global config DB beside the provider credentials it describes:
-- the catalogue follows the account, not the project.
CREATE TABLE IF NOT EXISTS model_catalog (
    provider_id         TEXT NOT NULL,
    model_id            TEXT NOT NULL,
    name                TEXT NOT NULL DEFAULT '',
    active_by_default   INTEGER NOT NULL DEFAULT 0,
    supports_images     INTEGER NOT NULL DEFAULT 0,
    context_window      INTEGER NOT NULL DEFAULT 0,
    max_output_tokens   INTEGER NOT NULL DEFAULT 0,
    collection          TEXT NOT NULL DEFAULT '',
    input_price_per_m   REAL NOT NULL DEFAULT 0,
    output_price_per_m  REAL NOT NULL DEFAULT 0,
    fetched_at          INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (provider_id, model_id)
);

-- +goose Down
DROP TABLE IF EXISTS model_catalog;
