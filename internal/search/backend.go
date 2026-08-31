package search

import (
	"context"
	"fmt"
	"log/slog"
)

// SearchResult is one entry returned by a Backend's Search.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// PageContent is the extracted text of a single fetched page.
type PageContent struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

// Backend is the web-search capability behind the web_search and fetch_page
// tools and the deep-research pipeline. NativeBackend is the only
// implementation; the interface remains so the tools and the pipeline depend on
// the capability rather than the concrete type, and so a test can substitute a
// fake without going to the network.
type Backend interface {
	// Search returns up to limit results for query, best first.
	Search(ctx context.Context, query string, limit int) ([]SearchResult, error)
	// FetchPage returns the readable text content of a single URL.
	FetchPage(ctx context.Context, url string) (PageContent, error)
}

var _ Backend = (*NativeBackend)(nil)

// NewFallbackBackend returns a Backend that answers from primary and only
// reaches for secondary when primary comes back with nothing usable.
//
// Composition lives here rather than inside either backend so the order is a
// wiring decision, stated once at the call site, instead of a property baked
// into one of them. That matters because the order has changed once already and
// the arguments for it are not permanent: the browser path was the default
// while the HTTP path was easy to block, and became the fallback once the HTTP
// path stopped being.
//
// A nil secondary yields primary unchanged, which is what the non-darwin build
// of NewSafariBackend produces — so callers wire the chain unconditionally and
// the platform question stays in one place.
func NewFallbackBackend(primary, secondary Backend) Backend {
	if secondary == nil {
		return primary
	}
	if primary == nil {
		return secondary
	}
	return &fallbackBackend{primary: primary, secondary: secondary}
}

type fallbackBackend struct {
	primary   Backend
	secondary Backend
}

var _ Backend = (*fallbackBackend)(nil)

// Search falls through on an error and on an empty result set.
//
// Empty counts as failure here, which is a deliberate choice rather than an
// oversight. The primary reports "no results" both when an engine honestly has
// none and when every engine in its chain served a challenge page that parsed
// to nothing — and those are indistinguishable from the outside. Retrying the
// handful of genuinely obscure queries through the slower path is a much
// cheaper mistake than silently reporting "nothing found" for a query the web
// can answer, which is the failure the fallback exists to prevent.
func (f *fallbackBackend) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	results, err := f.primary.Search(ctx, query, limit)
	if err == nil && len(results) > 0 {
		return results, nil
	}
	// A cancelled turn is not a failed search, and opening a browser window to
	// answer a question nobody is waiting for is the one outcome worse than no
	// answer.
	if ctx.Err() != nil {
		return results, err
	}
	slog.Info("web search: falling back to the browser", "query", query, "err", err)

	fallbackResults, fallbackErr := f.secondary.Search(ctx, query, limit)
	if fallbackErr == nil && len(fallbackResults) > 0 {
		return fallbackResults, nil
	}
	// Prefer the primary's failure in the report: it is the path that runs
	// every time, so it is the one worth telling the user about.
	if err != nil {
		return nil, err
	}
	return fallbackResults, fallbackErr
}

// FetchPage falls through on an error only.
//
// Unlike a search, a fetch that succeeds has returned the page it was asked
// for; there is no equivalent of "parsed to nothing but reported success",
// because the primary already treats unextractable content as an error. The
// errors worth a second attempt are exactly the ones the browser is good at —
// a bot challenge, or a page that exists only after its scripts run.
func (f *fallbackBackend) FetchPage(ctx context.Context, url string) (PageContent, error) {
	page, err := f.primary.FetchPage(ctx, url)
	if err == nil {
		return page, nil
	}
	if ctx.Err() != nil {
		return page, err
	}
	slog.Info("web search: falling back to the browser for a page", "url", url, "err", err)

	fallbackPage, fallbackErr := f.secondary.FetchPage(ctx, url)
	if fallbackErr != nil {
		// The primary's error describes the ordinary path and is the more
		// useful of the two; the browser's is kept alongside it because "both
		// refused" and "the browser could not run" are different problems.
		return PageContent{}, fmt.Errorf("%w (browser fallback: %v)", err, fallbackErr)
	}
	return fallbackPage, nil
}
