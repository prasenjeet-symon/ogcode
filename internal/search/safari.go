//go:build darwin

package search

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// SafariBackend answers the same two questions as NativeBackend — search, and
// fetch a page — by driving the user's own Safari instead of by making HTTP
// requests.
//
// The point is not the browser; it is the request that comes out of it. An
// engine sees a real browser with real cookies and a real TLS fingerprint, so
// the rate limiting that a bare HTTP client eventually runs into does not
// apply. Everything downstream is unchanged: Safari hands back the same markup
// the HTTP client would have received, and it goes through the same engine
// parsers and extractReadable that the native backend uses. This swaps the
// transport, not the parsing.
//
// It is the fallback, not the default. The HTTP path answers most searches in
// under a second and without touching the user's screen; this one takes several
// and opens windows, so it earns its cost only when the fast path has come back
// with nothing — blocked, rate-limited, or facing a page that does not exist
// until its scripts run. NewFallbackBackend wires that order.
//
// Being last also means this backend reports failure rather than delegating it.
// It used to hold a fallback of its own, from when it ran first; now there is
// nothing after it, and swallowing an error here would turn "Safari is not
// permitted to run" into a silent empty result.
type SafariBackend struct {
	// sem caps how many automation windows can exist at once. Lower than the
	// native cap: each slot is a visible window on someone's screen, not a
	// socket.
	sem         chan struct{}
	searchCache *ttlCache[[]SearchResult]
	fetchCache  *ttlCache[PageContent]

	// The liveness probe is memoised, but a *failure* is only memoised for a
	// while. Automation permission is granted in System Settings, often right
	// after the first refusal, and a permanent cache would mean the user grants
	// it and nothing changes until they restart ogcode.
	mu        sync.Mutex
	probed    bool
	available bool
	probedAt  time.Time
	// installed is latched separately: a missing Safari.app is not going to
	// resolve itself, so that answer never expires.
	missing bool

	// createMu serialises window creation. A window is identified by diffing
	// the window set before and after `make new document`, and that diff is
	// only unambiguous for one creation at a time: two concurrent calls each
	// see both new windows, and map iteration means they can pick the same id
	// — leaving the other window owned by nobody and never closed. The lock is
	// held only across creation and id resolution (tens of milliseconds), so
	// the page loads themselves still overlap.
	createMu sync.Mutex

	// jsMu guards the "may we run JavaScript" state. Reading the rendered DOM
	// needs Safari's "Allow JavaScript from Apple Events" setting, which is off
	// until someone turns it on, so the capability is discovered by trying it
	// rather than assumed either way.
	//
	// The answer is memoised because it is asked once per page load and it is
	// a checkbox, not a per-page property. A "no" expires, for the same reason
	// the Automation refusal does: the checkbox can be ticked mid-session.
	jsMu        sync.Mutex
	jsKnown     bool
	jsOK        bool
	jsCheckedAt time.Time
	jsHinted    bool

	// searchWait spaces searches to one engine, the way the native backend
	// does. A browser makes the requests look right; it does not make six of
	// them in four seconds look like a person.
	searchWait *hostLimiter
}

