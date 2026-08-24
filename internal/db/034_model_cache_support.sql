-- +goose Up
-- Whether an endpoint serves a repeated prompt prefix from a cache, resolved by
-- observing what the provider reports back and then persisted so the
-- observation window is spent at most once per (model, endpoint).
--
-- The key is composite, not model_id alone, because the same model can be
-- served by endpoints that differ: qwen3-coder:cloud resolves on both a local
-- Ollama (which reuses its KV cache and bills nothing) and ollama.com (which is
-- billed per token and does no prefix caching). Keying on the model alone would
-- let the verdict for one endpoint silently answer for the other.
CREATE TABLE IF NOT EXISTS model_cache_support (
    model_id    TEXT NOT NULL,
    endpoint    TEXT NOT NULL,
    verdict     TEXT NOT NULL,
    observed_at INTEGER NOT NULL,
    PRIMARY KEY (model_id, endpoint)
);

-- +goose Down
DROP TABLE IF EXISTS model_cache_support;
