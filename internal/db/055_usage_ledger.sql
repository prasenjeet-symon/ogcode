-- +goose Up
-- A ledger of model spend, kept in the global config DB so one view can total
-- usage across every project and every model. One row per agent step, keyed
-- by its assistant message id so a step recorded twice overwrites rather than
-- doubles; one row per utility call (title, risk check, compaction, turn-memory
-- summary, deep-search ranking and synthesis).
--
-- Rows outlive the sessions and messages they came from: tokens a deleted
-- session or an orphaned step spent were still spent. Sub-agent steps are
-- recorded under the sub-agent's own session as they run; folding that usage
-- into the parent's utility totals later writes no row, so nothing is counted
-- twice. Prices are not stored — a row is priced when it is read.
--
-- Every DB runs every migration, so the tables exist in project DBs too; only
-- the global one is written.
CREATE TABLE IF NOT EXISTS usage_event (
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL,
    project      TEXT NOT NULL DEFAULT '',
    session_id   TEXT NOT NULL DEFAULT '',
    provider     TEXT NOT NULL DEFAULT '',
    model        TEXT NOT NULL DEFAULT '',
    input        INTEGER NOT NULL DEFAULT 0,
    output       INTEGER NOT NULL DEFAULT 0,
    reasoning    INTEGER NOT NULL DEFAULT 0,
    cache_read   INTEGER NOT NULL DEFAULT 0,
    cache_write  INTEGER NOT NULL DEFAULT 0,
    time_created INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_usage_event_time ON usage_event(time_created);

-- Projects whose history from before the ledger has been copied in, so each
-- project's copy runs once rather than on every start.
CREATE TABLE IF NOT EXISTS usage_backfill (
    project   TEXT PRIMARY KEY,
    time_done INTEGER NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS usage_backfill;
DROP TABLE IF EXISTS usage_event;
