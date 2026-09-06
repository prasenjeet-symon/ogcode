package search

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	readability "github.com/go-shiori/go-readability"
)

// NativeBackend is ogcode's web-search backend: plain HTTP against JS-free
// search endpoints, plus Readability text extraction. It is compiled into the
// binary and needs no Node.js, no npm packages and no Chromium download.
//
// It cannot read pages that only exist after JavaScript runs, nor hosts that
// bot-block plain HTTP clients (Cloudflare interstitials). Those fetches fail
// cleanly: the deep-research pipeline drops them and synthesises from the pages
// it did get.
type NativeBackend struct {
	http *http.Client
	// persona is the browser this backend impersonates for its whole lifetime:
	// one TLS fingerprint, one User-Agent, one coherent header set. See
	// persona.go for why it is fixed rather than rotated per request.
	persona persona
	// sem caps simultaneous outbound requests. The cap is politeness: a burst of
	// parallel fetches from one IP is what gets a client rate-limited.
	sem         chan struct{}
	searchCache *ttlCache[[]SearchResult]
	fetchCache  *ttlCache[PageContent]
	// referers remembers which search page produced a given result URL, so a
	// later fetch of it carries the Referer a click would have carried. A
	// result URL arriving with no referrer at all is a small inconsistency, and
	// small inconsistencies are what the cheap heuristics look for.
	referers *ttlCache[string]
	// engineWait spaces requests per search host. Separate from sem, which
	// bounds total concurrency across every host at once.
	engineWait *hostLimiter
	// health demotes engines that have recently refused us, so a blocked engine
	// stops costing every later search its first attempt.
	health *engineHealth
	// engines is per-instance rather than package-level so an optional,
	// user-supplied endpoint can join the chain at construction.
	engines []searchEngine
}

const (
	// nativeCacheTTL is 10 minutes: results rarely change minute-to-minute, and
	// one research run re-queries the same terms often.
	nativeCacheTTL = 10 * time.Minute

	nativeMaxConcurrency = 8
	nativeSearchTimeout  = 15 * time.Second
	nativeFetchTimeout   = 15 * time.Second

	// nativeMaxAttempts is the total number of tries per request, so two
	// retries. A search endpoint that rate-limits usually clears within a
	// second or two; one that is blocking outright will not clear at all, and
	// spending more attempts on it only delays the fallback to the next engine.
	nativeMaxAttempts = 3

	// nativeRetryBase is the first backoff step, doubled per attempt and
	// jittered. Capped by nativeRetryMax so a retry can never outlast the
	// per-attempt timeout by much.
	nativeRetryBase = 600 * time.Millisecond
	nativeRetryMax  = 4 * time.Second

	// nativeRetryAfterCap bounds how long a server-supplied Retry-After will be
	// honoured. Engines sometimes ask for a minute; a research turn cannot stall
	// that long, so anything above this is treated as "not now" and the chain
	// falls through to the next engine instead.
	nativeRetryAfterCap = 8 * time.Second

	// nativeEngineInterval is the minimum spacing between two requests to the
	// same search endpoint. Fetches are deliberately not spaced — they hit many
	// different hosts and parallelism there is the point — but a deep-research
	// run issues several sub-queries to one engine back to back, and that burst
	// is what trips rate limiting.
	nativeEngineInterval = 700 * time.Millisecond

	// nativeEngineJitter is added to that spacing at random. Evenly spaced
	// requests are their own signal: a human reading results produces gaps that
	// vary by seconds, and a client that queries every 700ms to the millisecond
	// is describing itself even when every other layer looks like a browser.
	// The range this produces (0.7s–2.5s) also happens to be a plausible
	// read-and-refine interval.
	nativeEngineJitter = 1800 * time.Millisecond

	// nativeBlockCooldown is how long an engine that answered 403 (or otherwise
	// refused us outright) is tried last. Long enough to stop paying for it on
	// every search in a research run, short enough that a transient block
	// clears well inside a working session.
	nativeBlockCooldown = 10 * time.Minute

	// nativeSoftCooldown is the demotion for a softer failure — a timeout, or a
	// page that parsed to nothing. Those are often the query's fault rather
	// than the engine's, so the penalty is brief.
	nativeSoftCooldown = 90 * time.Second

	// nativeMaxBody bounds how much of a response we will read. Some pages ship
	// megabytes of inlined JSON payload; reading it all wastes memory for text
	// we then throw away.
	nativeMaxBody = 8 << 20

	// nativePageChars is the per-page truncation that the fetch_page tool reports
	// as "[content truncated at 14,000 characters]" — keep the two in step.
	nativePageChars = 14000

	// readabilityMinChars is the length below which Readability's output is
	// treated as suspect. Readability optimises for article-shaped documents; on
	// app shells and consent interstitials it can lock onto a tiny wrapper (a
	// cookie banner) and discard the page. Below this bar we cross-check it
	// against a structural extraction and keep whichever found more.
	readabilityMinChars = 600

	snippetMaxChars = 300
)

