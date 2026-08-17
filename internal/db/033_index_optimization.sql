-- +goose Up
-- Index review. Two hot list queries were doing full table scans, the message
-- loader sorted in a temp b-tree every turn, and several indexes were never read
-- (verified against every query site: nothing filters by project_id,
-- part.session_id, or model_preference.provider_id; the doc_page/index_excludes
-- directory indexes just duplicate their UNIQUE constraints). This aligns each
-- index with the columns queries actually filter and sort on, and drops the rest
-- so write-heavy paths (parts stream in on every token) carry less index upkeep.

-- session: list query is `WHERE directory=? ... ORDER BY time_updated DESC` — was a full SCAN.
DROP INDEX IF EXISTS idx_session_project;               -- project_id is never a filter
CREATE INDEX IF NOT EXISTS idx_session_directory ON session(directory, time_updated);

-- plan: list + archived queries filter `directory` (± status, archived_at) and sort time_updated — was a full SCAN.
DROP INDEX IF EXISTS idx_plan_project;                  -- project_id is never a filter
DROP INDEX IF EXISTS idx_plan_status;                   -- no status-only query; idx_plan_directory serves the archived query better
CREATE INDEX IF NOT EXISTS idx_plan_directory ON plan(directory, time_updated);

-- message: `WHERE session_id=? [AND id<?] ORDER BY id` — (session_id,id) is covering and drops the temp b-tree.
DROP INDEX IF EXISTS idx_message_session;               -- was (session_id, time_created); time_created is never sorted/filtered
CREATE INDEX IF NOT EXISTS idx_message_session ON message(session_id, id);

-- note: `WHERE directory=? ORDER BY time_updated DESC` — add the sort column to drop the temp b-tree.
DROP INDEX IF EXISTS idx_note_directory;
CREATE INDEX IF NOT EXISTS idx_note_directory ON note(directory, time_updated);

-- task: `WHERE plan_id=? ORDER BY order_index` — plan_id stays leftmost (FK cascade still served), sort now indexed.
DROP INDEX IF EXISTS idx_task_plan;
CREATE INDEX IF NOT EXISTS idx_task_plan ON task(plan_id, order_index);

-- Drop indexes no query reads (pure write overhead / duplicates of UNIQUE constraints).
DROP INDEX IF EXISTS idx_part_session;                  -- part is only read by message_id/id; no FK on session_id
DROP INDEX IF EXISTS idx_doc_page_index_path;           -- duplicates UNIQUE(doc_path, page_num)
DROP INDEX IF EXISTS idx_index_excludes_dir;            -- duplicates UNIQUE(directory, pattern)
DROP INDEX IF EXISTS idx_model_preference_provider;     -- provider_id is never a filter (list is ordered by time_created)

-- +goose Down
DROP INDEX IF EXISTS idx_session_directory;
CREATE INDEX IF NOT EXISTS idx_session_project ON session(project_id);
DROP INDEX IF EXISTS idx_plan_directory;
CREATE INDEX IF NOT EXISTS idx_plan_project ON plan(project_id);
CREATE INDEX IF NOT EXISTS idx_plan_status ON plan(status);
DROP INDEX IF EXISTS idx_message_session;
CREATE INDEX IF NOT EXISTS idx_message_session ON message(session_id, time_created);
DROP INDEX IF EXISTS idx_note_directory;
CREATE INDEX IF NOT EXISTS idx_note_directory ON note(directory);
DROP INDEX IF EXISTS idx_task_plan;
CREATE INDEX IF NOT EXISTS idx_task_plan ON task(plan_id);
CREATE INDEX IF NOT EXISTS idx_part_session ON part(session_id);
CREATE INDEX IF NOT EXISTS idx_doc_page_index_path ON doc_page_index(doc_path);
CREATE INDEX IF NOT EXISTS idx_index_excludes_dir ON index_excludes(directory);
CREATE INDEX IF NOT EXISTS idx_model_preference_provider ON model_preference(provider_id);
