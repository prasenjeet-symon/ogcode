//go:build darwin || windows

package search

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// ChromiumBackend answers the same two questions as NativeBackend — search, and
// fetch a page — by driving Chrome or Edge in headless mode instead of by
// making HTTP requests.
//
// It is the Windows counterpart to SafariBackend, and the point is the same:
// the engine sees a real browser running the page's own scripts, so a results
// page that builds itself in the DOM is readable, and the rate limiting that
// eventually catches a bare HTTP client does not apply. Everything downstream
// is unchanged: Chromium prints the rendered DOM, which goes through the same
// engine parsers and extractReadable as the native path. This swaps the
// transport, not the parsing.
//
// Where the Safari backend drives the user's own browser through AppleScript,
// this one launches a private, throwaway Chromium against a dedicated
// --user-data-dir under the system temp directory, so a search never touches
// the profile the user browses in. Chrome is preferred and Edge is the
// fallback; both are Chromium and answer identically for our purposes.
//
// It is the fallback, not the default, exactly as Safari is: the HTTP path
// answers most queries in under a second, this one spends a browser launch, so
// it earns its cost only when the fast path came back with nothing.
type ChromiumBackend struct {
	searchCache *ttlCache[[]SearchResult]
	fetchCache  *ttlCache[PageContent]

	// profiles holds one dedicated profile directory per concurrent load, and
	// is the concurrency gate at the same time: taking a directory is taking a
	// slot. A pool rather than one shared directory because Chromium locks the
	// profile it is using, so two instances cannot share one.
	profiles    chan string
	profileOnce sync.Once
	profileBase string

	// The liveness probe resolves the browser executable once. Unlike Safari's
	// Automation grant there is nothing for the user to permit, so a found
	// browser is final; a missing one is latched too — a browser will not
	// install itself mid-session, and re-checking on every search would only
	// pay for a stat and a PATH walk each time.
	mu        sync.Mutex
	probed    bool
	available bool
	binary    string

	// searchWait spaces searches to one engine, the way the other backends do.
	// A browser makes the requests look right; it does not make six of them in
	// four seconds look like a person.
	searchWait *hostLimiter
}

const (
	// chromiumMaxConcurrency is how many headless browsers may run at once.
	// Smaller than the native cap because each one is a full browser process
	// holding a profile directory, not a socket.
	chromiumMaxConcurrency = 3

	// chromiumLoadTimeout bounds one headless invocation, from launch to exit.
	// Generous: it covers process start, navigation, script execution and the
	// virtual-time wait below.
	chromiumLoadTimeout = 35 * time.Second

	// chromiumVirtualTimeBudget is how long Chromium may run a page's scripts,
	// in milliseconds, before the DOM is dumped. It is a *virtual* budget, so
	// idle timers do not hold it open longer than real work needs; it is what
	// lets a client-rendered results page finish. A plain --dump-dom returns at
	// the load event, which for a JavaScript-built engine is an empty shell.
	chromiumVirtualTimeBudget = 15000

	// chromiumSearchInterval and chromiumSearchJitter space consecutive
	// searches to one engine, matching the other backends' pacing.
	chromiumSearchInterval = 900 * time.Millisecond
	chromiumSearchJitter   = 2200 * time.Millisecond
)

// errChromiumUnavailable reports that the browser could not be driven at all —
// neither Chrome nor Edge is installed. Distinct from an engine failing,
// because it means every later call will fail the same way until something
// changes on the user's machine.
var errChromiumUnavailable = errors.New("chromium: the browser cannot be driven on this machine")

// NewChromiumBackend builds a Chromium-driven backend, for use as the second
// half of a NewFallbackBackend chain. The non-windows build of this package
// returns nil, and NewFallbackBackend treats a nil secondary as "no fallback",
// so callers wire it unconditionally and the platform question stays in one
// place.
func NewChromiumBackend() *ChromiumBackend {
	return &ChromiumBackend{
		searchCache: newTTLCache[[]SearchResult](nativeCacheTTL),
		fetchCache:  newTTLCache[PageContent](nativeCacheTTL),
		profiles:    make(chan string, chromiumMaxConcurrency),
		searchWait:  newHostLimiter(chromiumSearchInterval, chromiumSearchJitter),
	}
}

var _ Backend = (*ChromiumBackend)(nil)

// Name reports the provider name stamped on this backend's results.
func (s *ChromiumBackend) Name() string { return ProviderChromium }

// Available reports whether a Chromium browser can be driven right now. There
// is no permission to grant, so the only question is whether a Chromium-based
// browser (Chrome, Chromium, Brave, or Edge) is installed; the answer is
// resolved once and kept.
func (s *ChromiumBackend) Available() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.probed {
		return s.available
	}
	s.probed = true

	path := findChromium()
	if path == "" {
		slog.Info("web search: no Chrome or Edge found, using the built-in transport")
		return false
	}
	s.binary, s.available = path, true
	slog.Info("web search: Chromium transport ready", "browser", filepath.Base(path))
	return true
}

