-- +goose Up
-- The per-project message board: one row per agent handle that has joined a
-- project's board, and one row per post. Posts are append-only; claims are a
-- fold over the post log (the latest claim for a path that has not expired and
-- no later release), so there is no claims table to keep consistent.
CREATE TABLE board_agent (
  id         TEXT PRIMARY KEY,
  directory  TEXT NOT NULL,
  handle     TEXT NOT NULL,
  session_id TEXT NOT NULL DEFAULT '',
  role       TEXT NOT NULL DEFAULT '',
  kind       TEXT NOT NULL DEFAULT 'session',
  spawned_by TEXT NOT NULL DEFAULT '',
  status     TEXT NOT NULL DEFAULT 'idle',
  last_seq   INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE UNIQUE INDEX board_agent_handle  ON board_agent(directory, handle);
CREATE UNIQUE INDEX board_agent_session ON board_agent(session_id) WHERE session_id <> '';

CREATE TABLE board_post (
  seq         INTEGER PRIMARY KEY AUTOINCREMENT,
  id          TEXT NOT NULL UNIQUE,
  directory   TEXT NOT NULL,
  topic       TEXT NOT NULL DEFAULT 'general',
  from_handle TEXT NOT NULL,
  to_handle   TEXT NOT NULL DEFAULT '',
  kind        TEXT NOT NULL DEFAULT 'note',
  text        TEXT NOT NULL DEFAULT '',
  path        TEXT NOT NULL DEFAULT '',
  lease_until INTEGER NOT NULL DEFAULT 0,
  urgent      INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL
);

CREATE INDEX board_post_delivery ON board_post(directory, seq);
CREATE INDEX board_post_topic    ON board_post(directory, topic, seq);
CREATE INDEX board_post_claims   ON board_post(directory, kind, path);

-- +goose Down
DROP INDEX board_post_claims;
DROP INDEX board_post_topic;
DROP INDEX board_post_delivery;
DROP TABLE board_post;
DROP INDEX board_agent_session;
DROP INDEX board_agent_handle;
DROP TABLE board_agent;
