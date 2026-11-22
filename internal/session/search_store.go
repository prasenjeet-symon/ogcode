package session

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/webcache"
)

// Search providers. Native is the built-in engine compiled into the binary;
// Tavily is a third-party API keyed by the user's own token. You.com is a
// third-party API too, but its key is read from the environment (YDC_API_KEY)
// rather than stored, so the provider column is all the session keeps for it.
// The value is stored as a string so future providers slot in without a schema
// change.
const (
	SearchProviderNative = "native"
	SearchProviderTavily = "tavily"
	SearchProviderYoucom = "youcom"
)

// Which native engine leads the chain. SearchBrowserNative uses the HTTP path
// alone; SearchBrowserSafari and SearchBrowserChrome drive a real browser first
// — Safari on macOS, Chrome or Edge on Windows, so the two are the same slot
// under the platform's own name. The empty string is the built-in ordering —
// the HTTP path in front with the browser behind it — and is also what an
// unknown value collapses to.
const (
	SearchBrowserNative = "native"
	SearchBrowserSafari = "safari"
	SearchBrowserChrome = "chrome"
)

// Bounds and defaults for the native deep-research knobs, matching the constants
// in internal/agent so a value that survives normalisation is always usable.
const (
	DefaultSearchFetchTopK = 4
	DefaultSearchPageChars = 6000

	MinSearchFetchTopK, MaxSearchFetchTopK = 1, 10
	MinSearchPageChars, MaxSearchPageChars = 1000, 20000
)

// Bounds and defaults for the web cache's per-kind lifetimes, in minutes. The
// defaults are derived from webcache's own TTL constants — an hour for search
// rankings, a day for a fetched page, six hours for a synthesised answer — so
// the settings layer and the cache cannot drift apart. A week is the ceiling —
// anything older than that is better re-researched than trusted.
const (
	DefaultCacheSearchTTL = int(webcache.DefaultSearchTTL / time.Minute)
	DefaultCachePageTTL   = int(webcache.DefaultPageTTL / time.Minute)
	DefaultCacheAnswerTTL = int(webcache.DefaultAnswerTTL / time.Minute)

	MinCacheTTLMinutes, MaxCacheTTLMinutes = 1, 7 * 24 * 60
)

// MaskedAPIKey is the sentinel the UI receives in place of a stored secret, and
// sends back unchanged to mean "keep the key you already have". Shared with the
// provider-config masking so both credential surfaces behave identically.
const MaskedAPIKey = "__SET__"

// SearchConfig holds the global web-search toggle, the active search provider
// and its credential.
type SearchConfig struct {
	Enabled bool `json:"enabled"`
	// Provider selects the search backend: "native" (default), "tavily", or
	// "youcom" (keyed by the YDC_API_KEY environment variable).
	Provider string `json:"provider"`
	// TavilyAPIKey is the token for the Tavily provider. Masked to MaskedAPIKey
	// on read so it never reaches the UI in the clear.
	TavilyAPIKey string `json:"tavilyApiKey"`
	// FetchTopK is how many ranked URLs the native engine reads in full before
	// synthesising an answer.
	FetchTopK int `json:"fetchTopK"`
	// PageChars is the per-page character cap fed into synthesis.
	PageChars int `json:"pageChars"`
	// CacheEnabled keeps search results, fetched pages and deep-search answers
	// so a repeated question costs nothing. Off means every request is live.
	CacheEnabled bool `json:"cacheEnabled"`
	// CacheSearchTTL, CachePageTTL and CacheAnswerTTL bound each cache kind, in
	// minutes.
	CacheSearchTTL int `json:"cacheSearchTTL"`
	CachePageTTL   int `json:"cachePageTTL"`
	CacheAnswerTTL int `json:"cacheAnswerTTL"`
	// Browser selects which native engine leads: "native", "safari" or
	// "chrome", or "" for the built-in ordering (HTTP first, the browser as the
	// fallback).
	Browser   string `json:"browser"`
	UpdatedAt int64  `json:"updatedAt"`
}