// browserPath returns the resolved executable, or "" before Available ran.
func (s *ChromiumBackend) browserPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.binary
}

// loadHTML runs Chromium headless on rawURL and returns the DOM it rendered.
//
// One process per load: Chromium's --dump-dom mode prints the final DOM and
// exits, which is the whole interaction — no debugging port, no websocket, no
// dependency beyond the browser already on the machine. The DOM is taken after
// the page's scripts have run, so a page that builds its results in the browser
// reads the same as one that shipped them.
func (s *ChromiumBackend) loadHTML(ctx context.Context, rawURL string) (string, error) {
	binary := s.browserPath()
	if binary == "" {
		return "", errChromiumUnavailable
	}
	s.ensureProfiles()
	dir, err := s.acquireProfile(ctx)
	if err != nil {
		return "", err
	}
	defer s.releaseProfile(dir)

	loadCtx, cancel := context.WithTimeout(ctx, chromiumLoadTimeout)
	defer cancel()

	cmd := exec.CommandContext(loadCtx, binary, chromiumArgs(rawURL, dir)...)
	// Suppress the console window a fresh browser process would otherwise pop
	// over a Windows desktop, and bound pipe drain after termination so a
	// surviving renderer/GPU child holding stdout open cannot hang cmd.Run().
	configureChromiumCmd(cmd)
	cmd.WaitDelay = 5 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if loadCtx.Err() != nil {
			return "", loadCtx.Err()
		}
		if msg := lastLine(stderr.String()); msg != "" {
			return "", fmt.Errorf("chromium: %s", msg)
		}
		return "", fmt.Errorf("chromium: %w", err)
	}
	out := stdout.String()
	if strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("chromium: no DOM was produced for %s", rawURL)
	}
	return out, nil
}

// Search runs the query in a headless browser and parses the result page with
// the engine parsers the native backend uses.
func (s *ChromiumBackend) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 8
	}
	if !s.Available() {
		return nil, errChromiumUnavailable
	}
	key := query + "\x00" + strconv.Itoa(limit)
	if v, ok := s.searchCache.get(key); ok {
		return v, nil
	}

	var failures []string
	// Every engine is usable: a real browser renders the DOM regardless of how
	// a page builds it, so the needsDOM gate the Safari backend needs (where
	// Safari's JavaScript-from-Apple-Events setting may be off) never applies.
	for _, e := range browserEngines {
		endpoint := e.url(query, limit)
		// Space consecutive queries to the same engine. A browser is not a
		// licence to hammer: the pacing is what keeps a research run's handful
		// of sub-queries looking like someone refining a search.
		if u, err := url.Parse(endpoint); err == nil {
			if err := s.searchWait.wait(ctx, u.Host); err != nil {
				return nil, err
			}
		}
		html, err := s.loadHTML(ctx, endpoint)
		if err != nil {
			slog.Warn("chromium backend: engine failed", "engine", e.name, "err", err)
			failures = append(failures, e.name+": "+err.Error())
			continue
		}
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
		if err != nil {
			failures = append(failures, e.name+": parse: "+err.Error())
			continue
		}
		if results := e.parse(doc, limit); len(results) > 0 {
			withProvider(results, ProviderChromium)
			s.searchCache.set(key, results)
			return results, nil
		}
		slog.Info("chromium backend: engine returned no results", "engine", e.name, "query", query)
	}

	if len(failures) == 0 {
		return nil, nil
	}
	return nil, fmt.Errorf("chromium: every engine failed (%s)", strings.Join(failures, "; "))
}

// FetchPage loads a URL in a headless browser and extracts its readable text.
//
// What the browser buys here is the same as for Safari: the page's scripts run,
// so a challenge interstitial that navigates itself resolves to the real page,
// and a single-page app that draws into an empty shell comes back drawn.
func (s *ChromiumBackend) FetchPage(ctx context.Context, rawURL string) (PageContent, error) {
	if !s.Available() {
		return PageContent{}, errChromiumUnavailable
	}
	if v, ok := s.fetchCache.get(rawURL); ok {
		return v, nil
	}

	html, err := s.loadHTML(ctx, rawURL)
	if err != nil {
		return PageContent{}, err
	}
	title, text := extractReadable([]byte(html), rawURL)
	if text == "" {
		return PageContent{}, fmt.Errorf("chromium: no readable content extracted from %s", rawURL)
	}
	text, truncated := truncateChars(text, nativePageChars)
	page := PageContent{URL: rawURL, Title: title, Text: text, Truncated: truncated, Provider: ProviderChromium}
	s.fetchCache.set(rawURL, page)
	return page, nil
}

