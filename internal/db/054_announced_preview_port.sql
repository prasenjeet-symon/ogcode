-- +goose Up
-- A port an agent handed the user a live-preview URL for, recorded the moment
-- the turn that announced it ends. The Preview page lists exactly these ports:
-- mining the transcript for a URL is not enough, because a tool call's output
-- (a build log, a dependency that printed it binds) is full of ports that are
-- not this project's services. Recording at announce time, from the agent's own
-- prose alone, keeps the page to what it was actually told.
--
-- Keyed by (directory, port): a port belongs to the project whose agent
-- announced it, so one project's previews never leak into another's grid, and
-- re-announcing a port is idempotent so a long-lived session records it once.
--
-- Lives in the project DB beside the sessions it came from: unlike the model
-- catalogue, which follows the account, an announced service is per-project.
CREATE TABLE IF NOT EXISTS announced_preview_port (
    directory    TEXT NOT NULL,
    port         INTEGER NOT NULL,
    time_created INTEGER NOT NULL,
    PRIMARY KEY (directory, port)
);

-- +goose Down
DROP TABLE IF EXISTS announced_preview_port;
