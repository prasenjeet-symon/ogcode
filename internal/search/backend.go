package search

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// SearchResult is one entry returned by a Backend's Search.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
	// Provider names the backend that produced this result. A fallback
	// overwrites the stamp when its secondary answers, so it always names the
	// backend that actually answered — the web_search and fetch_page tools show
	// it, which is how "did Tavily answer, or did the native chain rescue the
	// call?" stopped being a guess.
	Provider string `json:"provider,omitempty"`
}

// PageContent is the extracted text of a single fetched page.
type PageContent struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
	// Provider carries the same attribution as SearchResult.Provider.
	Provider string `json:"provider,omitempty"`
}

// Backend is the web-search capability behind the web_search and fetch_page
// tools and the deep-research pipeline. Implementations are NativeBackend (the
// HTTP engine chain), TavilyBackend, and the platform browser backends —
// SafariBackend on macOS, ChromiumBackend on Windows — composed into chains
// with SwitchableBackend and NewFallbackBackend; the interface is what the
// tools and the pipeline depend on, and what a test substitutes a fake for to
// stay off the network.
type Backend interface {
	// Name reports the provider name this backend stamps on its answers. A
	// fallback chain reports its primary's name: which backend answered a given
	// call travels on the results themselves, because a chain has no single
	// answer of its own.
	Name() string
	// Search returns up to limit results for query, best first.
	Search(ctx context.Context, query string, limit int) ([]SearchResult, error)
	// FetchPage returns the readable text content of a single URL.
	FetchPage(ctx context.Context, url string) (PageContent, error)
}

var _ Backend = (*NativeBackend)(nil)

// Provider names, shared by the backends' Name methods and the stamps they
// leave on results and pages.
const (
	ProviderTavily   = "tavily"
	ProviderNative   = "native"
	ProviderSafari   = "safari"
	ProviderChromium = "chromium"
)

// withProvider fills in the provider stamp on any result that lacks one. It
// never overwrites a stamp that is already present, so running it over results
// that arrived stamped (from a cache, say) is a no-op — which is also what
// makes it safe on a slice another goroutine may already be reading. Leaf
// backends call it once, where the results are built, before publishing them
// to a cache.
func withProvider(results []SearchResult, name string) []SearchResult {
	for i := range results {
		if results[i].Provider == "" {
			results[i].Provider = name
		}
	}
	return results
}

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

// Name reports the primary's name — the provider this chain is wired as. Which
// backend answered an individual call is carried on the results themselves,
// since this wrapper has no single answer of its own.
func (f *fallbackBackend) Name() string { return f.primary.Name() }

// Close releases resources held by either half of the fallback chain.
func (f *fallbackBackend) Close() error {
	err1 := closeBackend(f.primary)
	err2 := closeBackend(f.secondary)
	if err1 != nil {
		return err1
	}
	return err2
}

// closeBackend releases any resources held by b (such as temporary browser
// profile directories) if b implements io.Closer.
func closeBackend(b Backend) error {
	if c, ok := b.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// isBrowserUnavailable reports whether err means a browser backend is not
// installed or cannot be driven on this machine at all.
func isBrowserUnavailable(err error) bool {
	return errors.Is(err, errChromiumUnavailable) || errors.Is(err, errSafariUnavailable)
}

// fallbackLabel builds the attribution for an answer that came from the
// secondary: the leaf backend that actually produced it, marked as having
// answered after the primary failed. The leaf is read from the stamp the
// secondary's own chain left on the data — the secondary may itself be a
// chain, and Safari answering under Tavily should read "safari (tavily
// fallback)", not stack a second parenthetical. A secondary that stamps
// nothing falls back to its Name.
func (f *fallbackBackend) fallbackLabel(stamped string) string {
	leaf := stamped
	if i := strings.IndexAny(leaf, " ("); i >= 0 {
		leaf = leaf[:i]
	}
	if leaf == "" {
		leaf = f.secondary.Name()
	}
	return leaf + " (" + f.primary.Name() + " fallback)"
}

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
	slog.Info("web search: primary produced nothing, trying its fallback",
		"primary", f.primary.Name(), "secondary", f.secondary.Name(), "query", query, "err", err)

	fallbackResults, fallbackErr := f.secondary.Search(ctx, query, limit)
	if fallbackErr == nil && len(fallbackResults) > 0 {
		// When the primary is a browser that simply isn't installed on this
		// machine (e.g. Chromium on a Safari-only Mac) and Safari answers as
		// the available browser in NewBrowserBackend(), preserve Safari's own
		// leaf stamp so an outer fallback (or direct browser mode) reads
		// "safari (native fallback)" or "safari" rather than
		// "safari (chromium fallback)".
		if !(isBrowserUnavailable(err) && f.secondary.Name() == ProviderSafari) {
			stamp := f.fallbackLabel(fallbackResults[0].Provider)
			for i := range fallbackResults {
				fallbackResults[i].Provider = stamp
			}
		}
		return fallbackResults, nil
	}
	// If the primary browser was not installed at all, surface the secondary's
	// error rather than the missing-primary sentinel.
	if isBrowserUnavailable(err) && fallbackErr != nil {
		return nil, fallbackErr
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
	slog.Info("web search: primary fetch failed, trying its fallback",
		"primary", f.primary.Name(), "secondary", f.secondary.Name(), "url", url, "err", err)

	fallbackPage, fallbackErr := f.secondary.FetchPage(ctx, url)
	if fallbackErr != nil {
		// When the primary is a browser that is not installed on this machine
		// (e.g. Chromium in macOS's Chromium -> Safari browser chain), return
		// the secondary's error directly so an outer fallback wraps one clean
		// browser failure instead of a nested "chromium: ... (fallback: safari: ...)".
		if isBrowserUnavailable(err) {
			return PageContent{}, fallbackErr
		}
		// The primary's error describes the ordinary path and is the more
		// useful of the two; the fallback's is kept alongside it because "both
		// refused" and "the fallback could not run" are different problems.
		return PageContent{}, fmt.Errorf("%w (fallback: %v)", err, fallbackErr)
	}
	if !(isBrowserUnavailable(err) && f.secondary.Name() == ProviderSafari) {
		fallbackPage.Provider = f.fallbackLabel(fallbackPage.Provider)
	}
	return fallbackPage, nil
}
