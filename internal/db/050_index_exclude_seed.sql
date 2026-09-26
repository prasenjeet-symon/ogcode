-- +goose Up
-- Default excludes are seeded into index_excludes as ordinary, deletable rows.
-- This table records that a directory has been seeded, so seeding can be
-- idempotent without keying on how many exclude rows exist: a default the user
-- deleted leaves a shorter list, and a later run must not read that as "never
-- seeded" and put the pattern back.
CREATE TABLE IF NOT EXISTS index_exclude_seed (
    directory TEXT PRIMARY KEY,
    seeded_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS index_exclude_seed;
