-- +goose Up
-- The mutation history: one row per write/edit that landed, kept forever so a
-- clobber can still be reviewed after the session that caused it is gone. The
-- row outlives its author, so session_id carries no foreign key — a session
-- delete must never fail an insert for a change that already happened.
CREATE TABLE file_change (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  directory    TEXT NOT NULL,
  path         TEXT NOT NULL,
  rev          INTEGER NOT NULL,
  session_id   TEXT NOT NULL DEFAULT '',
  agent        TEXT NOT NULL DEFAULT '',
  tool         TEXT NOT NULL,
  note         TEXT NOT NULL DEFAULT '',
  diff         TEXT NOT NULL DEFAULT '',
  diff_omitted INTEGER NOT NULL DEFAULT 0,
  created_at   INTEGER NOT NULL
);

CREATE INDEX file_change_path ON file_change(directory, path, rev);
CREATE INDEX file_change_time ON file_change(directory, created_at);

-- What each session last read of each file. This is the half of the gate that
-- must survive a restart, so it is a table rather than a map: one row per
-- (directory, file, session), upserted when the session observes a new
-- revision. No foreign key on session_id, for the same reason as file_change;
-- a bookmark whose session no longer exists is swept at startup instead.
CREATE TABLE file_bookmark (
  directory  TEXT NOT NULL,
  path       TEXT NOT NULL,
  session_id TEXT NOT NULL,
  rev        INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (directory, path, session_id)
);

CREATE INDEX file_bookmark_session ON file_bookmark(directory, session_id);

-- +goose Down
DROP INDEX file_bookmark_session;
DROP TABLE file_bookmark;
DROP INDEX file_change_time;
DROP INDEX file_change_path;
DROP TABLE file_change;
