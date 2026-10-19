-- +goose Up
-- The agent's forward-looking plan for the current turn: a small JSON list of
-- the steps it means to take and where it is in them, written by the
-- update_intent tool. Shown as a pill in the session header so the user can see
-- where a turn is heading instead of inferring it from finished actions. Cleared
-- when the next turn starts, so it describes the turn in flight, not history.
ALTER TABLE session ADD COLUMN intent TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE session DROP COLUMN intent;
