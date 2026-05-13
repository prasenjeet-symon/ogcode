-- +goose Up
ALTER TABLE plan ADD COLUMN archived_at INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE plan DROP COLUMN archived_at;
