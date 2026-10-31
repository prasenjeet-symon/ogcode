-- +goose Up
-- What the user told us about a custom model no catalogue knows: the context
-- window it accepts, its output ceiling, what its tokens cost per million and
-- whether it takes image input. 0 / false means "not configured", so a
-- catalogue or provider fact still answers for that field on its own.
ALTER TABLE model_preference ADD COLUMN context_window INTEGER NOT NULL DEFAULT 0;
ALTER TABLE model_preference ADD COLUMN max_output_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE model_preference ADD COLUMN input_price_per_m REAL NOT NULL DEFAULT 0;
ALTER TABLE model_preference ADD COLUMN output_price_per_m REAL NOT NULL DEFAULT 0;
ALTER TABLE model_preference ADD COLUMN supports_images INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE model_preference DROP COLUMN supports_images;
ALTER TABLE model_preference DROP COLUMN output_price_per_m;
ALTER TABLE model_preference DROP COLUMN input_price_per_m;
ALTER TABLE model_preference DROP COLUMN max_output_tokens;
ALTER TABLE model_preference DROP COLUMN context_window;