// NewNativeBackend builds a native backend with its own connection pool.
func NewNativeBackend() *NativeBackend {
	p := pickPersona()
	if name := os.Getenv(personaEnv); name != "" {
		if chosen, ok := personaByName(name); ok {
			p = chosen
		} else {
			slog.Warn("web search: unknown "+personaEnv+", using a random persona", "value", name)
		}
	}
	slog.Debug("web search: native transport persona", "persona", p.name)

	// A cookie jar because browsers have one. Search engines set a session
	// cookie on the first response and expect it back on the next; a client
	// that never returns one looks like a different visitor every time, which
	// is both suspicious in itself and the thing that makes consent
	// interstitials reappear on every request.
	//
	// A jar is also per-process state that a research run benefits from: the
	// consent cookie an engine sets is honoured for the rest of the session.
	jar, err := cookiejar.New(nil)
	if err != nil {
		// cookiejar.New only fails on a bad PublicSuffixList, and nil is valid,
		// so this is unreachable — but a nil jar silently disables cookies, and
		// that is worth a line rather than a surprise.
		slog.Warn("web search: cookie jar unavailable, continuing without cookies", "err", err)
	}

	return &NativeBackend{
		persona: p,
		http: &http.Client{
			Transport: newBrowserTransport(p),
			Jar:       jar,
			// Belt-and-braces behind the per-request context deadlines.
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return fmt.Errorf("stopped after 10 redirects")
				}
				// net/http carries the original headers across a redirect but
				// not the browser's notion of what the redirect means. A
				// browser re-derives Sec-Fetch-Site against the new URL, and a
				// cross-origin hop that still claims "none" is a contradiction.
				if len(via) > 0 && req.Header.Get("sec-fetch-site") != "" {
					req.Header.Set("sec-fetch-site", secFetchSite(req.URL, via[len(via)-1].URL.String()))
				}
				return nil
			},
		},
		sem:         make(chan struct{}, nativeMaxConcurrency),
		searchCache: newTTLCache[[]SearchResult](nativeCacheTTL),
		fetchCache:  newTTLCache[PageContent](nativeCacheTTL),
		referers:    newTTLCache[string](nativeCacheTTL),
		engineWait:  newHostLimiter(nativeEngineInterval, nativeEngineJitter),
		health:      newEngineHealth(),
		engines:     buildEngines(os.Getenv(searxngEnv)),
	}
}

// personaEnv pins the impersonated browser, for debugging a host that behaves
// differently for one of them.
//
//	OGCODE_SEARCH_PERSONA=chrome-133-macos
const personaEnv = "OGCODE_SEARCH_PERSONA"

// searxngEnv names a SearxNG instance to query before the built-in engines.
//
// Every public engine that answers a plain HTTP client either blocks or serves
// junk — DuckDuckGo's html and lite endpoints return a 202 interstitial,
// Startpage and Mojeek serve a shell with no results, Ecosia answers 403. So
// the third engine cannot be another scraper picked for everyone; it has to be
// an endpoint the user controls. A SearxNG instance (self-hosted, or any that
// leaves the JSON API on) is exactly that, and it returns structured JSON
// rather than markup that changes between deploys.
//
//	OGCODE_SEARXNG_URL=https://searx.example.org
const searxngEnv = "OGCODE_SEARXNG_URL"

// buildEngines assembles the chain, best first. A configured SearxNG goes ahead
// of the scrapers: it is the user's own instance, so it neither rate-limits
// them nor breaks when a vendor reskins its results page.
func buildEngines(searxngURL string) []searchEngine {
	var engines []searchEngine
	if u := strings.TrimRight(strings.TrimSpace(searxngURL), "/"); u != "" {
		engines = append(engines, searchEngine{
			name: "searxng",
			fn:   searxngEngine(u),
			page: func(query string, _ int) string { return u + "/search?q=" + url.QueryEscape(query) },
		})
	}
	return append(engines,
		searchEngine{name: "duckduckgo", fn: (*NativeBackend).searchDuckDuckGo, page: func(q string, _ int) string { return duckDuckGoSearchURL(q) }},
		searchEngine{name: "brave", fn: (*NativeBackend).searchBrave, page: func(q string, _ int) string { return braveSearchURL(q) }},
		searchEngine{name: "bing", fn: (*NativeBackend).searchBing, page: func(q string, _ int) string { return bingSearchURL(q) }},
		searchEngine{name: "yahoo", fn: (*NativeBackend).searchYahoo, page: yahooSearchURL},
	)
}

// searchEngine is one JS-free search endpoint. They are tried in order and the
// first to return results wins.
type searchEngine struct {
	name string
	fn   func(*NativeBackend, context.Context, string, int) ([]SearchResult, error)
	// page builds the URL of the results page itself. It is not used to make
	// the request — fn does that — but to know what Referer a click on one of
	// these results would have carried.
	page func(query string, limit int) string
}

// searchURL is the results page this engine would have shown for the query.
func (e searchEngine) searchURL(query string, limit int) string {
	if e.page == nil {
		return ""
	}
	return e.page(query, limit)
}

// The engine chain is built per backend in buildEngines.
//
// This list used to be much shorter, and the reason it grew is worth recording,
// because it is the clearest evidence for what the transport layer buys.
// DuckDuckGo and Bing were both struck off after testing: DuckDuckGo answered a
// plain Go client with a 202 interstitial, and Bing answered it with ten
// well-formed organic results for unrelated spam domains — a parse that looks
// healthy while feeding the pipeline garbage. Neither was a parser problem.
// Once the client presented a browser's TLS fingerprint (see transport.go) both
// began answering the query properly, from the same code, with no change to the
// selectors. What looked like two hostile engines was one hostile handshake.
//
// Still absent, re-verified with the browser fingerprint in place:
//   - Google, Startpage — serve a JavaScript shell with no results in the
//     markup. The document arrives, so nothing errors; there is simply nothing
//     to parse. These need a real browser, which is what the Safari backend is.
//   - Ecosia — answers 403 regardless.
//   - Mojeek — answers, but with a CAPTCHA rather than results.

