package session

import (
	"database/sql"
	"fmt"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

// Search providers. Native is the built-in engine compiled into the binary;
// Tavily is a third-party API keyed by the user's own token. The value is
// stored as a string so future providers slot in without a schema change.
const (
	SearchProviderNative = "native"
	SearchProviderTavily = "tavily"
)

// MaskedAPIKey is the sentinel the UI receives in place of a stored secret, and
// sends back unchanged to mean "keep the key you already have". Shared with the
// provider-config masking so both credential surfaces behave identically.
const MaskedAPIKey = "__SET__"

// SearchConfig holds the global web-search toggle, the active search provider
// and its credential.
type SearchConfig struct {
	Enabled bool `json:"enabled"`
	// Provider selects the search backend: "native" (default) or "tavily".
	Provider string `json:"provider"`
	// TavilyAPIKey is the token for the Tavily provider. Masked to MaskedAPIKey
	// on read so it never reaches the UI in the clear.
	TavilyAPIKey string `json:"tavilyApiKey"`
	UpdatedAt    int64  `json:"updatedAt"`
}

// normaliseProvider pins the provider to a known value. Applied on both read and
// write so consumers always see a usable value regardless of how the row was
// populated.
func (c *SearchConfig) normaliseProvider() {
	if c.Provider != SearchProviderTavily {
		c.Provider = SearchProviderNative
	}
}

// GetSearchConfig returns the stored config. If no row exists it returns the
// defaults, which have search ENABLED: the backend is compiled into the binary
// and needs nothing installed, so there is no setup step to gate it behind.
// A user who does not want outbound requests turns the toggle off, and that
// stored choice is honoured on every later read.
func GetSearchConfig(database *db.DB) (*SearchConfig, error) {
	var enabled int
	var provider, tavilyKey string
	var updatedAt int64
	err := database.QueryRow(
		`SELECT enabled, provider, tavily_api_key, time_updated FROM search_config WHERE id = 1`,
	).Scan(&enabled, &provider, &tavilyKey, &updatedAt)
	if err == sql.ErrNoRows {
		def := &SearchConfig{Enabled: true, Provider: SearchProviderNative}
		def.normaliseProvider()
		return def, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get search config: %w", err)
	}
	cfg := &SearchConfig{
		Enabled:      enabled != 0,
		Provider:     provider,
		TavilyAPIKey: tavilyKey,
		UpdatedAt:    updatedAt,
	}
	cfg.normaliseProvider()
	return cfg, nil
}

// SetSearchConfig upserts the singleton config row, normalising the provider
// before persisting so an invalid client payload can never store a bad value.
func SetSearchConfig(database *db.DB, c *SearchConfig) error {
	c.normaliseProvider()
	enabled := 0
	if c.Enabled {
		enabled = 1
	}
	// The legacy use_real_profile column is left in place rather than dropped:
	// it is NOT NULL DEFAULT 0, so omitting it here is safe, and keeping it
	// means an older binary can still read this database.
	_, err := database.Exec(`
		INSERT INTO search_config (id, enabled, provider, tavily_api_key, time_updated)
		VALUES (1, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			enabled        = excluded.enabled,
			provider       = excluded.provider,
			tavily_api_key = excluded.tavily_api_key,
			time_updated   = excluded.time_updated
	`, enabled, c.Provider, c.TavilyAPIKey, Now())
	if err != nil {
		return fmt.Errorf("set search config: %w", err)
	}
	return nil
}

// MaskedSearchConfig returns a copy with the Tavily key replaced by the mask
// sentinel so the config can be sent to the UI without leaking the real value.
// Mirrors MaskedProviderConfig.
func MaskedSearchConfig(c *SearchConfig) *SearchConfig {
	mc := *c
	if mc.TavilyAPIKey != "" {
		mc.TavilyAPIKey = MaskedAPIKey
	}
	return &mc
}
