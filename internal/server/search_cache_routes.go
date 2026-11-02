package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/webcache"
)

// searchCacheTTLPayload reports each cache kind's lifetime in seconds, so the
// settings screen can show what the stored minute values resolved to.
type searchCacheTTLPayload struct {
	Search int64 `json:"search"`
	Page   int64 `json:"page"`
	Answer int64 `json:"answer"`
}

// searchCachePayload is the shape of GET /api/search/cache.
type searchCachePayload struct {
	Enabled    bool                  `json:"enabled"`
	Entries    int64                 `json:"entries"`
	Bytes      int64                 `json:"bytes"`
	Hits       int64                 `json:"hits"`
	Misses     int64                 `json:"misses"`
	Evictions  int64                 `json:"evictions"`
	TTLSeconds searchCacheTTLPayload `json:"ttlSeconds"`
}

// handleGetSearchCache reports the web cache's size, hit counters and resolved
// lifetimes. A nil cache (disabled, or the table failed to open) answers 200
// with enabled:false and the built-in defaults: "off" is a state the UI renders,
// not an error, and every number it shows still has a value to show.
func (s *Server) handleGetSearchCache(w http.ResponseWriter, r *http.Request) {
	stats := s.webCache.Stats()
	writeJSON(w, http.StatusOK, searchCachePayload{
		Enabled:   s.webCache.Enabled(),
		Entries:   stats.Entries,
		Bytes:     stats.Bytes,
		Hits:      stats.Hits,
		Misses:    stats.Misses,
		Evictions: stats.Evictions,
		TTLSeconds: searchCacheTTLPayload{
			Search: ttlSeconds(s.webCache.TTL(webcache.KindSearch)),
			Page:   ttlSeconds(s.webCache.TTL(webcache.KindPage)),
			Answer: ttlSeconds(s.webCache.TTL(webcache.KindAnswer)),
		},
	})
}

// handleClearSearchCache empties both levels of the cache and reports how many
// stored rows went with it. Clearing an already-empty or absent cache is a
// no-op, not an error: the endpoint's promise is "nothing is cached now", and
// that holds either way.
func (s *Server) handleClearSearchCache(w http.ResponseWriter, r *http.Request) {
	deleted, err := s.webCache.Clear()
	if err != nil {
		slog.Warn("clear web cache", "err", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}

// ttlSeconds renders a lifetime for the wire without rounding a sub-second
// lifetime down to "0 seconds, i.e. never expires".
func ttlSeconds(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	secs := int64(d / time.Second)
	if secs == 0 {
		return 1
	}
	return secs
}
