package search

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/webcache"
)

// openTestCache returns a live cache on a throwaway database, opened the way
// the server opens it (db.Open runs the migrations that create web_cache).
func openTestCache(t *testing.T) *webcache.Cache {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	c, err := webcache.Open(database.DB, webcache.Options{
		Enabled: true,
		TTL: map[webcache.Kind]time.Duration{
			webcache.KindSearch: time.Hour,
			webcache.KindPage:   time.Hour,
		},
		L1Entries: 64,
		L1Bytes:   1 << 20,
		L2Rows:    1000,
		L2Bytes:   16 << 20,
	})
	if err != nil {
		t.Fatalf("open webcache: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func cachedFake(name string) *fakeBackend {
	return &fakeBackend{
		name:    name,
		results: []SearchResult{{Title: "t", URL: "https://example.com", Snippet: "s", Provider: name}},
		page:    PageContent{URL: "https://example.com", Title: "t", Text: "body text", Provider: name},
	}
}

// TestWithCacheServesASecondSearchFromCache is the whole point of the decorator:
// the same query at the same limit must reach the network once per TTL, not
// once per call.
func TestWithCacheServesASecondSearchFromCache(t *testing.T) {
	inner := cachedFake(ProviderNative)
	b := WithCache(inner, openTestCache(t))
	ctx := context.Background()

	first, err := b.Search(ctx, "go generics", 5)
	if err != nil {
		t.Fatalf("first search: %v", err)
	}
	second, err := b.Search(ctx, "go generics", 5)
	if err != nil {
		t.Fatalf("second search: %v", err)
	}

	if n := inner.searches.Load(); n != 1 {
		t.Errorf("inner backend ran %d times; want 1", n)
	}
	if len(second) != len(first) || second[0].URL != first[0].URL {
		t.Errorf("cached results differ: %+v vs %+v", second, first)
	}
	if got := b.Name(); got != ProviderNative {
		t.Errorf("Name() = %q; want %q", got, ProviderNative)
	}
}

// TestWithCacheKeysSearchesByProvider pins why Name() is in the key: Tavily's
// ranking is not the native chain's, so after a provider switch the same query
// must miss instead of serving the old engine's results under the new stamp.
func TestWithCacheKeysSearchesByProvider(t *testing.T) {
	c := openTestCache(t)
	ctx := context.Background()

	native := cachedFake(ProviderNative)
	if _, err := WithCache(native, c).Search(ctx, "same query", 5); err != nil {
		t.Fatalf("native search: %v", err)
	}

	tavily := cachedFake(ProviderTavily)
	got, err := WithCache(tavily, c).Search(ctx, "same query", 5)
	if err != nil {
		t.Fatalf("tavily search: %v", err)
	}
	if n := tavily.searches.Load(); n != 1 {
		t.Errorf("the second provider ran %d searches; want 1 — a provider switch must miss the cache", n)
	}
	if len(got) != 1 || got[0].Provider != ProviderTavily {
		t.Errorf("results = %+v; want the tavily provider's", got)
	}
}

// TestWithCacheOnlyStoresSuccesses pins the two exclusions: an error and an
// empty result set both look like "nothing found", and caching either would
// freeze the failure in place for the whole TTL.
func TestWithCacheOnlyStoresSuccesses(t *testing.T) {
	ctx := context.Background()

	failing := &fakeBackend{err: errors.New("engine challenge")}
	fb := WithCache(failing, openTestCache(t))
	for i := 0; i < 2; i++ {
		if _, err := fb.Search(ctx, "q", 5); err == nil {
			t.Fatal("cached backend swallowed the inner error")
		}
	}
	if n := failing.searches.Load(); n != 2 {
		t.Errorf("failing backend ran %d times; an error must not be cached", n)
	}

	empty := &fakeBackend{results: nil}
	eb := WithCache(empty, openTestCache(t))
	for i := 0; i < 2; i++ {
		got, err := eb.Search(ctx, "q", 5)
		if err != nil || len(got) != 0 {
			t.Fatalf("empty search = %v,%v; want no results and no error", got, err)
		}
	}
	if n := empty.searches.Load(); n != 2 {
		t.Errorf("empty backend ran %d times; an empty result set must not be cached", n)
	}

	textless := &fakeBackend{page: PageContent{URL: "https://example.com"}}
	pb := WithCache(textless, openTestCache(t))
	for i := 0; i < 2; i++ {
		if _, err := pb.FetchPage(ctx, "https://example.com"); err != nil {
			t.Fatalf("textless fetch: %v", err)
		}
	}
	if n := textless.fetches.Load(); n != 2 {
		t.Errorf("backend ran %d fetches; a page with no text must not be cached", n)
	}
}

// TestWithCacheNormalisesKeys pins the canonical forms: spacing in a query and
// URL spelling (trailing slash, fragment, stray spaces) must not each buy a
// separate network round trip.
func TestWithCacheNormalisesKeys(t *testing.T) {
	ctx := context.Background()
	inner := cachedFake(ProviderNative)
	b := WithCache(inner, openTestCache(t))

	for _, q := range []string{"go  generics", " go generics ", "go generics"} {
		if _, err := b.Search(ctx, q, 5); err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
	}
	if n := inner.searches.Load(); n != 1 {
		t.Errorf("the same query in three spellings reached the backend %d times; want 1", n)
	}

	for _, u := range []string{"https://example.com/a", "https://example.com/a/", " https://example.com/a#section "} {
		if _, err := b.FetchPage(ctx, u); err != nil {
			t.Fatalf("fetch %q: %v", u, err)
		}
	}
	if n := inner.fetches.Load(); n != 1 {
		t.Errorf("the same page in three spellings reached the backend %d times; want 1", n)
	}
}

// TestWithCacheReturnsAFreshSlice pins the aliasing rule: the fallback chain
// rewrites Provider on the slice Search returns, so a hit must be unmarshalled
// fresh instead of handing out the array some earlier caller may still hold.
func TestWithCacheReturnsAFreshSlice(t *testing.T) {
	inner := cachedFake(ProviderNative)
	b := WithCache(inner, openTestCache(t))
	ctx := context.Background()

	first, err := b.Search(ctx, "q", 5)
	if err != nil {
		t.Fatalf("first search: %v", err)
	}
	first[0].Provider = "native (tavily fallback)" // what fallbackBackend does
	first[0].URL = "https://corrupted.example"

	second, err := b.Search(ctx, "q", 5)
	if err != nil {
		t.Fatalf("second search: %v", err)
	}
	if second[0].Provider != ProviderNative || second[0].URL != "https://example.com" {
		t.Errorf("second hit = %+v; a caller's edit to the first slice leaked into the cache", second)
	}
	if n := inner.searches.Load(); n != 1 {
		t.Errorf("inner backend ran %d times; the second call should have hit the cache", n)
	}
}

// TestWithCacheBypassSkipsTheReadButRefreshes pins the escape hatch: a caller
// that needs a live answer asks for a bypass, which skips the read and still
// stores what came back, so the next ordinary call is served the fresh copy.
func TestWithCacheBypassSkipsTheReadButRefreshes(t *testing.T) {
	inner := cachedFake(ProviderNative)
	b := WithCache(inner, openTestCache(t))
	ctx := context.Background()

	if _, err := b.Search(ctx, "q", 5); err != nil {
		t.Fatalf("first search: %v", err)
	}
	inner.results = []SearchResult{{Title: "fresh", URL: "https://fresh.example", Provider: ProviderNative}}

	bypassed, err := b.Search(webcache.Bypass(ctx), "q", 5)
	if err != nil {
		t.Fatalf("bypass search: %v", err)
	}
	if n := inner.searches.Load(); n != 2 {
		t.Fatalf("inner backend ran %d times; a bypass must not read the cache", n)
	}
	if bypassed[0].URL != "https://fresh.example" {
		t.Errorf("bypass returned %+v; want the live answer", bypassed)
	}

	after, err := b.Search(ctx, "q", 5)
	if err != nil {
		t.Fatalf("search after bypass: %v", err)
	}
	if after[0].URL != "https://fresh.example" {
		t.Errorf("cached entry = %+v; a bypassed call should still refresh it", after)
	}
	if n := inner.searches.Load(); n != 2 {
		t.Errorf("inner backend ran %d times; the refreshed entry should now serve", n)
	}
}

// TestWithCacheBypassRefreshesAPage covers the same escape hatch on the page
// path: a bypassed fetch reaches the backend and its answer replaces the stored
// copy instead of being discarded.
func TestWithCacheBypassRefreshesAPage(t *testing.T) {
	inner := cachedFake(ProviderNative)
	b := WithCache(inner, openTestCache(t))
	ctx := context.Background()

	if _, err := b.FetchPage(ctx, "https://example.com"); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	inner.page = PageContent{URL: "https://example.com", Title: "fresh", Text: "fresh text", Provider: ProviderNative}

	bypassed, err := b.FetchPage(webcache.Bypass(ctx), "https://example.com")
	if err != nil {
		t.Fatalf("bypass fetch: %v", err)
	}
	if bypassed.Text != "fresh text" {
		t.Errorf("bypass returned %+v; want the live page", bypassed)
	}

	after, err := b.FetchPage(ctx, "https://example.com")
	if err != nil {
		t.Fatalf("fetch after bypass: %v", err)
	}
	if after.Text != "fresh text" {
		t.Errorf("cached page = %+v; a bypassed fetch should still refresh it", after)
	}
	if n := inner.fetches.Load(); n != 2 {
		t.Errorf("inner backend ran %d fetches; the refreshed page should now serve", n)
	}
}

// TestWithCacheNilCacheDelegates pins the nil-safety the wiring relies on: with
// caching switched off the decorator must be invisible.
func TestWithCacheNilCacheDelegates(t *testing.T) {
	ctx := context.Background()
	inner := cachedFake(ProviderNative)
	b := WithCache(inner, nil)

	for i := 0; i < 2; i++ {
		if _, err := b.Search(ctx, "q", 5); err != nil {
			t.Fatalf("search: %v", err)
		}
		if _, err := b.FetchPage(ctx, "https://example.com"); err != nil {
			t.Fatalf("fetch: %v", err)
		}
	}
	if n := inner.searches.Load(); n != 2 {
		t.Errorf("inner backend ran %d searches; a disabled cache must not answer", n)
	}
	if n := inner.fetches.Load(); n != 2 {
		t.Errorf("inner backend ran %d fetches; a disabled cache must not answer", n)
	}
}

func TestCanonicalPageURL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://example.com/a", "https://example.com/a"},
		{"https://example.com/a/", "https://example.com/a"},
		{"  https://example.com/a  ", "https://example.com/a"},
		{"https://example.com/a#section", "https://example.com/a"},
		{"https://example.com/a/#section", "https://example.com/a"},
	}
	for _, tt := range tests {
		if got := canonicalPageURL(tt.in); got != tt.want {
			t.Errorf("canonicalPageURL(%q) = %q; want %q", tt.in, got, tt.want)
		}
	}
}
