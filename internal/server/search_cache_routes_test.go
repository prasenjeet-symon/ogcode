package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/webcache"
)

// searchCacheBody decodes a cache-route response into a generic map so the test
// asserts the wire names themselves, not whatever a struct happens to be.
func searchCacheBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v (body: %s)", err, rec.Body.String())
	}
	return body
}

// TestSearchCacheRouteReportsDisabledCache pins the "off" state: a nil cache is
// a normal answer, not an error, and every field the settings screen reads is
// still present with the built-in lifetimes.
func TestSearchCacheRouteReportsDisabledCache(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/search/cache", nil))

	body := searchCacheBody(t, rec)
	if enabled, ok := body["enabled"].(bool); !ok || enabled {
		t.Fatalf("enabled = %v, want false", body["enabled"])
	}
	for _, key := range []string{"entries", "bytes", "hits", "misses", "evictions"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("missing %q in %v", key, body)
		}
	}
	ttl, ok := body["ttlSeconds"].(map[string]any)
	if !ok {
		t.Fatalf("ttlSeconds = %v, want an object", body["ttlSeconds"])
	}
	want := map[string]float64{
		"search": webcache.DefaultSearchTTL.Seconds(),
		"page":   webcache.DefaultPageTTL.Seconds(),
		"answer": webcache.DefaultAnswerTTL.Seconds(),
	}
	for key, seconds := range want {
		if got, _ := ttl[key].(float64); got != seconds {
			t.Errorf("ttlSeconds.%s = %v, want %v", key, ttl[key], seconds)
		}
	}
}

// TestSearchCacheClearRouteWithNilCache pins that clearing an absent cache is a
// 200 with zero deleted rows rather than a 500.
func TestSearchCacheClearRouteWithNilCache(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/search/cache/clear", nil))

	body := searchCacheBody(t, rec)
	if deleted, _ := body["deleted"].(float64); deleted != 0 {
		t.Fatalf("deleted = %v, want 0", body["deleted"])
	}
}

// TestSearchCacheClearRouteDeletesRows drives a real cache: the GET reports it
// as enabled with its one entry, and the POST reports exactly that row gone.
func TestSearchCacheClearRouteDeletesRows(t *testing.T) {
	srv := newTestServer(t)
	cache, err := webcache.Open(srv.globalDB.DB, webcache.Options{
		Enabled: true,
		TTL:     map[webcache.Kind]time.Duration{webcache.KindSearch: time.Hour},
	})
	if err != nil {
		t.Fatalf("open web cache: %v", err)
	}
	t.Cleanup(func() { cache.Close() })
	key := webcache.Key(webcache.KindSearch, "ogcode caching")
	if err := cache.Put(webcache.KindSearch, key, []byte(`{"results":[]}`)); err != nil {
		t.Fatalf("put cache entry: %v", err)
	}
	srv.webCache = cache

	h := srv.routes()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/search/cache", nil))
	body := searchCacheBody(t, rec)
	if enabled, _ := body["enabled"].(bool); !enabled {
		t.Fatalf("enabled = %v, want true", body["enabled"])
	}
	if entries, _ := body["entries"].(float64); entries != 1 {
		t.Fatalf("entries = %v, want 1", body["entries"])
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/search/cache/clear", nil))
	if deleted, _ := searchCacheBody(t, rec)["deleted"].(float64); deleted != 1 {
		t.Fatalf("deleted = %v, want 1", deleted)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/search/cache", nil))
	if entries, _ := searchCacheBody(t, rec)["entries"].(float64); entries != 0 {
		t.Fatalf("entries after clear = %v, want 0", entries)
	}
}

// TestResolveWebCacheOptionsEnvWins pins the documented precedence: the
// environment overrides the stored row, and an unparseable TTL is ignored
// rather than becoming a zero lifetime that would disable one kind.
func TestResolveWebCacheOptionsEnvWins(t *testing.T) {
	cfg := &session.SearchConfig{
		CacheEnabled:   true,
		CacheSearchTTL: 30,
		CachePageTTL:   60,
		CacheAnswerTTL: 90,
	}

	opts := resolveWebCacheOptions(cfg)
	if !opts.Enabled {
		t.Fatal("stored toggle on, want enabled")
	}
	if got, want := opts.TTL[webcache.KindSearch], 30*time.Minute; got != want {
		t.Errorf("search ttl = %v, want %v", got, want)
	}

	t.Setenv("OGCODE_SEARCH_CACHE", "OFF")
	if resolveWebCacheOptions(cfg).Enabled {
		t.Error("OGCODE_SEARCH_CACHE=OFF left the cache enabled")
	}
	t.Setenv("OGCODE_SEARCH_CACHE", "")
	t.Setenv("OGCODE_SEARCH_CACHE_SEARCH_TTL", "90m")
	t.Setenv("OGCODE_SEARCH_CACHE_PAGE_TTL", "48h")
	t.Setenv("OGCODE_SEARCH_CACHE_ANSWER_TTL", "not-a-duration")
	opts = resolveWebCacheOptions(cfg)
	if got, want := opts.TTL[webcache.KindSearch], 90*time.Minute; got != want {
		t.Errorf("search ttl = %v, want %v", got, want)
	}
	if got, want := opts.TTL[webcache.KindPage], 48*time.Hour; got != want {
		t.Errorf("page ttl = %v, want %v", got, want)
	}
	if got, want := opts.TTL[webcache.KindAnswer], 90*time.Minute; got != want {
		t.Errorf("answer ttl = %v, want the stored %v", got, want)
	}
	if opts.L1Entries != webCacheL1Entries || opts.L2Rows != webCacheL2Rows {
		t.Errorf("caps = (%d, %d), want (%d, %d)", opts.L1Entries, opts.L2Rows, webCacheL1Entries, webCacheL2Rows)
	}
}

// TestResolveWebCacheOptionsDefaults pins that an unset stored value falls back
// to the package default rather than a zero TTL.
func TestResolveWebCacheOptionsDefaults(t *testing.T) {
	opts := resolveWebCacheOptions(&session.SearchConfig{CacheEnabled: true})
	for kind, want := range map[webcache.Kind]time.Duration{
		webcache.KindSearch: webcache.DefaultSearchTTL,
		webcache.KindPage:   webcache.DefaultPageTTL,
		webcache.KindAnswer: webcache.DefaultAnswerTTL,
	} {
		if got := opts.TTL[kind]; got != want {
			t.Errorf("%s ttl = %v, want %v", kind, got, want)
		}
	}
}