const (
	// safariMaxConcurrency is how many automation windows may be open at once.
	// Deliberately small: the loads inside a window are already parallel, so
	// more windows buys little and costs the user their screen.
	safariMaxConcurrency = 3

	// safariLoadTimeout bounds one page load. Longer than the HTTP timeout
	// because a real browser also runs scripts and fetches subresources.
	safariLoadTimeout = 25 * time.Second

	// safariPollInterval is how often a loading tab is checked. Each poll is an
	// Apple Event, so this trades latency against event traffic.
	safariPollInterval = 150 * time.Millisecond

	// safariMinBytes is the source length below which a tab is still considered
	// to be loading. A blank or error shell is a few hundred bytes; a real
	// results page is hundreds of kilobytes.
	safariMinBytes = 20000

	// safariOSATimeout bounds a single osascript invocation. A hung Apple Event
	// would otherwise pin a goroutine for the whole turn.
	safariOSATimeout = 20 * time.Second

	// safariOpenTimeout bounds window creation and id resolution. Creation does
	// not inherit the caller's deadline, so it needs its own.
	safariOpenTimeout = 8 * time.Second

	// safariProbeCooldown is how long a refusal is trusted before asking again.
	// Long enough that a denied user is not re-prompted every search, short
	// enough that granting the permission takes effect without a restart.
	safariProbeCooldown = 2 * time.Minute

	// safariJSRecheck is the same idea for the JavaScript permission: it lives
	// behind a checkbox the user can tick mid-session, so a refusal expires.
	safariJSRecheck = 3 * time.Minute

	// safariRenderSettle bounds the extra wait for client-side rendering. It
	// only applies when the DOM is readable; the served markup has already
	// settled by the time it starts.
	safariRenderSettle = 4 * time.Second

	// safariDwellMin and safariDwellJitter are how long the page is left alone
	// after it finishes loading, before its content is read.
	//
	// A person's eyes take a moment to reach the results. A client that reads
	// the DOM the instant the last byte lands, every time, to the millisecond,
	// produces a timing signature that survives every other disguise — and the
	// pause costs a fraction of the page load it follows.
	safariDwellMin    = 350 * time.Millisecond
	safariDwellJitter = 800 * time.Millisecond

	// safariSearchInterval and safariSearchJitter space consecutive searches to
	// one engine, matching the native backend's pacing.
	safariSearchInterval = 900 * time.Millisecond
	safariSearchJitter   = 2200 * time.Millisecond
)

// errSafariUnavailable reports that the browser could not be driven at all —
// not installed, not launchable, or Automation refused. Distinct from an engine
// failing, because it means every later call will fail the same way until
// something changes on the user's machine.
var errSafariUnavailable = errors.New("safari: the browser cannot be driven on this machine")

// NewSafariBackend builds a Safari-driven backend, for use as the second half
// of a NewFallbackBackend chain. The non-darwin build of this package returns
// nil, and NewFallbackBackend treats a nil secondary as "no fallback", so
// callers wire it unconditionally and the platform question stays in one place.
func NewSafariBackend() *SafariBackend {
	return &SafariBackend{
		sem:         make(chan struct{}, safariMaxConcurrency),
		searchCache: newTTLCache[[]SearchResult](nativeCacheTTL),
		fetchCache:  newTTLCache[PageContent](nativeCacheTTL),
		searchWait:  newHostLimiter(safariSearchInterval, safariSearchJitter),
	}
}

var _ Backend = (*SafariBackend)(nil)

// Name reports the provider name stamped on this backend's results.
func (s *SafariBackend) Name() string { return ProviderSafari }

// safariEngines are the engines the browser path drives. They are the same ones
// the native path uses, reached through the same URL builders and read by the
// same parsers — only the transport differs.
//
// Sharing the whole chain rather than a subset matters more than it looks.
// Safari's advantage is not that it can parse something the native path cannot;
// it is that the requests come from a real browser with the user's own cookies,
// so the rate limiting that eventually catches a bare HTTP client does not
// apply. An engine that is missing here is an engine with no second chance once
// the native path is throttled.
var safariEngines = []struct {
	name  string
	url   func(query string, limit int) string
	parse func(*goquery.Document, int) []SearchResult
	// ready is a fragment that only appears once real results have rendered.
	//
	// Byte-count stability is not enough to know a search page is done. Brave
	// answers a fresh browser with a JavaScript challenge that sits perfectly
	// still at ~73KB for about eight seconds before swapping itself for the
	// results — long enough that a stability check calls it loaded, the parser
	// finds nothing, and the engine looks like it blocked us. Waiting for the
	// marker waits for the page the parser actually needs.
	ready string
	// needsDOM marks an engine whose results exist only after its scripts run.
	// Skipped entirely when the rendered DOM cannot be read, because the served
	// markup for these is an empty shell and loading it would cost a page load
	// for a guaranteed zero results.
	needsDOM bool
}{
	// Google leads when it is reachable at all. It is the best answer to a
	// research query and it is the one engine here that no HTTP client can
	// have: it ships a shell and builds the results in the page, so reading it
	// requires a browser whose DOM we can see. That combination is exactly what
	// this backend is.
	{"google", func(q string, _ int) string { return googleSearchURL(q) }, parseGoogleResults, `<h3`, true},
	{"duckduckgo", func(q string, _ int) string { return duckDuckGoSearchURL(q) }, parseDuckDuckGoResults, `result__a`, false},
	{"brave", func(q string, _ int) string { return braveSearchURL(q) }, parseBraveResults, `data-type="web"`, false},
	{"bing", func(q string, _ int) string { return bingSearchURL(q) }, parseBingResults, `b_algo`, false},
	{"yahoo", yahooSearchURL, parseYahooResults, `class="algo`, false},
}