// Name reports the provider name stamped on this backend's results.
func (n *NativeBackend) Name() string { return ProviderNative }

// Search queries each engine in turn and returns the first non-empty result set.
func (n *NativeBackend) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 8
	}
	cacheKey := query + "\x00" + strconv.Itoa(limit)
	if v, ok := n.searchCache.get(cacheKey); ok {
		return v, nil
	}

	if err := n.acquire(ctx); err != nil {
		return nil, err
	}
	defer n.release()
	// Re-check: a concurrent caller may have filled the cache while we queued.
	if v, ok := n.searchCache.get(cacheKey); ok {
		return v, nil
	}

	var failures []string
	for _, e := range n.health.order(n.engines) {
		results, err := e.fn(n, ctx, query, limit)
		if err != nil {
			slog.Warn("native search: engine failed", "engine", e.name, "err", err)
			failures = append(failures, e.name+": "+err.Error())
			n.health.penalise(e.name, cooldownFor(err))
			continue
		}
		if len(results) == 0 {
			// An engine that answers 200 with nothing a parser recognises is
			// usually not reporting "no such page" — it is serving a challenge,
			// a consent wall, or markup that has moved on since the selectors
			// were written. Treated as a soft failure so the next search starts
			// somewhere more promising.
			slog.Info("native search: engine returned no results", "engine", e.name, "query", query)
			n.health.penalise(e.name, nativeSoftCooldown)
			continue
		}
		n.health.recover(e.name)
		// Stamp before the results go into the cache: everything handed out
		// from here on — this call and every cache hit — must carry the
		// attribution, and the cache is shared between readers.
		withProvider(results, ProviderNative)
		n.rememberReferer(results, e.searchURL(query, limit))
		n.searchCache.set(cacheKey, results)
		return results, nil
	}

	// Every engine erroring is a different condition from every engine honestly
	// finding nothing, and the caller renders them differently.
	if len(failures) == len(n.engines) {
		return nil, fmt.Errorf("all search engines failed (%s)", strings.Join(failures, "; "))
	}
	return nil, nil
}

// FetchPage retrieves a URL over plain HTTP and extracts its readable text.
func (n *NativeBackend) FetchPage(ctx context.Context, rawURL string) (PageContent, error) {
	if v, ok := n.fetchCache.get(rawURL); ok {
		return v, nil
	}
	if err := n.acquire(ctx); err != nil {
		return PageContent{}, err
	}
	defer n.release()
	if v, ok := n.fetchCache.get(rawURL); ok {
		return v, nil
	}

	// A page we chose off a results list is reached by clicking it, so it goes
	// out with that results page as its Referer. Anything else — a URL the user
	// or the model supplied directly — is a typed navigation and correctly has
	// none.
	nav := navigation{}
	if ref, ok := n.referers.get(dedupKey(rawURL)); ok {
		nav.referer = ref
	}

	body, finalURL, contentType, err := n.get(ctx, rawURL, nativeFetchTimeout, nav)
	if err != nil {
		return PageContent{}, err
	}
	if !isTextualContentType(contentType) {
		return PageContent{}, fmt.Errorf("fetch %s: unsupported content type %q", rawURL, contentType)
	}

	var title, text string
	if isHTMLContentType(contentType) {
		title, text = extractReadable(body, finalURL)
	} else {
		text = normalizeWhitespace(string(body))
	}
	if text == "" {
		return PageContent{}, fmt.Errorf("fetch %s: no readable content extracted", rawURL)
	}

	text, truncated := truncateChars(text, nativePageChars)
	// Report the requested URL, not the post-redirect one, so the caller's
	// dedup keys and citations line up with the URL it asked for.
	page := PageContent{URL: rawURL, Title: title, Text: text, Truncated: truncated, Provider: ProviderNative}
	n.fetchCache.set(rawURL, page)
	return page, nil
}

// ── engines ─────────────────────────────────────────────────────────────────

// searchGet is get() with per-host spacing applied first. Only the search
// endpoints go through it; page fetches stay unspaced because they hit many
// different hosts and their parallelism is the point.
func (n *NativeBackend) searchGet(ctx context.Context, endpoint string) ([]byte, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if err := n.engineWait.wait(ctx, u.Host); err != nil {
		return nil, err
	}
	// No Referer: a search is a typed navigation, which is exactly what an
	// empty referrer and Sec-Fetch-Site: none describe.
	body, _, _, err := n.get(ctx, endpoint, nativeSearchTimeout, navigation{})
	return body, err
}

