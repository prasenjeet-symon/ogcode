-- +goose Up
-- The spawning agent's prediction of how hard this session's task is ("easy",
-- "medium", "hard"; see internal/provider/complexity.go), stated when it handed
-- the work over. Every request the session makes carries it as the
-- X-Client-Complexity header, so a router can serve an easy child from a
-- cheaper model. It sits beside effort because it is read the same way: once at
-- the start of each turn, so a resumed or restarted turn sends the class the
-- spawner declared rather than none at all. Empty means nobody delegated this
-- session and no class was predicted.
ALTER TABLE session ADD COLUMN complexity TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE session DROP COLUMN complexity;
