-- +goose Up
-- The per-million USD cache-read and cache-write rates a provider publishes for
-- a model (OpenRouter/OpenAI-style `input_cache_read`, the OGX gateway's
-- `pricing.cache_read`). Kept with the rest of the fetched catalogue so cost
-- quotes stay right after a restart, not only until then. 0 where the provider
-- publishes none.
ALTER TABLE model_catalog ADD COLUMN cache_read_price_per_m  REAL NOT NULL DEFAULT 0;
ALTER TABLE model_catalog ADD COLUMN cache_write_price_per_m REAL NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE model_catalog DROP COLUMN cache_write_price_per_m;
ALTER TABLE model_catalog DROP COLUMN cache_read_price_per_m;
