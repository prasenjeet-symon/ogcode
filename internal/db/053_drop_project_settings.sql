-- +goose Up
-- Whether the agent is offered compact_context is now a process-wide switch
-- (the OGCODE_COMPACT_CONTEXT environment variable), not a per-project setting.
-- Nothing reads this table any more. Drop it.
DROP TABLE IF EXISTS project_settings;

-- +goose Down
CREATE TABLE IF NOT EXISTS project_settings (
    id                      INTEGER PRIMARY KEY CHECK (id = 1),
    compact_context_enabled INTEGER NOT NULL DEFAULT 1,
    time_updated            INTEGER NOT NULL
);
