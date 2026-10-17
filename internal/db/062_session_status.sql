-- +goose Up
-- The in-flight turn state of a session, shown as a dot in the session list:
-- "running" while a turn is in progress, "failed" when the last turn ended in
-- an error, and "" (the default) when no turn is running — idle, or the last
-- turn finished cleanly. Server-tracked so every row is right on a fresh load,
-- not only in the tab that ran the turn.
ALTER TABLE session ADD COLUMN status TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE session DROP COLUMN status;
