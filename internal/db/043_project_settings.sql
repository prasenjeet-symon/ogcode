-- +goose Up
-- General settings that belong to one project rather than to the user as a
-- whole. This database is itself per project (<project>/.ogcode/ogcode.db), so
-- the table is a singleton: one row, id = 1.
--
-- Every column carries the default that matches the behaviour from before the
-- setting existed, so a project with no row behaves exactly as it did.
CREATE TABLE IF NOT EXISTS project_settings (
    id                      INTEGER PRIMARY KEY CHECK (id = 1),
    compact_context_enabled INTEGER NOT NULL DEFAULT 1,
    time_updated            INTEGER NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS project_settings;
