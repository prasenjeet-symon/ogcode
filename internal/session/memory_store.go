package session

import (
	"database/sql"
	"fmt"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

// MemoryConfig holds the user's agentic-memory configuration stored in the DB.
//
// Embedding is always produced by the inbuilt local embedder
// (gte-small) — there is no embedder configuration. The synthesis LLM
// is not configured here either: it uses the session's selected model at call
// time. So the only persisted knob is whether agentic memory is enabled.
//
// The underlying memory_config table still carries legacy embed/chat columns
// from earlier releases; they are no longer read or written by this struct but
// are retained in the schema for migration safety.
type MemoryConfig struct {
	Enabled   bool  `json:"enabled"`
	UpdatedAt int64 `json:"updatedAt"`
}

// GetMemoryConfig returns the stored agentic-memory config. If the row does not
// exist, agentic memory defaults to enabled: the inbuilt local embedder
// (gte-small) needs zero setup — no API key, no external service — so
// memory is on out of the box. A user who has explicitly turned memory off has
// a persisted row with enabled = 0, which is still honored.
func GetMemoryConfig(database *db.DB) (*MemoryConfig, error) {
	var c MemoryConfig
	var enabled int
	var updated int64
	err := database.QueryRow(`
			SELECT enabled, time_updated
			FROM memory_config WHERE id = 1
		`).Scan(&enabled, &updated)
	if err == sql.ErrNoRows {
		return &MemoryConfig{Enabled: true}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get memory config: %w", err)
	}
	c.Enabled = enabled != 0
	c.UpdatedAt = updated
	return &c, nil
}

// SetMemoryConfig upserts the agentic-memory config row (id is always 1).
// Legacy embed/chat columns are reset to defaults — they are no longer used.
func SetMemoryConfig(database *db.DB, c *MemoryConfig) error {
	enabled := 0
	if c.Enabled {
		enabled = 1
	}
	_, err := database.Exec(`
			INSERT INTO memory_config (id, enabled, embed_provider_id, embed_model, embed_api_key, embed_base_url,
			                           chat_provider_id, chat_model, chat_api_key, chat_base_url, time_updated)
			VALUES (1, ?, '', '', '', '', '', '', '', '', ?)
			ON CONFLICT(id) DO UPDATE SET
				enabled = excluded.enabled,
				embed_provider_id = excluded.embed_provider_id,
				embed_model = excluded.embed_model,
				embed_api_key = excluded.embed_api_key,
				embed_base_url = excluded.embed_base_url,
				chat_provider_id = excluded.chat_provider_id,
				chat_model = excluded.chat_model,
				chat_api_key = excluded.chat_api_key,
				chat_base_url = excluded.chat_base_url,
				time_updated = excluded.time_updated
	`, enabled, Now())
	if err != nil {
		return fmt.Errorf("set memory config: %w", err)
	}
	return nil
}

// MaskedMemoryConfig returns a copy of c. With the embed/chat fields gone there
// are no secrets to mask, but the function is retained for API compatibility.
func MaskedMemoryConfig(c *MemoryConfig) *MemoryConfig {
	mc := *c
	return &mc
}