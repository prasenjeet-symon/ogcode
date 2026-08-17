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

// SearchConfig holds the global web-search toggle plus the deep-research
// pipeline tuning knobs (pages fetched, per-page size).
type SearchConfig struct {
	Enabled        bool  `json:"enabled"`
	UseRealProfile bool  `json:"useRealProfile"`
	FetchTopK      int   `json:"fetchTopK"`
	PageChars      int   `json:"pageChars"`
	UpdatedAt      int64 `json:"updatedAt"`
}

// clampParams clamps the research knobs into their valid range, substituting the
// default for any zero/out-of-range value. Applied on both read and write so
// consumers always see usable numbers regardless of how the row was populated.
func (c *SearchConfig) clampParams() {
	c.FetchTopK = clampInt(c.FetchTopK, minSearchFetchTopK, maxSearchFetchTopK, DefaultSearchFetchTopK)
	c.PageChars = clampInt(c.PageChars, minSearchPageChars, maxSearchPageChars, DefaultSearchPageChars)
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
// defaults (disabled, with default research params). Research params are always
// clamped so callers never receive zero/invalid values.
func GetSearchConfig(database *db.DB) (*SearchConfig, error) {
	var enabled, useRealProfile, fetchTopK, pageChars int
	var updatedAt int64
	err := database.QueryRow(
		`SELECT enabled, use_real_profile, fetch_top_k, page_chars, time_updated FROM search_config WHERE id = 1`,
	).Scan(&enabled, &useRealProfile, &fetchTopK, &pageChars, &updatedAt)
	if err == sql.ErrNoRows {
		def := &SearchConfig{}
		def.clampParams()
		return def, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get search config: %w", err)
	}
	cfg := &SearchConfig{
		Enabled:        enabled != 0,
		UseRealProfile: useRealProfile != 0,
		FetchTopK:      fetchTopK,
		PageChars:      pageChars,
		UpdatedAt:      updatedAt,
	}
	cfg.clampParams()
	return cfg, nil
}

// SetSearchConfig upserts the singleton config row, clamping the research params
// before persisting so an invalid client payload can never store bad values.
func SetSearchConfig(database *db.DB, c *SearchConfig) error {
	c.clampParams()
	enabled, useRealProfile := 0, 0
	if c.Enabled {
		enabled = 1
	}
	if c.UseRealProfile {
		useRealProfile = 1
	}
	_, err := database.Exec(`
		INSERT INTO search_config (id, enabled, use_real_profile, fetch_top_k, page_chars, time_updated)
		VALUES (1, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			enabled          = excluded.enabled,
			use_real_profile = excluded.use_real_profile,
			fetch_top_k      = excluded.fetch_top_k,
			page_chars       = excluded.page_chars,
			time_updated     = excluded.time_updated
	`, enabled, useRealProfile, c.FetchTopK, c.PageChars, Now())
	if err != nil {
		return fmt.Errorf("set search config: %w", err)
	}
	return nil
}
