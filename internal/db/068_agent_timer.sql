-- +goose Up
-- One-shot reminders an agent schedules for itself. When fire_at passes the
-- server claims the row (pending → due) and delivers the note back into that
-- session's loop as a system notice, then marks it delivered. The row is
-- keyed by id, not by session+time, so a session's reminders survive a restart.
--
-- Timestamps are unix seconds, the convention every board and file-change
-- table uses. There is no foreign key on session_id: a reminder outlives the
-- session that scheduled it, and a session delete must never fail on one.

CREATE TABLE agent_timer (
  id         TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  note       TEXT NOT NULL,
  fire_at    INTEGER NOT NULL,
  status     TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE INDEX agent_timer_session ON agent_timer(session_id, status);
CREATE INDEX agent_timer_claim   ON agent_timer(status, fire_at);

-- +goose Down
DROP INDEX agent_timer_claim;
DROP INDEX agent_timer_session;
DROP TABLE agent_timer;