// Available reports whether Safari can be driven right now.
//
// The first call is what triggers macOS's Automation prompt ("ogcode wants to
// control Safari"), so it deliberately happens at the point of use rather than
// at startup: a dialog that appears while someone is running a search is one
// they have the context to answer, and probing at boot would launch Safari for
// a user who never searches.
func (s *SafariBackend) Available() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.missing {
		return false
	}
	// A success is final; a refusal is reconsidered after the cooldown.
	if s.probed && (s.available || time.Since(s.probedAt) < safariProbeCooldown) {
		return s.available
	}

	if _, err := os.Stat("/Applications/Safari.app"); err != nil {
		slog.Info("web search: Safari not installed, using the built-in transport")
		s.missing = true
		s.probed, s.available = true, false
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), safariOSATimeout)
	defer cancel()
	// Counting windows is the cheapest command that still proves Automation was
	// granted; without the grant it fails with -1743.
	_, err := runOSA(ctx, `tell application "Safari" to count windows`)

	wasAvailable := s.probed && s.available
	s.probed, s.probedAt = true, time.Now()
	s.available = err == nil

	switch {
	case err == nil && !wasAvailable:
		slog.Info("web search: Safari transport ready")
	case err != nil && isNotAuthorized(err):
		slog.Warn("web search: ogcode is not allowed to control Safari, using the built-in transport instead. " +
			"To use your browser for search, allow it under System Settings > Privacy & Security > Automation, " +
			"then run another search — no restart needed.")
	case err != nil:
		slog.Warn("web search: Safari could not be reached, using the built-in transport", "err", err)
	}
	return s.available
}

// isNotAuthorized separates "the user said no" from "Safari was busy". Only the
// first is worth telling someone how to fix; AppleScript reports it as -1743,
// and the message wording has varied across macOS releases, so both are matched.
// isTransientOSAError reports whether an osascript failure was the command
// being cut short rather than Safari answering. runOSA kills the process on its
// own deadline, so this is not always visible in the caller's context.
func isTransientOSAError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, frag := range []string{"killed", "timed out", "deadline exceeded", "context canceled", "interrupt"} {
		if strings.Contains(msg, frag) {
			return true
		}
	}
	return false
}

func isNotAuthorized(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "-1743") ||
		strings.Contains(msg, "not authorized") ||
		strings.Contains(msg, "not allowed assistive")
}

// Search runs the query in a real browser tab and parses the result page with
// the engine parsers the native backend uses.
func (s *SafariBackend) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 8
	}
	if !s.Available() {
		return nil, errSafariUnavailable
	}
	key := query + "\x00" + strconv.Itoa(limit)
	if v, ok := s.searchCache.get(key); ok {
		return v, nil
	}

	var failures []string
	for _, e := range safariEngines {
		if e.needsDOM && !s.javascriptAvailable() {
			continue
		}
		endpoint := e.url(query, limit)
		// Space consecutive queries to the same engine. A browser is not a
		// licence to hammer: the pacing is what keeps a research run's handful
		// of sub-queries looking like someone refining a search.
		if u, err := url.Parse(endpoint); err == nil {
			if err := s.searchWait.wait(ctx, u.Host); err != nil {
				return nil, err
			}
		}
		html, err := s.loadHTML(ctx, endpoint, e.ready)
		if err != nil {
			slog.Warn("safari backend: engine failed", "engine", e.name, "err", err)
			failures = append(failures, e.name+": "+err.Error())
			continue
		}
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
		if err != nil {
			failures = append(failures, e.name+": parse: "+err.Error())
			continue
		}
		if results := e.parse(doc, limit); len(results) > 0 {
			withProvider(results, ProviderSafari)
			s.searchCache.set(key, results)
			return results, nil
		}
		slog.Info("safari backend: engine returned no results", "engine", e.name, "query", query)
	}

	return nil, fmt.Errorf("safari: every engine failed (%s)", strings.Join(failures, "; "))
}

