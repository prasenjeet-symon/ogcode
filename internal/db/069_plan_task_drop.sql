-- +goose Up
-- Remove the plan-mode feature: the plan and task tables are no longer written
-- by any code (plan mode, task breakdown and the task-execute worktree flow are
-- gone). DROP TABLE also drops the columns later migrations added to these
-- tables and their indexes, so this is the single statement each direction
-- needs. The task table is dropped first: it foreign-keys plan.

DROP TABLE IF EXISTS task;
DROP TABLE IF EXISTS plan;

-- +goose Down
-- Recreate the exact pre-069 shape (end of 068), so a down-replay past this
-- point stays consistent: every migration below that altered these tables
-- drops its own column in reverse order (047 provider, 030 task.model,
-- 029 plan.base_branch, 016 plan.archived_at, 015 task.chain_branch, 009
-- task.worktree_path; 008/010/011 record their Down as no-ops). Columns left
-- behind by a no-op Down are dropped with the tables by 006's Down. This keeps
-- the schema matched to goose_db_version at every step of the replay.
CREATE TABLE IF NOT EXISTS plan (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES session(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL,
    directory TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'open',
    model TEXT NOT NULL DEFAULT '',
    compaction_summary TEXT NOT NULL DEFAULT '',
    time_created INTEGER NOT NULL,
    time_updated INTEGER NOT NULL,
    breakdown_status TEXT NOT NULL DEFAULT '',
    breakdown_warnings TEXT NOT NULL DEFAULT '',
    archived_at INTEGER NOT NULL DEFAULT 0,
    base_branch TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_plan_session ON plan(session_id);
CREATE INDEX IF NOT EXISTS idx_plan_project ON plan(project_id);
CREATE INDEX IF NOT EXISTS idx_plan_status ON plan(status);

CREATE TABLE IF NOT EXISTS task (
    id TEXT PRIMARY KEY,
    plan_id TEXT NOT NULL REFERENCES plan(id) ON DELETE CASCADE,
    session_id TEXT REFERENCES session(id) ON DELETE SET NULL,
    parent_task_id TEXT REFERENCES task(id) ON DELETE SET NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    effort TEXT NOT NULL DEFAULT 'M',
    complexity TEXT NOT NULL DEFAULT 'medium',
    status TEXT NOT NULL DEFAULT 'pending',
    dependencies TEXT NOT NULL DEFAULT '[]',
    branch_name TEXT NOT NULL DEFAULT '',
    pr_url TEXT NOT NULL DEFAULT '',
    pr_number INTEGER,
    order_index INTEGER NOT NULL DEFAULT 0,
    time_created INTEGER NOT NULL,
    time_updated INTEGER NOT NULL,
    worktree_path TEXT NOT NULL DEFAULT '',
    pr_error TEXT NOT NULL DEFAULT '',
    chain_branch TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_task_plan ON task(plan_id);
CREATE INDEX IF NOT EXISTS idx_task_status ON task(status);
CREATE INDEX IF NOT EXISTS idx_task_session ON task(session_id);