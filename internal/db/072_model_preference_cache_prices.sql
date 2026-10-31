-- +goose Up
-- What the user told us about a model's cache traffic: what reading from and
-- writing to the prompt cache costs per million tokens. 0 means "not
-- configured", so a provider listing, a catalogue fact or the standard
-- multipliers of the input price still answer for that field on its own.
ALTER TABLE model_preference ADD COLUMN cache_read_price_per_m REAL NOT NULL DEFAULT 0;
ALTER TABLE model_preference ADD COLUMN cache_write_price_per_m REAL NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE model_preference DROP COLUMN cache_write_price_per_m;
ALTER TABLE model_preference DROP COLUMN cache_read_price_per_m;
