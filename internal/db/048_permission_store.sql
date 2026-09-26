-- +goose Up
-- Permission decisions that outlive the session they were made in. Both tables
-- live in the GLOBAL config DB (~/.ogcode/config.db) next to provider_config
-- and ogx_account: an approval the user gave once, or a mode they chose once,
-- is their preference and follows them across every project on this machine.
--
-- permission_grant records an "always allow" as the EXACT target the user
-- approved — the command for bash, the path for write/edit, the skill name for
-- skill — rather than the whole tool. Approving one command must not silently
-- approve every future one, and (tool, pattern) is therefore the key: the same
-- target approved twice is one row, not two.
CREATE TABLE IF NOT EXISTS permission_grant (
    tool         TEXT NOT NULL,
    pattern      TEXT NOT NULL,
    action       TEXT NOT NULL,
    time_created INTEGER NOT NULL,
    PRIMARY KEY (tool, pattern)
);

-- permission_mode is the Ask/Auto choice new sessions start in. Toggling the
-- mode in any session records it here (last choice wins), so a user who prefers
-- Auto does not re-select it on every new session. Singleton: one row, id = 1.
-- The column default matches the behaviour from before the choice was stored:
-- a row-less database reads as "ask".
CREATE TABLE IF NOT EXISTS permission_mode (
    id           INTEGER PRIMARY KEY CHECK (id = 1),
    mode         TEXT NOT NULL DEFAULT 'ask',
    time_updated INTEGER NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS permission_mode;
DROP TABLE IF EXISTS permission_grant;
