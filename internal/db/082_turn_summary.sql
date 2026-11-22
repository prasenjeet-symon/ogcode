-- +goose Up
-- Persistent per-turn summaries: one turn-local summary per agent turn, stored
-- so it survives restart. The next turn injects the last 2 into its system
-- prompt as a continuity hint ("Turn N: <summary>", oldest first).
--
-- GENERATED at turn end by a detached one-shot call (timeout + head/tail
-- truncation fallback) that runs after the turn closes, never re-summarized
-- at next-turn start. Rows are deleted with their session (foreign key
-- cascade), matching message/part.
CREATE TABLE IF NOT EXISTS turn_summary (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL REFERENCES session(id) ON DELETE CASCADE,
    turn_index  INTEGER NOT NULL,
    summary     TEXT NOT NULL,
    user_text   TEXT NOT NULL DEFAULT '',
    time_created INTEGER NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_turn_summary_session
    ON turn_summary(session_id, turn_index);

-- +goose Down
DROP TABLE IF EXISTS turn_summary;
