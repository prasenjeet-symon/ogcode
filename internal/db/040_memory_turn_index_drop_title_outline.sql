-- +goose Up
-- The turn index no longer stores a heading outline (memory_map renders from
-- filenames + topic labels and reads an outline on demand via file_map) nor a
-- separate title column (the filename slug carries the title). Both were
-- written but never read; drop them.
ALTER TABLE memory_turn_index DROP COLUMN title;
ALTER TABLE memory_turn_index DROP COLUMN outline;

-- +goose Down
ALTER TABLE memory_turn_index ADD COLUMN title TEXT NOT NULL DEFAULT '';
ALTER TABLE memory_turn_index ADD COLUMN outline TEXT NOT NULL DEFAULT '';