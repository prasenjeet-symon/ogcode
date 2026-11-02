package session

import (
	"path/filepath"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

// newSearchTestDB opens a migrated, empty database in a temp dir.
func newSearchTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

// TestSearchConfigDefaultsToEnabled pins the on-by-default contract.
//
// Web search used to require a Node/Playwright install, so shipping it off by
// default was the honest thing to do — enabling it without that setup produced
// a broken tool. The backend is now compiled into the binary, so there is no
// setup step left to gate on, and a fresh install should have deep_search
// working with nothing configured.
func TestSearchConfigDefaultsToEnabled(t *testing.T) {
	cfg, err := GetSearchConfig(newSearchTestDB(t))
	if err != nil {
		t.Fatalf("get search config: %v", err)
	}
	if !cfg.Enabled {
		t.Error("web search should be enabled by default on a fresh database")
	}
}

// TestSearchConfigRespectsExplicitDisable is the other half of the contract:
// on-by-default must not mean impossible-to-turn-off. A stored false has to
// survive a round trip, or the settings toggle would silently do nothing.
func TestSearchConfigRespectsExplicitDisable(t *testing.T) {
	database := newSearchTestDB(t)

	if err := SetSearchConfig(database, &SearchConfig{Enabled: false}); err != nil {
		t.Fatalf("set search config: %v", err)
	}
	cfg, err := GetSearchConfig(database)
	if err != nil {
		t.Fatalf("get search config: %v", err)
	}
	if cfg.Enabled {
		t.Error("an explicitly disabled config came back enabled")
	}
}

// TestSearchConfigNativeKnobsRoundTrip pins that the three native-engine knobs
// the settings screen now owns survive a write/read cycle. They were pulled
// from the environment-only set back into the database, so a stored value that
// did not come back would leave the screen silently unable to tune the engine.
func TestSearchConfigNativeKnobsRoundTrip(t *testing.T) {
	database := newSearchTestDB(t)

	want := &SearchConfig{
		Enabled:   true,
		Provider:  SearchProviderNative,
		FetchTopK: 7,
		PageChars: 9000,
		Browser:   SearchBrowserSafari,
	}
	if err := SetSearchConfig(database, want); err != nil {
		t.Fatalf("set search config: %v", err)
	}
	cfg, err := GetSearchConfig(database)
	if err != nil {
		t.Fatalf("get search config: %v", err)
	}
	if cfg.FetchTopK != 7 || cfg.PageChars != 9000 || cfg.Browser != SearchBrowserSafari {
		t.Fatalf("round trip lost knobs: got %+v", cfg)
	}
}

// TestSearchConfigCacheSettingsRoundTrip pins the web cache's knobs. The enable
// flag defaults ON for the same reason search itself does: a fresh install
// should get the fast path with nothing configured, and an explicit off must
// survive a round trip or the settings toggle would silently do nothing.
func TestSearchConfigCacheSettingsRoundTrip(t *testing.T) {
	database := newSearchTestDB(t)

	def, err := GetSearchConfig(database)
	if err != nil {
		t.Fatalf("get search config: %v", err)
	}
	if !def.CacheEnabled {
		t.Error("the web cache should be enabled by default on a fresh database")
	}
	if def.CacheSearchTTL != DefaultCacheSearchTTL || def.CachePageTTL != DefaultCachePageTTL || def.CacheAnswerTTL != DefaultCacheAnswerTTL {
		t.Fatalf("fresh defaults lost: got %+v", def)
	}

	if err := SetSearchConfig(database, &SearchConfig{
		Enabled:        true,
		CacheEnabled:   true,
		CacheSearchTTL: 30,
		CachePageTTL:   2880,
		CacheAnswerTTL: 120,
	}); err != nil {
		t.Fatalf("set search config: %v", err)
	}
	cfg, err := GetSearchConfig(database)
	if err != nil {
		t.Fatalf("get search config: %v", err)
	}
	if !cfg.CacheEnabled || cfg.CacheSearchTTL != 30 || cfg.CachePageTTL != 2880 || cfg.CacheAnswerTTL != 120 {
		t.Fatalf("round trip lost the cache settings: got %+v", cfg)
	}

	if err := SetSearchConfig(database, &SearchConfig{Enabled: true, CacheEnabled: false}); err != nil {
		t.Fatalf("set search config: %v", err)
	}
	if off, err := GetSearchConfig(database); err != nil {
		t.Fatalf("get search config: %v", err)
	} else if off.CacheEnabled {
		t.Error("an explicitly disabled cache came back enabled")
	}

	// Out of range clamps to the nearest bound; unset (zero) falls back to the
	// default rather than clamping up to the minimum.
	if err := SetSearchConfig(database, &SearchConfig{
		Enabled:        true,
		CacheEnabled:   true,
		CacheSearchTTL: 999999,
		CachePageTTL:   1,
		CacheAnswerTTL: 0,
	}); err != nil {
		t.Fatalf("set search config: %v", err)
	}
	clamped, err := GetSearchConfig(database)
	if err != nil {
		t.Fatalf("get search config: %v", err)
	}
	if clamped.CacheSearchTTL != MaxCacheTTLMinutes || clamped.CachePageTTL != MinCacheTTLMinutes || clamped.CacheAnswerTTL != DefaultCacheAnswerTTL {
		t.Fatalf("clamping lost: got %+v", clamped)
	}
}

// TestSearchConfigNormalisesNativeKnobs pins the read/write guard: an
// out-of-range value clamps to the nearest bound, an unset (zero) value falls
// back to the default, and an unrecognised browser collapses to the empty
// string. A client cannot store a value the engine would choke on.
func TestSearchConfigNormalisesNativeKnobs(t *testing.T) {
	tests := map[string]struct {
		in   SearchConfig
		want SearchConfig
	}{
		"clamp high": {
			in:   SearchConfig{Enabled: true, FetchTopK: 99, PageChars: 999999},
			want: SearchConfig{Enabled: true, FetchTopK: MaxSearchFetchTopK, PageChars: MaxSearchPageChars},
		},
		"clamp low": {
			in:   SearchConfig{Enabled: true, FetchTopK: 1, PageChars: 1},
			want: SearchConfig{Enabled: true, FetchTopK: MinSearchFetchTopK, PageChars: MinSearchPageChars},
		},
		"zero means default": {
			in:   SearchConfig{Enabled: true},
			want: SearchConfig{Enabled: true, FetchTopK: DefaultSearchFetchTopK, PageChars: DefaultSearchPageChars},
		},
		"unknown browser collapses": {
			in:   SearchConfig{Enabled: true, Browser: "firefox"},
			want: SearchConfig{Enabled: true, Browser: "", FetchTopK: DefaultSearchFetchTopK, PageChars: DefaultSearchPageChars},
		},
		"chrome browser preserved": {
			in:   SearchConfig{Enabled: true, Browser: SearchBrowserChrome},
			want: SearchConfig{Enabled: true, Browser: SearchBrowserChrome, FetchTopK: DefaultSearchFetchTopK, PageChars: DefaultSearchPageChars},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			database := newSearchTestDB(t)
			in := tc.in
			if err := SetSearchConfig(database, &in); err != nil {
				t.Fatalf("set search config: %v", err)
			}
			cfg, err := GetSearchConfig(database)
			if err != nil {
				t.Fatalf("get search config: %v", err)
			}
			if cfg.FetchTopK != tc.want.FetchTopK || cfg.PageChars != tc.want.PageChars || cfg.Browser != tc.want.Browser {
				t.Fatalf("got %+v, want fetchTopK=%d pageChars=%d browser=%q",
					cfg, tc.want.FetchTopK, tc.want.PageChars, tc.want.Browser)
			}
		})
	}
}
