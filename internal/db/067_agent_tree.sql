-- +goose Up
-- The board is a tree now: rows in board_agent are the whole of it. The post
-- log (and with it claims, topics, cursors and delivery budgets) is gone —
-- agents coordinate by mutating files, and a child reports to its parent by
-- finishing, not by posting.
--
-- notified_at is the one piece of the old log that had to survive: it is when
-- the spawner was last told its child is done. A terminal row whose updated_at
-- is newer than notified_at is news the parent has not heard yet, so the next
-- turn start delivers it.
DROP TABLE board_post;
ALTER TABLE board_agent DROP COLUMN last_seq;
ALTER TABLE board_agent ADD COLUMN notified_at INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE board_agent DROP COLUMN notified_at;
ALTER TABLE board_agent ADD COLUMN last_seq INTEGER NOT NULL DEFAULT 0;

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
