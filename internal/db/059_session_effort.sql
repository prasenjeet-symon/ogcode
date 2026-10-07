-- +goose Up
-- The reasoning effort the session's agent loop asks its model for ("low",
-- "high", "max", "none", ...; see internal/provider/effort.go). Empty means
-- none was chosen and the model runs at its vendor's default. It sits beside
-- model and provider because it is part of the same choice: each turn reads all
-- three at start, so a resumed or restarted turn runs at the effort the user
-- picked rather than silently falling back to the default.
ALTER TABLE session ADD COLUMN effort TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE session DROP COLUMN effort;