// searchDoc is searchGet for the engines that are parsed as markup.
func (n *NativeBackend) searchDoc(ctx context.Context, endpoint string) (*goquery.Document, error) {
	body, err := n.searchGet(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	return goquery.NewDocumentFromReader(bytes.NewReader(body))
}

// searxngEngine queries a SearxNG instance's JSON API. Unlike the scrapers it
// parses a documented response rather than markup, so it does not break when a
// vendor reskins its results page.
//
// The instance must have `search.formats` include `json` — it is off by default
// on many public instances, which is why this is opt-in by URL rather than a
// public endpoint baked in.
func searxngEngine(baseURL string) func(*NativeBackend, context.Context, string, int) ([]SearchResult, error) {
	return func(n *NativeBackend, ctx context.Context, query string, limit int) ([]SearchResult, error) {
		endpoint := baseURL + "/search?format=json&q=" + url.QueryEscape(query)
		body, err := n.searchGet(ctx, endpoint)
		if err != nil {
			return nil, err
		}
		return parseSearxngResults(body, limit)
	}
}

// parseSearxngResults is split out so the shape can be pinned in a test without
// standing up an instance.
func parseSearxngResults(body []byte, limit int) ([]SearchResult, error) {
	var payload struct {
		Results []struct {
			URL     string `json:"url"`
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		// A JSON-disabled instance answers with the HTML page, which is the
		// single most likely misconfiguration — say so rather than reporting a
		// raw unmarshal error against a wall of markup.
		return nil, fmt.Errorf("searxng: response was not JSON (does the instance enable the json format?): %w", err)
	}
	var out []SearchResult
	seen := make(map[string]bool)
	for _, r := range payload.Results {
		if !strings.HasPrefix(r.URL, "http://") && !strings.HasPrefix(r.URL, "https://") {
			continue
		}
		key := dedupKey(r.URL)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, SearchResult{
			Title:   normalizeWhitespace(r.Title),
			URL:     r.URL,
			Snippet: clipChars(normalizeWhitespace(r.Content), snippetMaxChars),
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// The *SearchURL builders are split out from the engine methods because the
// Safari backend drives the same engines through a browser and must hit
// byte-identical URLs — a second copy would drift the moment one of them gained
// a parameter. They are also what rememberReferer records as the page a result
// was clicked from.
func braveSearchURL(query string) string {
	return "https://search.brave.com/search?q=" + url.QueryEscape(query)
}

// duckDuckGoSearchURL targets the html endpoint rather than duckduckgo.com
// itself: the main site renders results client-side, while this one has always
// served them in the markup. It only became usable once the handshake stopped
// giving us away — before that it answered with a 202 interstitial.
func duckDuckGoSearchURL(query string) string {
	return "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
}

func bingSearchURL(query string) string {
	return "https://www.bing.com/search?q=" + url.QueryEscape(query)
}

// googleSearchURL is only ever driven by the Safari backend. Google serves a
// shell and builds its results in the page, so there is nothing here for an
// HTTP client to parse however convincing its handshake is — which is why it
// stays out of the native chain and leads the browser one.
func googleSearchURL(query string) string {
	return "https://www.google.com/search?q=" + url.QueryEscape(query)
}

// parseGoogleResults reads Google's rendered result list.
//
// Google's class names are generated and rotate, so anchoring on them would be
// a standing maintenance cost. The selector here is structural instead: a
// result is an anchor that contains an h3. That relationship is not a styling
// choice — it is how the page states "this heading is a link to this document"
// — and it has outlived every redesign. Class names appear only as a hint for
// finding the snippet, where being wrong costs a description rather than a
// result.
//
// It lives here rather than in safari.go so it is testable against a fixture on
// any platform; only its caller is macOS-only.
func parseGoogleResults(doc *goquery.Document, limit int) []SearchResult {
	var out []SearchResult
	seen := make(map[string]bool)
	doc.Find("a[href]").EachWithBreak(func(_ int, anchor *goquery.Selection) bool {
		heading := anchor.Find("h3").First()
		if heading.Length() == 0 {
			return true
		}
		href, ok := anchor.Attr("href")
		if !ok {
			return true
		}
		href = resolveGoogleURL(href)
		if !isUsableResultURL(href, "google.com") {
			return true
		}
		key := dedupKey(href)
		if seen[key] {
			return true
		}
		seen[key] = true

		out = append(out, SearchResult{
			Title:   normalizeWhitespace(heading.Text()),
			URL:     href,
			Snippet: clipChars(googleSnippet(anchor, heading), snippetMaxChars),
		})
		return len(out) < limit
	})
	return out
}

// googleSnippet finds the description belonging to a result anchor.
func googleSnippet(anchor, heading *goquery.Selection) string {
	block := anchor.Closest("div.MjjYud, div.tF2Cxc, div.g")
	if block.Length() == 0 {
		return ""
	}
	if text := normalizeWhitespace(block.Find("div[data-sncf], .VwiC3b").First().Text()); text != "" {
		return text
	}
	// The snippet classes have moved on. The block still holds the description
	// somewhere, and it reads better with the title removed from the front than
	// not at all.
	full := normalizeWhitespace(block.Text())
	return strings.TrimSpace(strings.TrimPrefix(full, normalizeWhitespace(heading.Text())))
}

// resolveGoogleURL unwraps the /url?q= redirect Google uses in some contexts.
// Rendered results usually carry the destination directly, so this is a
// safeguard rather than the common path.
func resolveGoogleURL(href string) string {
	if !strings.HasPrefix(href, "/url?") {
		return href
	}
	u, err := url.Parse("https://www.google.com" + href)
	if err != nil {
		return href
	}
	if target := u.Query().Get("q"); strings.HasPrefix(target, "http") {
		return target
	}
	return href
}

// searchDuckDuckGo parses the html endpoint's server-rendered result list.
//
// It is first in the chain: it returns direct, on-topic results in the smallest
// page of any engine here (tens of kilobytes against Brave's hundreds), and its
// markup has been stable for years — the class names below long predate the
// current site.
func (n *NativeBackend) searchDuckDuckGo(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	doc, err := n.searchDoc(ctx, duckDuckGoSearchURL(query))
	if err != nil {
		return nil, err
	}
	return parseDuckDuckGoResults(doc, limit), nil
}

// parseDuckDuckGoResults is split out from searchDuckDuckGo so the selectors
// can be pinned against a recorded response without going to the network.
func parseDuckDuckGoResults(doc *goquery.Document, limit int) []SearchResult {
	var out []SearchResult
	seen := make(map[string]bool)
	doc.Find("div.result, div.web-result").EachWithBreak(func(_ int, block *goquery.Selection) bool {
		// Ads use the same container but a different anchor class, so keying on
		// result__a rather than the first link filters them out for free.
		anchor := block.Find("a.result__a").First()
		href, ok := anchor.Attr("href")
		if !ok {
			return true
		}
		href = resolveDuckDuckGoURL(href)
		if !isUsableResultURL(href, "duckduckgo.com") {
			return true
		}
		key := dedupKey(href)
		if seen[key] {
			return true
		}
		seen[key] = true

		out = append(out, SearchResult{
			Title:   normalizeWhitespace(anchor.Text()),
			URL:     href,
			Snippet: clipChars(normalizeWhitespace(block.Find("a.result__snippet, .result__snippet").First().Text()), snippetMaxChars),
		})
		return len(out) < limit
	})
	return out
}

// resolveDuckDuckGoURL unwraps the /l/ redirector, which carries the real
// target percent-encoded in a uddg parameter. The href is protocol-relative, so
// it has to be given a scheme before it will parse.
func resolveDuckDuckGoURL(href string) string {
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	if !strings.Contains(href, "duckduckgo.com/l/") {
		return href
	}
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	if target := u.Query().Get("uddg"); strings.HasPrefix(target, "http") {
		return target
	}
	return href
}

// searchBing parses Bing's server-rendered result list.
func (n *NativeBackend) searchBing(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	doc, err := n.searchDoc(ctx, bingSearchURL(query))
	if err != nil {
		return nil, err
	}
	return parseBingResults(doc, limit), nil
}

// parseBingResults is split out from searchBing for the same reason as the
// others.
func parseBingResults(doc *goquery.Document, limit int) []SearchResult {
	var out []SearchResult
	seen := make(map[string]bool)
	doc.Find("li.b_algo").EachWithBreak(func(_ int, block *goquery.Selection) bool {
		anchor := block.Find("h2 a[href]").First()
		href, ok := anchor.Attr("href")
		if !ok {
			return true
		}
		href = resolveBingURL(href)
		if !isUsableResultURL(href, "bing.com") {
			return true
		}
		key := dedupKey(href)
		if seen[key] {
			return true
		}
		seen[key] = true

		out = append(out, SearchResult{
			Title:   normalizeWhitespace(anchor.Text()),
			URL:     href,
			Snippet: clipChars(normalizeWhitespace(block.Find(".b_caption p, .b_algoSlug, p").First().Text()), snippetMaxChars),
		})
		return len(out) < limit
	})
	return out
}

// resolveBingURL unwraps the bing.com/ck/a click tracker, which carries the
// target base64url-encoded in a "u" parameter behind a two-character "a1" tag.
//
// Unwrapping rather than following the redirect is deliberate: the alternative
// is an extra request per result, to a URL we are not going to fetch unless the
// ranker picks it.
func resolveBingURL(href string) string {
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	host := strings.ToLower(u.Host)
	if host != "www.bing.com" && host != "bing.com" {
		return href
	}
	enc := u.Query().Get("u")
	if !strings.HasPrefix(enc, "a1") {
		return href
	}
	// Bing strips the padding; base64url decoding needs it back.
	raw := enc[2:]
	if pad := len(raw) % 4; pad != 0 {
		raw += strings.Repeat("=", 4-pad)
	}
	decoded, err := base64.URLEncoding.DecodeString(raw)
	if err != nil || !strings.HasPrefix(string(decoded), "http") {
		return href
	}
	return string(decoded)
}

func yahooSearchURL(query string, limit int) string {
	return "https://search.yahoo.com/search?p=" + url.QueryEscape(query) + "&n=" + strconv.Itoa(limit)
}

// searchBrave parses Brave Search's server-rendered result list. Brave emits
// direct result URLs (no redirect wrapper), so no URL decoding is needed. Its
// class names are Svelte-hashed and change between deploys, so selectors anchor
// on the stable data-type attribute and semantic class stems only.
func (n *NativeBackend) searchBrave(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	doc, err := n.searchDoc(ctx, braveSearchURL(query))
	if err != nil {
		return nil, err
	}
	return parseBraveResults(doc, limit), nil
}

// parseBraveResults is split out from searchBrave so the selectors can be
// pinned against a recorded response without going to the network.
func parseBraveResults(doc *goquery.Document, limit int) []SearchResult {
	var out []SearchResult
	seen := make(map[string]bool)
	doc.Find(`div.snippet[data-type="web"]`).EachWithBreak(func(_ int, block *goquery.Selection) bool {
		href, ok := block.Find(`a[href]`).First().Attr("href")
		if !ok || !isUsableResultURL(href, "brave.com") {
			return true
		}
		key := dedupKey(href)
		if seen[key] {
			return true
		}
		seen[key] = true

		titleEl := block.Find(".title").First()
		// Brave mirrors the full untruncated title into the title attribute,
		// while the text node is CSS line-clamped.
		title, _ := titleEl.Attr("title")
		if strings.TrimSpace(title) == "" {
			title = titleEl.Text()
		}
		out = append(out, SearchResult{
			Title:   normalizeWhitespace(title),
			URL:     href,
			Snippet: clipChars(normalizeWhitespace(block.Find(".snippet-content, .generic-snippet .content").First().Text()), snippetMaxChars),
		})
		return len(out) < limit
	})
	return out
}

// searchYahoo parses Yahoo's server-rendered result list. Yahoo is the fallback
// because, unlike every other engine that answers a plain HTTP client, it
// returns results that are actually on-topic.
func (n *NativeBackend) searchYahoo(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	doc, err := n.searchDoc(ctx, yahooSearchURL(query, limit))
	if err != nil {
		return nil, err
	}
	return parseYahooResults(doc, limit), nil
}

// parseYahooResults is split out from searchYahoo for the same reason as
// parseBraveResults.
func parseYahooResults(doc *goquery.Document, limit int) []SearchResult {
	var out []SearchResult
	seen := make(map[string]bool)
	doc.Find("div.algo").EachWithBreak(func(_ int, block *goquery.Selection) bool {
		href, ok := block.Find(`a[href]`).First().Attr("href")
		if !ok {
			return true
		}
		href = resolveYahooURL(href)
		if !isUsableResultURL(href, "yahoo.com") {
			return true
		}
		key := dedupKey(href)
		if seen[key] {
			return true
		}
		seen[key] = true

		out = append(out, SearchResult{
			Title:   normalizeWhitespace(block.Find("h3.title").First().Text()),
			URL:     href,
			Snippet: clipChars(normalizeWhitespace(block.Find(".compText p").First().Text()), snippetMaxChars),
		})
		return len(out) < limit
	})
	return out
}

// yahooRedirectRe matches the destination in Yahoo's click-tracking wrapper,
// which carries the real URL percent-encoded in an /RU=.../ path segment.
var yahooRedirectRe = regexp.MustCompile(`/RU=([^/]+)/`)

// resolveYahooURL unwraps r.search.yahoo.com click-tracking links.
func resolveYahooURL(href string) string {
	if !strings.Contains(href, "search.yahoo.com") {
		return href
	}
	m := yahooRedirectRe.FindStringSubmatch(href)
	if m == nil {
		return href
	}
	// PathUnescape, not QueryUnescape: the encoded value is a path segment, and
	// QueryUnescape would turn a literal "+" in the target URL into a space.
	decoded, err := url.PathUnescape(m[1])
	if err != nil || !strings.HasPrefix(decoded, "http") {
		return href
	}
	return decoded
}

// ── extraction ──────────────────────────────────────────────────────────────

// extractReadable pulls the title and readable body text out of an HTML page.
func extractReadable(body []byte, pageURL string) (string, string) {
	doc, docErr := goquery.NewDocumentFromReader(bytes.NewReader(body))
	docTitle := ""
	if docErr == nil {
		docTitle = normalizeWhitespace(doc.Find("title").First().Text())
	}

	var readTitle, readText string
	parsed, _ := url.Parse(pageURL)
	if article, err := readability.FromReader(bytes.NewReader(body), parsed); err == nil {
		readTitle = normalizeWhitespace(article.Title)
		readText = normalizeWhitespace(article.TextContent)
	}

	title := readTitle
	if title == "" {
		title = docTitle
	}

	if len(readText) >= readabilityMinChars || docErr != nil {
		return title, readText
	}
	// Readability came back implausibly short — cross-check it structurally.
	if structural := structuralText(doc); len(structural) > len(readText) {
		return title, structural
	}
	return title, readText
}

// chromeSelector matches page furniture that is never the content: navigation,
// boilerplate, and the consent dialogs that Readability is most prone to
// mistake for the article on a JavaScript-rendered page.
const chromeSelector = `script, style, noscript, nav, footer, header, aside, iframe, svg, form,
	[role="banner"], [role="navigation"], [role="contentinfo"], [role="dialog"], [role="alertdialog"],
	[aria-hidden="true"],
	[data-testid*="consent" i], [id*="consent" i], [class*="consent" i],
	[id*="cookie-banner" i], [class*="cookie-banner" i],
	[id*="cookie-notice" i], [class*="cookie-notice" i]`

// contentContainers are tried most-focused first; each trades noise for the
// risk of missing content that sits outside it.
var contentContainers = []string{"main", "article", `[role="main"]`, "#main", ".main-content"}

// structuralText strips chrome and returns the text of the most focused
// container that actually holds the page's content. It mutates doc, which is
// safe because the caller is finished with it.
func structuralText(doc *goquery.Document) string {
	doc.Find(chromeSelector).Remove()

	bodyText := normalizeWhitespace(doc.Find("body").Text())
	if bodyText == "" {
		return ""
	}

	// Prefer a focused container over the whole body, but only when it carries
	// the bulk of the text. An app shell can ship a <main> holding nothing but a
	// loading skeleton while the server-rendered content sits elsewhere in the
	// body — taking the first container that clears a flat length bar would
	// return "LoadingLoadingLoading" and discard the page.
	minShare := len(bodyText) * 6 / 10
	for _, sel := range contentContainers {
		text := normalizeWhitespace(doc.Find(sel).First().Text())
		if len(text) >= readabilityMinChars && len(text) >= minShare {
			return text
		}
	}
	return bodyText
}

// ── HTTP ────────────────────────────────────────────────────────────────────

// get performs a bounded GET and returns the body, the post-redirect URL and
// the content type.
// get fetches a URL, retrying the failures that are worth retrying.
//
// Rate limiting used to be fatal here: any non-2xx became an immediate hard
// error, so a single 429 took the engine out for that call, and with both
// engines limited the whole research run reported "all search engines failed".
// A 429 or a 5xx is a "later", not a "no" — the retry treats it as one, and
// honours the server's own Retry-After when it supplies a usable value.
//
// `timeout` bounds each attempt, not the sequence; the caller's context bounds
// the whole thing.
func (n *NativeBackend) get(ctx context.Context, rawURL string, timeout time.Duration, nav navigation) ([]byte, string, string, error) {
	var lastErr error
	for attempt := 0; attempt < nativeMaxAttempts; attempt++ {
		if attempt > 0 {
			if err := sleepCtx(ctx, lastErr.(*httpError).retryDelay(attempt)); err != nil {
				return nil, "", "", err
			}
		}
		body, finalURL, contentType, err := n.getOnce(ctx, rawURL, timeout, nav)
		if err == nil {
			return body, finalURL, contentType, nil
		}
		he, ok := err.(*httpError)
		if !ok || !he.retryable {
			return nil, "", "", err
		}
		lastErr = he
		slog.Debug("native search: retrying", "url", rawURL, "attempt", attempt+1, "err", he)
	}
	return nil, "", "", lastErr
}

// httpError carries the retry decision alongside the message, so get() does not
// have to re-parse a string to know whether another attempt is worth making.
type httpError struct {
	url        string
	status     int
	retryable  bool
	retryAfter time.Duration
	err        error
}

func (e *httpError) Error() string {
	if e.status != 0 {
		return fmt.Sprintf("fetch %s: http %d %s", e.url, e.status, http.StatusText(e.status))
	}
	return fmt.Sprintf("fetch %s: %v", e.url, e.err)
}

func (e *httpError) Unwrap() error { return e.err }

// retryDelay is the server's Retry-After when it gave a usable one, otherwise
// exponential backoff with jitter. Jitter matters because the pipeline fires
// several sub-queries at once: without it their retries would stay in lockstep
// and arrive together, which is the burst that caused the limit.
func (e *httpError) retryDelay(attempt int) time.Duration {
	if e.retryAfter > 0 {
		return e.retryAfter
	}
	d := nativeRetryBase << (attempt - 1)
	if d > nativeRetryMax {
		d = nativeRetryMax
	}
	return d + time.Duration(rand.Int64N(int64(d/2)))
}

// getOnce performs a single attempt.
func (n *NativeBackend) getOnce(ctx context.Context, rawURL string, timeout time.Duration, nav navigation) ([]byte, string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", "", fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	n.persona.applyHeaders(req, nav)

	resp, err := n.http.Do(req)
	if err != nil {
		// A transport error is worth one more try unless the caller gave up:
		// a dropped connection mid-burst is common and usually clears at once.
		return nil, "", "", &httpError{url: rawURL, err: err, retryable: ctx.Err() == nil}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, "", "", &httpError{
			url:        rawURL,
			status:     resp.StatusCode,
			retryable:  isRetryableStatus(resp.StatusCode),
			retryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, nativeMaxBody))
	if err != nil {
		return nil, "", "", fmt.Errorf("fetch %s: read body: %w", rawURL, err)
	}
	return body, resp.Request.URL.String(), resp.Header.Get("Content-Type"), nil
}

// isRetryableStatus reports whether waiting could plausibly change the answer.
// 403 is excluded on purpose: when an engine blocks a non-browser client it
// answers 403 every time, and retrying only delays the fallback.
func isRetryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests, // 429
		http.StatusBadGateway,         // 502
		http.StatusServiceUnavailable, // 503
		http.StatusGatewayTimeout:     // 504
		return true
	}
	return false
}

// parseRetryAfter reads both forms the header takes — delay-seconds and an
// HTTP-date. A value beyond nativeRetryAfterCap returns 0, which drops the
// request back to ordinary backoff and lets the chain move on rather than
// stalling the turn on one engine.
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	var d time.Duration
	if secs, err := strconv.Atoi(v); err == nil {
		d = time.Duration(secs) * time.Second
	} else if t, err := http.ParseTime(v); err == nil {
		d = time.Until(t)
	}
	if d <= 0 || d > nativeRetryAfterCap {
		return 0
	}
	return d
}

// sleepCtx waits, unless the caller gives up first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// hostLimiter spaces requests per host by handing each caller a reserved slot.
// Reserving up front (rather than sleeping until "last + interval") means N
// concurrent callers queue at N distinct times instead of all waking together
// and firing at once, which is the stampede the spacing exists to prevent.
type hostLimiter struct {
	mu       sync.Mutex
	next     map[string]time.Time
	interval time.Duration
	// jitter is the random amount added to each reservation. Spacing requests
	// at an exact interval is a machine signature in its own right, so the gap
	// between two searches is never twice the same length.
	jitter time.Duration
}

func newHostLimiter(interval, jitter time.Duration) *hostLimiter {
	return &hostLimiter{next: make(map[string]time.Time), interval: interval, jitter: jitter}
}

func (l *hostLimiter) wait(ctx context.Context, host string) error {
	l.mu.Lock()
	now := time.Now()
	at := l.next[host]
	if at.Before(now) {
		at = now
	}
	l.next[host] = at.Add(l.gap())
	l.mu.Unlock()
	return sleepCtx(ctx, time.Until(at))
}

func (l *hostLimiter) gap() time.Duration {
	if l.jitter <= 0 {
		return l.interval
	}
	return l.interval + time.Duration(rand.Int64N(int64(l.jitter)))
}

// ── engine health ───────────────────────────────────────────────────────────

// engineHealth remembers which engines have recently refused us.
//
// Without it, an engine that has decided to block this IP is still the first
// thing every search tries: a research run issuing several queries pays the
// same 403 (and its retries) once per query before falling through to an engine
// that works. Demotion is deliberately not exclusion — a blocked engine is
// still tried, just last — because the reason for a refusal is guesswork and
// the cost of being wrong about it should be a slower search, never no search.
type engineHealth struct {
	mu    sync.Mutex
	until map[string]time.Time
}

func newEngineHealth() *engineHealth {
	return &engineHealth{until: make(map[string]time.Time)}
}

func (h *engineHealth) penalise(name string, d time.Duration) {
	if d <= 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if at := time.Now().Add(d); at.After(h.until[name]) {
		h.until[name] = at
	}
}

// recover clears an engine's penalty. Called on a successful search, so one
// good answer restores an engine that was demoted for a transient reason.
func (h *engineHealth) recover(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.until, name)
}

func (h *engineHealth) blocked(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	at, ok := h.until[name]
	if !ok {
		return false
	}
	if time.Now().After(at) {
		delete(h.until, name)
		return false
	}
	return true
}

// order returns the engines with the healthy ones first, each group keeping the
// configured order so a working SearxNG still outranks a working scraper.
func (h *engineHealth) order(engines []searchEngine) []searchEngine {
	ordered := make([]searchEngine, 0, len(engines))
	var demoted []searchEngine
	for _, e := range engines {
		if h.blocked(e.name) {
			demoted = append(demoted, e)
			continue
		}
		ordered = append(ordered, e)
	}
	return append(ordered, demoted...)
}

// cooldownFor grades a failure. A refusal is a decision the engine has made
// about us and will keep making; a timeout is weather.
func cooldownFor(err error) time.Duration {
	var he *httpError
	if errors.As(err, &he) {
		switch {
		case he.status == http.StatusForbidden,
			he.status == http.StatusUnauthorized,
			he.status == http.StatusTooManyRequests:
			return nativeBlockCooldown
		case he.status >= 500:
			return nativeSoftCooldown
		}
	}
	return nativeSoftCooldown
}

// rememberReferer records the results page that produced each URL, so a later
// fetch of one can present itself as a click rather than as a bare request.
func (n *NativeBackend) rememberReferer(results []SearchResult, searchPage string) {
	if searchPage == "" {
		return
	}
	for _, r := range results {
		n.referers.set(dedupKey(r.URL), searchPage)
	}
}

func (n *NativeBackend) acquire(ctx context.Context) error {
	select {
	case n.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (n *NativeBackend) release() { <-n.sem }

// ── helpers ─────────────────────────────────────────────────────────────────

// isUsableResultURL rejects non-http links and links back into the engine that
// produced them (ads, "more results", image verticals).
func isUsableResultURL(raw, engineHost string) bool {
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Host)
	return host != engineHost && !strings.HasSuffix(host, "."+engineHost)
}

func isTextualContentType(ct string) bool {
	ct = strings.ToLower(ct)
	// An absent Content-Type is common on small static hosts; assume HTML and
	// let extraction decide, rather than dropping the page outright.
	if strings.TrimSpace(ct) == "" {
		return true
	}
	return strings.Contains(ct, "text/") ||
		strings.Contains(ct, "application/xhtml") ||
		strings.Contains(ct, "application/xml") ||
		strings.Contains(ct, "application/json")
}

func isHTMLContentType(ct string) bool {
	ct = strings.ToLower(ct)
	if strings.TrimSpace(ct) == "" {
		return true
	}
	return strings.Contains(ct, "html") || strings.Contains(ct, "xml")
}

// dedupKey normalises a URL for equality, matching the pipeline's own urlKey.
func dedupKey(u string) string { return strings.TrimRight(strings.TrimSpace(u), "/") }

// normalizeWhitespace collapses every run of whitespace to a single space.
func normalizeWhitespace(s string) string { return strings.Join(strings.Fields(s), " ") }

// truncateChars cuts s to at most max runes, reporting whether it cut.
func truncateChars(s string, max int) (string, bool) {
	count := 0
	for i := range s {
		if count == max {
			return s[:i], true
		}
		count++
	}
	return s, false
}

func clipChars(s string, max int) string {
	out, _ := truncateChars(s, max)
	return out
}

// ── cache ───────────────────────────────────────────────────────────────────

type ttlEntry[T any] struct {
	value     T
	expiresAt time.Time
}

// ttlCache is a small mutex-guarded expiring map. Entries are evicted lazily on
// read; a research run is short-lived, so no background sweeper is warranted.
type ttlCache[T any] struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]ttlEntry[T]
}

func newTTLCache[T any](ttl time.Duration) *ttlCache[T] {
	return &ttlCache[T]{ttl: ttl, entries: make(map[string]ttlEntry[T])}
}

func (c *ttlCache[T]) get(key string) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || time.Now().After(entry.expiresAt) {
		delete(c.entries, key)
		var zero T
		return zero, false
	}
	return entry.value, true
}

func (c *ttlCache[T]) set(key string, value T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = ttlEntry[T]{value: value, expiresAt: time.Now().Add(c.ttl)}
}