// ensureProfiles creates the pool of dedicated profile directories, once. Each
// load takes one. A directory that could not be created is left empty, and a
// load with an empty directory drops --user-data-dir and lets Chromium fall
// back to its own default — worse, but not fatal.
func (s *ChromiumBackend) ensureProfiles() {
	s.profileOnce.Do(func() {
		base, err := os.MkdirTemp("", "ogcode-chromium-")
		if err != nil {
			slog.Warn("web search: could not create a browser profile directory", "err", err)
		}
		s.profileBase = base
		for i := 0; i < chromiumMaxConcurrency; i++ {
			dir := ""
			if base != "" {
				dir = filepath.Join(base, fmt.Sprintf("profile-%d", i))
				if err := os.MkdirAll(dir, 0o700); err != nil {
					slog.Warn("web search: could not create a browser profile", "dir", dir, "err", err)
					dir = ""
				}
			}
			s.profiles <- dir
		}
	})
}

// Close removes the temporary profile directories created for this backend.
func (s *ChromiumBackend) Close() error {
	if s.profileBase == "" {
		return nil
	}
	return os.RemoveAll(s.profileBase)
}

// acquireProfile takes a profile directory, blocking until one is free or the
// caller gives up. Taking one is taking a concurrency slot.
func (s *ChromiumBackend) acquireProfile(ctx context.Context) (string, error) {
	select {
	case dir := <-s.profiles:
		return dir, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// releaseProfile returns a profile directory to the pool.
func (s *ChromiumBackend) releaseProfile(dir string) {
	s.profiles <- dir
}

// chromiumArgs is the argument list for one headless dump. --dump-dom is what
// makes it a one-shot command; --virtual-time-budget is what gives a
// JavaScript-built page time to finish before the DOM is taken.
func chromiumArgs(rawURL, profileDir string) []string {
	args := []string{
		"--headless=new",
		"--dump-dom",
		"--disable-gpu",
		// A server may run without the privileges the sandbox needs, and the
		// profile is throwaway anyway, so dropping it costs nothing this
		// process was relying on.
		"--no-sandbox",
		"--disable-extensions",
		"--disable-background-networking",
		"--disable-sync",
		"--disable-default-apps",
		"--no-first-run",
		"--no-default-browser-check",
		"--mute-audio",
		"--hide-scrollbars",
		"--disable-blink-features=AutomationControlled",
		fmt.Sprintf("--virtual-time-budget=%d", chromiumVirtualTimeBudget),
	}
	if profileDir != "" {
		args = append(args, "--user-data-dir="+profileDir)
	}
	return append(args, rawURL)
}

// findChromium locates a Chromium executable to drive, preferring Chrome over
// Chromium, Brave, and Edge. It checks the standard install locations first,
// then falls back to PATH so a portable or custom install still works.
func findChromium() string {
	for _, p := range chromiumCandidates() {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	for _, name := range chromiumLookPathNames(runtime.GOOS) {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// chromiumCandidates lists the installed-browser paths to try on the current
// OS, ordered so Chrome wins over Chromium, Brave, and Edge wherever several
// exist.
func chromiumCandidates() []string {
	home, _ := os.UserHomeDir()
	return chromiumCandidatesFor(runtime.GOOS, home, os.Getenv)
}

func chromiumCandidatesFor(goos, homeDir string, getenv func(string) string) []string {
	if goos == "darwin" {
		roots := []string{"/Applications"}
		if homeDir != "" {
			roots = append(roots, filepath.Join(homeDir, "Applications"))
		}
		relatives := []string{
			filepath.Join("Google Chrome.app", "Contents", "MacOS", "Google Chrome"),
			filepath.Join("Chromium.app", "Contents", "MacOS", "Chromium"),
			filepath.Join("Brave Browser.app", "Contents", "MacOS", "Brave Browser"),
			filepath.Join("Microsoft Edge.app", "Contents", "MacOS", "Microsoft Edge"),
		}
		var out []string
		for _, rel := range relatives {
			for _, root := range roots {
				out = append(out, filepath.Join(root, rel))
			}
		}
		return out
	}

	roots := []string{
		getenv("ProgramFiles"),
		getenv("ProgramFiles(x86)"),
		getenv("ProgramW6432"),
		getenv("LocalAppData"),
	}
	relatives := []string{
		filepath.Join("Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join("Chromium", "Application", "chrome.exe"),
		filepath.Join("BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
		filepath.Join("Microsoft", "Edge", "Application", "msedge.exe"),
	}
	var out []string
	for _, rel := range relatives {
		for _, root := range roots {
			if root == "" {
				continue
			}
			out = append(out, filepath.Join(root, rel))
		}
	}
	return out
}

func chromiumLookPathNames(goos string) []string {
	if goos == "darwin" {
		return []string{"google-chrome", "chromium", "brave-browser", "microsoft-edge", "chrome"}
	}
	return []string{"chrome.exe", "msedge.exe", "brave.exe"}
}

// lastLine returns the last non-empty line of s, for surfacing Chromium's
// stderr without the wall of startup noise it usually carries.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[len(lines)-1])
}