// FetchPage loads a URL in a tab and extracts its readable text.
//
// What the browser buys here, precisely: the page's scripts run, so anything
// that gates content behind a challenge and then *navigates* — Cloudflare
// interstitials, Brave's own bot check — resolves and yields the real page.
//
// Client-rendered pages used to be the gap: Safari's `source` property reports
// the HTML that was served, so a single-page app that draws itself into an
// empty shell came back as that shell. readPage closes it where it can, by
// reading document.documentElement.outerHTML instead — still markup, so
// extraction is unchanged — and falls back to `source` where Safari's
// JavaScript-from-Apple-Events setting is off.
func (s *SafariBackend) FetchPage(ctx context.Context, rawURL string) (PageContent, error) {
	if !s.Available() {
		return PageContent{}, errSafariUnavailable
	}
	if v, ok := s.fetchCache.get(rawURL); ok {
		return v, nil
	}

	html, err := s.loadHTML(ctx, rawURL, "")
	if err != nil {
		return PageContent{}, err
	}
	title, text := extractReadable([]byte(html), rawURL)
	if text == "" {
		return PageContent{}, fmt.Errorf("safari: no readable content extracted from %s", rawURL)
	}
	text, truncated := truncateChars(text, nativePageChars)
	page := PageContent{URL: rawURL, Title: title, Text: text, Truncated: truncated, Provider: ProviderSafari}
	s.fetchCache.set(rawURL, page)
	return page, nil
}

// loadHTML opens rawURL in a dedicated window, waits for it to load, returns
// its HTML source and closes the window.
//
// A window per load, rather than tabs in one shared window, because Safari's
// scripting model gives windows a stable id but gives tabs only an index —
// and indices shift the moment any tab closes. Owning the whole window means
// the one tab in it is always tab 1, and cleanup is a single close.
func (s *SafariBackend) loadHTML(ctx context.Context, rawURL, ready string) (string, error) {
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}

	// Creation is deliberately not cancellable. If the caller's context could
	// kill it midway, Safari might still process the command and create the
	// window seconds later — after this function has returned and with no defer
	// armed to close it. Bounded separately, it either produces a window this
	// function owns, or fails; either way nothing is orphaned.
	winID, err := s.openWindow(rawURL)
	if err != nil {
		return "", err
	}
	// Closing must not inherit the caller's context: when a turn is cancelled
	// mid-load, ctx is already dead, and using it here would leak the window
	// into the user's browser — the exact failure this backend must not have.
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), safariOSATimeout)
		defer cancel()
		if _, err := runOSA(closeCtx, fmt.Sprintf(
			`tell application "Safari" to close (first window whose id is %s)`, winID)); err != nil {
			slog.Warn("safari backend: could not close automation window", "window", winID, "err", err)
		}
	}()

	// Now that cleanup is armed, a cancelled caller can be honoured safely.
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// Settled before waiting, so the poll watches the same document the read
	// will take.
	useDOM := s.probeJavaScript(ctx, winID)
	if err := s.waitForLoad(ctx, winID, ready, useDOM); err != nil {
		return "", err
	}
	if useDOM {
		// The markup has settled; anything the page draws for itself has not.
		s.waitForRender(ctx, winID)
	}
	if err := sleepCtx(ctx, safariDwell()); err != nil {
		return "", err
	}
	return s.readPage(ctx, winID, useDOM)
}

// safariDwell is the randomised pause between a page finishing and being read.
func safariDwell() time.Duration {
	return safariDwellMin + time.Duration(rand.Int64N(int64(safariDwellJitter)))
}

