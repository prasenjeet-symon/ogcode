-- +goose Up
-- Per-project UI preferences for the left-hand navigation panels. This database
-- is itself per project (<project>/.ogcode/ogcode.db), so the table is a
-- singleton: one row, id = 1, and each project remembers its own layout. Both
-- columns carry the default that matches the behaviour from before the setting
-- existed: expanded.
CREATE TABLE project_ui_prefs (
    id                     INTEGER PRIMARY KEY CHECK (id = 1),
    sidebar_collapsed      INTEGER NOT NULL DEFAULT 0,
    settings_nav_collapsed INTEGER NOT NULL DEFAULT 0,
    time_updated           INTEGER NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS project_ui_prefs;
