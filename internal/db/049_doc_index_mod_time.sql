-- +goose Up
-- The project index used to be keyed on path alone, so a file that changed on
-- disk after it was indexed was skipped by every later run — only new and
-- deleted files were noticed. Recording the file's modification time with its
-- rows lets an incremental run tell a rewritten file from an untouched one and
-- re-index just the former. Rows written before this column existed default to
-- 0, which reads as "stale" and so refreshes each file once on the next run.
ALTER TABLE doc_page_index ADD COLUMN mod_time INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE doc_page_index DROP COLUMN mod_time;