// normalise pins every field to a known, usable value. Applied on both read and
// write so consumers always see a valid config regardless of how the row was
// populated or what a client sent.
func (c *SearchConfig) normalise() {
	if c.Provider != SearchProviderTavily && c.Provider != SearchProviderYoucom {
		c.Provider = SearchProviderNative
	}
	c.FetchTopK = clampInt(c.FetchTopK, MinSearchFetchTopK, MaxSearchFetchTopK, DefaultSearchFetchTopK)
	c.PageChars = clampInt(c.PageChars, MinSearchPageChars, MaxSearchPageChars, DefaultSearchPageChars)
	c.CacheSearchTTL = clampInt(c.CacheSearchTTL, MinCacheTTLMinutes, MaxCacheTTLMinutes, DefaultCacheSearchTTL)
	c.CachePageTTL = clampInt(c.CachePageTTL, MinCacheTTLMinutes, MaxCacheTTLMinutes, DefaultCachePageTTL)
	c.CacheAnswerTTL = clampInt(c.CacheAnswerTTL, MinCacheTTLMinutes, MaxCacheTTLMinutes, DefaultCacheAnswerTTL)
	if c.Browser != SearchBrowserNative && c.Browser != SearchBrowserSafari && c.Browser != SearchBrowserChrome {
		c.Browser = ""
	}
}

// clampInt returns v clamped into [lo, hi]. A zero or negative v means "unset"
// (an untouched client field, or a row from before the column existed) and
// resolves to def rather than clamping up to the minimum.
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
// stored choice is honoured on every later read.
func GetSearchConfig(database *db.DB) (*SearchConfig, error) {
	var enabled, cacheEnabled int
	var provider, tavilyKey, browser string
	var fetchTopK, pageChars int
	var cacheSearchTTL, cachePageTTL, cacheAnswerTTL int
	var updatedAt int64
	err := database.QueryRow(
		`SELECT enabled, provider, tavily_api_key, fetch_top_k, page_chars, browser,
		        cache_enabled, cache_search_ttl_minutes, cache_page_ttl_minutes, cache_answer_ttl_minutes,
		        time_updated
		   FROM search_config WHERE id = 1`,
	).Scan(&enabled, &provider, &tavilyKey, &fetchTopK, &pageChars, &browser,
		&cacheEnabled, &cacheSearchTTL, &cachePageTTL, &cacheAnswerTTL, &updatedAt)
	if err == sql.ErrNoRows {
		def := &SearchConfig{Enabled: true, CacheEnabled: true, Provider: SearchProviderNative}
		def.normalise()
		return def, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get search config: %w", err)
	}
	cfg := &SearchConfig{
		Enabled:        enabled != 0,
		Provider:       provider,
		TavilyAPIKey:   tavilyKey,
		FetchTopK:      fetchTopK,
		PageChars:      pageChars,
		CacheEnabled:   cacheEnabled != 0,
		CacheSearchTTL: cacheSearchTTL,
		CachePageTTL:   cachePageTTL,
		CacheAnswerTTL: cacheAnswerTTL,
		Browser:        browser,
		UpdatedAt:      updatedAt,
	}
	cfg.normalise()
	return cfg, nil
}

// SetSearchConfig upserts the singleton config row, normalising every field
// before persisting so an invalid client payload can never store a bad value.
func SetSearchConfig(database *db.DB, c *SearchConfig) error {
	c.normalise()
	enabled := 0
	if c.Enabled {
		enabled = 1
	}
	// The legacy use_real_profile column is left in place rather than dropped:
	// it is NOT NULL DEFAULT 0, so omitting it here is safe, and keeping it
	// means an older binary can still read this database.
	cacheEnabled := 0
	if c.CacheEnabled {
		cacheEnabled = 1
	}
	_, err := database.Exec(`
		INSERT INTO search_config (id, enabled, provider, tavily_api_key, fetch_top_k, page_chars, browser,
		                           cache_enabled, cache_search_ttl_minutes, cache_page_ttl_minutes, cache_answer_ttl_minutes,
		                           time_updated)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			enabled                  = excluded.enabled,
			provider                 = excluded.provider,
			tavily_api_key           = excluded.tavily_api_key,
			fetch_top_k              = excluded.fetch_top_k,
			page_chars               = excluded.page_chars,
			browser                  = excluded.browser,
			cache_enabled            = excluded.cache_enabled,
			cache_search_ttl_minutes = excluded.cache_search_ttl_minutes,
			cache_page_ttl_minutes   = excluded.cache_page_ttl_minutes,
			cache_answer_ttl_minutes = excluded.cache_answer_ttl_minutes,
			time_updated             = excluded.time_updated
	`, enabled, c.Provider, c.TavilyAPIKey, c.FetchTopK, c.PageChars, c.Browser,
		cacheEnabled, c.CacheSearchTTL, c.CachePageTTL, c.CacheAnswerTTL,
		Now())
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
