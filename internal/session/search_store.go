package session

import (
	"database/sql"
	"fmt"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

// Deep-research tuning defaults and bounds. Defaults mirror the pipeline's
// built-in constants; bounds keep user-entered values sane.
const (
	DefaultSearchFetchTopK = 4
	DefaultSearchPageChars = 6000

	minSearchFetchTopK, maxSearchFetchTopK = 1, 10
	minSearchPageChars, maxSearchPageChars = 1000, 20000
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
// and its credential, plus the deep-research pipeline tuning knobs (pages
// fetched, per-page size).
type SearchConfig struct {
	Enabled bool `json:"enabled"`
	// Provider selects the search backend: "native" (default) or "tavily".
	Provider string `json:"provider"`
	// TavilyAPIKey is the token for the Tavily provider. Masked to MaskedAPIKey
	// on read so it never reaches the UI in the clear.
	TavilyAPIKey string `json:"tavilyApiKey"`
	FetchTopK    int    `json:"fetchTopK"`
	PageChars    int    `json:"pageChars"`
	UpdatedAt    int64  `json:"updatedAt"`
}

// clampParams clamps the research knobs into their valid range, substituting the
// default for any zero/out-of-range value, and normalises the provider to a known
// value. Applied on both read and write so consumers always see usable values
// regardless of how the row was populated.
func (c *SearchConfig) clampParams() {
	c.FetchTopK = clampInt(c.FetchTopK, minSearchFetchTopK, maxSearchFetchTopK, DefaultSearchFetchTopK)
	c.PageChars = clampInt(c.PageChars, minSearchPageChars, maxSearchPageChars, DefaultSearchPageChars)
	if c.Provider != SearchProviderTavily {
		c.Provider = SearchProviderNative
	}
}

func clampInt(v, lo, hi, def int) int {
	if v <= 0 {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// GetSearchConfig returns the stored config. If no row exists it returns the
// defaults, which have search ENABLED: the backend is compiled into the binary
// and needs nothing installed, so there is no setup step to gate it behind.
// A user who does not want outbound requests turns the toggle off, and that
// stored choice is honoured on every later read. Research params are always
// clamped so callers never receive zero/invalid values.
func GetSearchConfig(database *db.DB) (*SearchConfig, error) {
	var enabled, fetchTopK, pageChars int
	var provider, tavilyKey string
	var updatedAt int64
	err := database.QueryRow(
		`SELECT enabled, provider, tavily_api_key, fetch_top_k, page_chars, time_updated FROM search_config WHERE id = 1`,
	).Scan(&enabled, &provider, &tavilyKey, &fetchTopK, &pageChars, &updatedAt)
	if err == sql.ErrNoRows {
		def := &SearchConfig{Enabled: true, Provider: SearchProviderNative}
		def.clampParams()
		return def, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get search config: %w", err)
	}
	cfg := &SearchConfig{
		Enabled:      enabled != 0,
		Provider:     provider,
		TavilyAPIKey: tavilyKey,
		FetchTopK:    fetchTopK,
		PageChars:    pageChars,
		UpdatedAt:    updatedAt,
	}
	cfg.clampParams()
	return cfg, nil
}

// SetSearchConfig upserts the singleton config row, clamping the research params
// before persisting so an invalid client payload can never store bad values.
func SetSearchConfig(database *db.DB, c *SearchConfig) error {
	c.clampParams()
	enabled := 0
	if c.Enabled {
		enabled = 1
	}
	// The legacy use_real_profile column is left in place rather than dropped:
	// it is NOT NULL DEFAULT 0, so omitting it here is safe, and keeping it
	// means an older binary can still read this database.
	_, err := database.Exec(`
		INSERT INTO search_config (id, enabled, provider, tavily_api_key, fetch_top_k, page_chars, time_updated)
		VALUES (1, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			enabled        = excluded.enabled,
			provider       = excluded.provider,
			tavily_api_key = excluded.tavily_api_key,
			fetch_top_k    = excluded.fetch_top_k,
			page_chars     = excluded.page_chars,
			time_updated   = excluded.time_updated
	`, enabled, c.Provider, c.TavilyAPIKey, c.FetchTopK, c.PageChars, Now())
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
