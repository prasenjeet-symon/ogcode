-- +goose Up
-- Machine-wide UI preferences live in the GLOBAL config DB rather than the
-- per-project one: the light/dark choice applies to the whole install, not to
-- one workspace. A singleton row (id = 1) keeps the shape simple.
CREATE TABLE IF NOT EXISTS ui_prefs (
    id           INTEGER PRIMARY KEY CHECK (id = 1),
    theme_mode   TEXT NOT NULL DEFAULT 'system',
    time_updated INTEGER NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS ui_prefs;