// openWindow creates a window at rawURL and returns its id.
//
// The id is found by diffing the window set, not by reading `id of window 1`:
// immediately after `make new document` the new window is not reliably window 1,
// and reading it there returns whichever window the user happened to be in.
func (s *SafariBackend) openWindow(rawURL string) (winID string, err error) {
	s.createMu.Lock()
	defer s.createMu.Unlock()

	// Its own context, not the caller's: see loadHTML. Bounded so a wedged
	// Safari cannot hold the creation lock indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), safariOpenTimeout)
	defer cancel()

	before, err := s.windowIDs(ctx)
	if err != nil {
		return "", err
	}
	script := fmt.Sprintf(`tell application "Safari" to make new document with properties {URL:%s}`,
		osaString(rawURL))
	if _, err := runOSA(ctx, script); err != nil {
		return "", fmt.Errorf("safari: open window: %w", err)
	}

	// Belt and braces for the one case the ownership rule cannot cover: the
	// window never appeared within the deadline, so there is no id to hand back
	// and no defer to arm, yet Safari may still produce it afterwards.
	defer func() {
		if err != nil {
			s.sweepNewWindows(before)
		}
	}()

	deadline := time.Now().Add(safariOpenTimeout)
	for time.Now().Before(deadline) {
		after, err := s.windowIDs(ctx)
		if err != nil {
			return "", err
		}
		for id := range after {
			if !before[id] {
				// Best effort: keeping the automation window out of the way is a
				// courtesy, and not every Safari version honours it.
				_, _ = runOSA(ctx, fmt.Sprintf(
					`tell application "Safari" to set miniaturized of (first window whose id is %s) to true`, id))
				return id, nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "", fmt.Errorf("safari: window did not appear")
}

// sweepNewWindows closes any window that was not present in `before`.
//
// It runs on a fresh context on purpose: it exists precisely for the case where
// the caller's context is already dead, and reusing that context would make the
// cleanup fail exactly when it is needed.
func (s *SafariBackend) sweepNewWindows(before map[string]bool) {
	ctx, cancel := context.WithTimeout(context.Background(), safariOSATimeout)
	defer cancel()

	// Poll rather than look once. The common reason this runs is a context that
	// died moments after `make new document` was issued — at which point Safari
	// has accepted the command but not yet created the window, so a single
	// immediate check finds nothing and the window appears just afterwards.
	deadline := time.Now().Add(6 * time.Second)
	for {
		after, err := s.windowIDs(ctx)
		if err != nil {
			slog.Warn("safari backend: could not sweep automation windows", "err", err)
			return
		}
		closed := false
		for id := range after {
			if before[id] {
				continue
			}
			if _, err := runOSA(ctx, fmt.Sprintf(
				`tell application "Safari" to close (first window whose id is %s)`, id)); err != nil {
				slog.Warn("safari backend: could not close orphaned window", "window", id, "err", err)
				continue
			}
			slog.Debug("safari backend: swept orphaned window", "window", id)
			closed = true
		}
		if closed || time.Now().After(deadline) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (s *SafariBackend) windowIDs(ctx context.Context) (map[string]bool, error) {
	out, err := runOSA(ctx, `tell application "Safari" to return id of every window`)
	if err != nil {
		return nil, fmt.Errorf("safari: list windows: %w", err)
	}
	ids := make(map[string]bool)
	for _, id := range strings.Split(out, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids[id] = true
		}
	}
	return ids, nil
}

// waitForLoad polls until the page has substance and has stopped growing.
//
// Two consecutive equal lengths, not just one over the threshold: a large page
// crosses the threshold long before it finishes, and reading it there yields a
// truncated document that the parsers then find nothing in.
func (s *SafariBackend) waitForLoad(ctx context.Context, winID, ready string, useDOM bool) error {
	ctx, cancel := context.WithTimeout(ctx, safariLoadTimeout)
	defer cancel()

	// Poll whichever document the read will take. For an engine that renders
	// its results client-side, the served markup never grows the marker at all.
	pageExpr := sourceExpr(winID)
	if useDOM {
		pageExpr = domExpr(winID)
	}

	// The marker test runs inside AppleScript rather than here: the alternative
	// is pulling half a megabyte of HTML across an Apple Event on every poll.
	readyClause := "set hit to 1"
	if ready != "" {
		readyClause = fmt.Sprintf("if src contains %s then set hit to 1", osaString(ready))
	}
	script := fmt.Sprintf(`tell application "Safari"
		set n to 0
		set hit to 0
		try
			set src to %s
			set n to count of characters of src
			%s
		end try
		return (n as text) & "|" & (hit as text)
	end tell`, pageExpr, readyClause)

	last := -1
	for {
		out, err := runOSA(ctx, script)
		if err != nil {
			// The deadline can land on the poll as easily as on the sleep.
			if ctx.Err() != nil {
				return s.loadTimeout(winID, last, ctx.Err())
			}
			return fmt.Errorf("safari: poll: %w", err)
		}
		parts := strings.SplitN(strings.TrimSpace(out), "|", 2)
		n, _ := strconv.Atoi(parts[0])
		hit := len(parts) > 1 && parts[1] == "1"

		// With a marker, the marker *is* the readiness signal — plus one poll of
		// stability, since it can appear while the rest is still rendering. The
		// byte floor deliberately does not apply here: it is only a stand-in
		// for "this is not a blank shell" in the no-marker case, and applying
		// it to a marker match would reject a small results page outright.
		settled := (hit && n == last) || (ready == "" && n >= safariMinBytes && n == last)
		if settled {
			return nil
		}
		last = n
		if err := sleepCtx(ctx, safariPollInterval); err != nil {
			return s.loadTimeout(winID, last, err)
		}
	}
}

// loadTimeout decides what a load that ran out of time should mean.
//
// A page that never showed its marker is not necessarily broken — it may
// genuinely have no results, or be an engine having a bad day. If something
// substantial did load, hand it to the parser: it reports zero results and the
// chain moves to the next engine, which is a better outcome than turning a real
// (if empty) answer into a transport error.
func (s *SafariBackend) loadTimeout(winID string, chars int, cause error) error {
	if chars >= safariMinBytes {
		slog.Debug("safari backend: load timed out with content present; parsing what arrived",
			"window", winID, "chars", chars)
		return nil
	}
	return fmt.Errorf("safari: page did not finish loading: %w", cause)
}

// readPage returns the tab's HTML, preferring the rendered DOM over the served
// markup.
//
// This is the fix for the limitation the backend shipped with: `source` reports
// what the server sent, so a page that draws itself with JavaScript — a docs
// site built as a single-page app, a results page that renders client-side —
// came back as an empty shell even though Safari was displaying it perfectly.
// Reading document.documentElement.outerHTML instead returns what is actually
// on screen, and it is still markup, so Readability and the engine parsers keep
// working unchanged.
//
// The catch is that it needs Safari's "Allow JavaScript from Apple Events",
// which is off by default and cannot be enabled programmatically. So the DOM is
// attempted, and a refusal falls back to `source` — which is exactly the
// behaviour that predates this, meaning the worst case is no worse than before.
func (s *SafariBackend) readPage(ctx context.Context, winID string, useDOM bool) (string, error) {
	if useDOM {
		html, err := s.capture(ctx, winID, domExpr(winID))
		if err == nil && html != "" {
			return html, nil
		}
		// The probe said yes and the read still failed, so this is not the
		// permission — a page that navigated away mid-read, most likely. Take
		// the markup rather than losing the page.
		slog.Debug("safari backend: rendered DOM unreadable, falling back to served markup", "err", err)
	}
	return s.capture(ctx, winID, sourceExpr(winID))
}

// sourceExpr and domExpr are the two AppleScript expressions that yield a
// page's HTML: what the server sent, and what is currently rendered.
func sourceExpr(winID string) string {
	return fmt.Sprintf(`source of current tab of (first window whose id is %s)`, winID)
}

func domExpr(winID string) string {
	return fmt.Sprintf(`(do JavaScript "document.documentElement.outerHTML" in current tab of (first window whose id is %s))`, winID)
}

// capture evaluates an HTML-yielding expression and returns the result.
//
// It goes via a temp file rather than stdout: a results page is around half a
// megabyte, and routing that back through an Apple Event reply and the shell is
// both slower and a good way to meet an escaping bug. AppleScript writes UTF-8
// straight to disk instead.
func (s *SafariBackend) capture(ctx context.Context, winID, expr string) (string, error) {
	f, err := os.CreateTemp("", "ogcode-safari-*.html")
	if err != nil {
		return "", err
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)

	script := fmt.Sprintf(`tell application "Safari"
		set src to %s
		set fh to open for access POSIX file %s with write permission
		set eof fh to 0
		write src to fh as «class utf8»
		close access fh
	end tell`, expr, osaString(path))

	if _, err := runOSA(ctx, script); err != nil {
		return "", fmt.Errorf("safari: read page: %w", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(body) == 0 {
		return "", fmt.Errorf("safari: page source was empty")
	}
	return string(body), nil
}

// probeJavaScript reports whether this window's DOM can be read, running the
// cheapest possible script to find out.
//
// It is answered before the page is waited on rather than at read time, and
// that ordering is the point. The readiness poll has to look at whichever
// document the parser will eventually read: polling the served markup for a
// marker that only ever appears in the rendered DOM would wait out the whole
// load timeout on every search of a JavaScript-rendered engine, then parse
// successfully anyway — 25 seconds bought nothing.
func (s *SafariBackend) probeJavaScript(ctx context.Context, winID string) bool {
	s.jsMu.Lock()
	if s.jsKnown && (s.jsOK || time.Since(s.jsCheckedAt) < safariJSRecheck) {
		ok := s.jsOK
		s.jsMu.Unlock()
		return ok
	}
	s.jsMu.Unlock()

	_, err := runOSA(ctx, fmt.Sprintf(
		`tell application "Safari" to do JavaScript "1" in current tab of (first window whose id is %s)`, winID))

	// A probe that was killed says nothing about the permission. Caching it as
	// a refusal would be a real regression rather than a cosmetic one: a single
	// cancelled turn would silently disable the rendered-DOM path — and with it
	// Google — for the next several minutes.
	if err != nil && (ctx.Err() != nil || isTransientOSAError(err)) {
		slog.Debug("safari backend: JavaScript probe interrupted, not recording a verdict", "err", err)
		return false
	}

	s.jsMu.Lock()
	defer s.jsMu.Unlock()
	s.jsKnown, s.jsOK, s.jsCheckedAt = true, err == nil, time.Now()
	if err == nil {
		return true
	}
	// Said once, not per search: this is a missing nicety, not a fault.
	// Everything still works without it, on the markup the server sent.
	if !s.jsHinted {
		s.jsHinted = true
		slog.Info("web search: reading Safari's rendered page is not permitted, using the served markup instead. "+
			"Pages that build themselves with JavaScript will come back mostly empty, and Google is skipped. "+
			"To allow it, enable Safari > Settings > Advanced > \"Show features for web developers\", then "+
			"Develop > \"Allow JavaScript from Apple Events\" — no restart needed.", "err", err)
	}
	return false
}

// javascriptAvailable reports the memoised answer without probing, for the
// callers that have no window to probe with — Search, deciding whether an
// engine that only exists in the rendered DOM is worth loading.
//
// Optimistic when nothing is known yet: the first load will settle it, and
// being wrong once costs a page load, while being pessimistic by default would
// mean never discovering the capability at all.
func (s *SafariBackend) javascriptAvailable() bool {
	s.jsMu.Lock()
	defer s.jsMu.Unlock()
	if !s.jsKnown {
		return true
	}
	return s.jsOK || time.Since(s.jsCheckedAt) > safariJSRecheck
}

// waitForRender waits for the rendered DOM to stop growing.
//
// waitForLoad has already established that the served markup is complete, which
// says nothing about a page that fetches its content afterwards. Two equal
// readings, bounded by safariRenderSettle: this is a best-effort improvement on
// reading immediately, so it never returns an error — whatever is there when
// the budget runs out is what gets read.
func (s *SafariBackend) waitForRender(ctx context.Context, winID string) {
	ctx, cancel := context.WithTimeout(ctx, safariRenderSettle)
	defer cancel()

	script := fmt.Sprintf(`tell application "Safari"
		set n to 0
		try
			set n to (do JavaScript "document.documentElement.outerHTML.length" in current tab of (first window whose id is %s))
		end try
		return n as text
	end tell`, winID)

	last := -1
	for {
		out, err := runOSA(ctx, script)
		if err != nil {
			return
		}
		n, _ := strconv.Atoi(strings.TrimSpace(out))
		if n > 0 && n == last {
			return
		}
		last = n
		if err := sleepCtx(ctx, safariPollInterval); err != nil {
			return
		}
	}
}

// runOSA executes one AppleScript and returns its stdout.
func runOSA(ctx context.Context, script string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, safariOSATimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		if stderr != "" {
			return "", fmt.Errorf("osascript: %s", stderr)
		}
		return "", fmt.Errorf("osascript: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// osaString quotes a Go string as an AppleScript literal.
//
// Values reaching here are URLs chosen by a model from search results and temp
// paths, so this is a real injection boundary: an unescaped quote would end the
// literal and let the rest of the URL run as AppleScript.
func osaString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
