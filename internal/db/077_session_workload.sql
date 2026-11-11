-- +goose Up
-- The spawning agent's prediction of what this session's task mostly does
-- ("read-heavy" or "write-heavy"; see internal/provider/workload.go), stated
-- when it handed the work over. Every request the session makes carries it as
-- the X-Client-Workload header, so a router can serve a read-heavy child from a
-- cheaper model. It sits beside complexity because it is read the same way: once
-- at the start of each turn, so a resumed or restarted turn sends the class the
-- spawner declared rather than none at all. Empty means nobody delegated this
-- session and no workload was predicted.
ALTER TABLE session ADD COLUMN workload TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE session DROP COLUMN workload;
