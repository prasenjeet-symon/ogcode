package search

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/webcache"
)

// cacheBackend serves successfully answered searches and page fetches out of a
// persistent cache before asking the backend below it.
//
// It sits above SwitchableBackend rather than inside a single backend, so the
// same cached page is shared by the web_search and fetch_page tools and the
// deep-research pipeline, and survives a provider switch in settings. Cached
// values are always unmarshalled into a fresh slice on the way out: the
// fallback chain rewrites Provider on the slice its Search returns, so handing
// out a shared slice would silently corrupt what later callers read back.
type cacheBackend struct {
	inner Backend
	cache *webcache.Cache
}

var _ Backend = (*cacheBackend)(nil)

// WithCache wraps b so successful searches and page fetches are served from c.
// A nil or disabled c leaves b's behaviour untouched, so the wiring can stay
// unconditional.
func WithCache(b Backend, c *webcache.Cache) Backend {
	return &cacheBackend{inner: b, cache: c}
}

// Name reports the wrapped backend's provider name.
func (b *cacheBackend) Name() string { return b.inner.Name() }

// Search answers from the cache when the same query at the same limit was
// already asked of the same provider, otherwise delegates and stores what came
// back. Errors and empty result sets are never stored: "no results" is also
// what a challenged engine chain reports, and caching it would freeze that
// failure in place for the whole TTL.
func (b *cacheBackend) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if !b.cache.Enabled() {
		return b.inner.Search(ctx, query, limit)
	}
	// The provider is part of the key on purpose: results are provider-specific,
	// so after a switch from one engine to another the same query must miss
	// rather than serve the other engine's ranking under the new attribution.
	key := webcache.Key(webcache.KindSearch, b.inner.Name(), normalizeWhitespace(query), strconv.Itoa(limit))

	// A bypass skips the read only: the live answer is still stored below, so
	// the next ordinary call does not keep serving what the caller just
	// declared stale.
	if !webcache.IsBypass(ctx) {
		if body, ok := b.cache.Get(webcache.KindSearch, key); ok {
			var results []SearchResult
			err := json.Unmarshal(body, &results)
			if err == nil {
				return results, nil
			}
			// A payload we cannot read is as good as absent; search again rather
			// than fail the call over a cache bug.
			slog.Warn("web search: cached result set is unreadable, searching again", "err", err)
		}
	}

	results, err := b.inner.Search(ctx, query, limit)
	if err != nil || len(results) == 0 {
		return results, err
	}
	b.store(webcache.KindSearch, key, results)
	return results, nil
}

// FetchPage answers from the cache when the same URL was fetched before.
// Whether a page needs a fresh copy (a browser's rendered view, say) is the
// caller's decision: it asks with webcache.Bypass, which skips the read but
// still refreshes the stored copy.
func (b *cacheBackend) FetchPage(ctx context.Context, url string) (PageContent, error) {
	if !b.cache.Enabled() {
		return b.inner.FetchPage(ctx, url)
	}
	// No provider in the key: page text is the page text, whichever engine
	// fetched it.
	key := webcache.Key(webcache.KindPage, canonicalPageURL(url))

	if !webcache.IsBypass(ctx) {
		if body, ok := b.cache.Get(webcache.KindPage, key); ok {
			var page PageContent
			err := json.Unmarshal(body, &page)
			if err == nil {
				return page, nil
			}
			slog.Warn("web search: cached page is unreadable, fetching again", "err", err)
		}
	}

	page, err := b.inner.FetchPage(ctx, url)
	if err != nil || page.Text == "" {
		return page, err
	}
	b.store(webcache.KindPage, key, page)
	return page, nil
}

// store marshals v and writes it under key. A cache failure is logged at Warn
// and swallowed: an unhealthy cache must never turn a good search into a failed
// one.
func (b *cacheBackend) store(kind webcache.Kind, key string, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		slog.Warn("web search: could not encode cache entry", "kind", kind, "err", err)
		return
	}
	if err := b.cache.Put(kind, key, body); err != nil {
		slog.Warn("web search: could not store cache entry", "kind", kind, "err", err)
	}
}

// canonicalPageURL normalises a URL for equality, matching dedupKey's shape so
// the same page reached as "https://x/y/", " https://x/y " and "https://x/y#frag"
// shares one entry.
func canonicalPageURL(u string) string {
	u = strings.TrimSpace(u)
	if i := strings.Index(u, "#"); i >= 0 {
		u = u[:i]
	}
	return strings.TrimRight(u, "/")
}
