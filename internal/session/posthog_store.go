package session

import (
	"database/sql"
	"fmt"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

// DefaultPostHogHost is the cloud-hosted PostHog endpoint.
const DefaultPostHogHost = "https://app.posthog.com"

// PostHogConfig holds the global configuration for PostHog product analytics.
// The config is stored as a singleton row (id = 1) in the shared global config DB.
type PostHogConfig struct {
	Enabled   bool   `json:"enabled"`
	APIKey    string `json:"apiKey"`
	APIHost   string `json:"apiHost"`
	UpdatedAt int64  `json:"updatedAt"`
}

// GetPostHogConfig returns the stored PostHog config. If no row exists, returns
// a disabled default.
func GetPostHogConfig(database *db.DB) (*PostHogConfig, error) {
	var enabled int
	var apiKey, apiHost string
	var updatedAt int64
	err := database.QueryRow(
		`SELECT enabled, api_key, api_host, time_updated FROM posthog_config WHERE id = 1`,
	).Scan(&enabled, &apiKey, &apiHost, &updatedAt)
	if err == sql.ErrNoRows {
		return &PostHogConfig{APIHost: DefaultPostHogHost}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get posthog config: %w", err)
	}
	return &PostHogConfig{
		Enabled:   enabled != 0,
		APIKey:    apiKey,
		APIHost:   apiHost,
		UpdatedAt: updatedAt,
	}, nil
}

// SetPostHogConfig upserts the singleton config row.
func SetPostHogConfig(database *db.DB, c *PostHogConfig) error {
	enabled := 0
	if c.Enabled {
		enabled = 1
	}
	host := c.APIHost
	if host == "" {
		host = DefaultPostHogHost
	}
	_, err := database.Exec(`
		INSERT INTO posthog_config (id, enabled, api_key, api_host, time_updated) VALUES (1, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			enabled      = excluded.enabled,
			api_key      = excluded.api_key,
			api_host     = excluded.api_host,
			time_updated = excluded.time_updated
	`, enabled, c.APIKey, host, Now())
	if err != nil {
		return fmt.Errorf("set posthog config: %w", err)
	}
	return nil
}

// MaskedPostHogConfig returns a copy of c with the API key masked for display.
// It replaces the key with a short prefix + "…" when set, or "" when empty.
func MaskedPostHogConfig(c *PostHogConfig) *PostHogConfig {
	mc := *c
	if mc.APIKey != "" {
		if len(mc.APIKey) > 10 {
			mc.APIKey = mc.APIKey[:8] + "…"
		} else {
			mc.APIKey = "••••"
		}
	}
	return &mc
}