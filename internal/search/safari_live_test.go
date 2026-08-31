//go:build darwin

package search

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// These drive the real Safari on this machine: they open windows, load pages
// and close them. Opt in explicitly.
//
//	OGCODE_LIVE_SAFARI_TEST=1 go test ./internal/search/ -run Safari -v
const liveSafariEnv = "OGCODE_LIVE_SAFARI_TEST"

func requireLiveSafari(t *testing.T) *SafariBackend {
	t.Helper()
	if os.Getenv(liveSafariEnv) != "1" {
		t.Skipf("set %s=1 to run tests that drive Safari", liveSafariEnv)
	}
	b := NewSafariBackend()
	if !b.Available() {
		t.Skip("Safari cannot be driven on this machine")
	}
	return b
}

// The whole premise: a real browser returns the same markup the HTTP client
// would have, so the existing engine parsers find results in it.
func TestLiveSafariSearch(t *testing.T) {
	b := requireLiveSafari(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	results, err := b.Search(ctx, "golang context cancellation", 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("no results")
	}
	for i, r := range results {
		t.Logf("  %d. %s — %s", i+1, r.Title, r.URL)
		if strings.TrimSpace(r.Title) == "" || !strings.HasPrefix(r.URL, "http") {
			t.Errorf("result %d malformed: %+v", i, r)
		}
	}
}

func TestLiveSafariFetchPage(t *testing.T) {
	b := requireLiveSafari(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	page, err := b.FetchPage(ctx, "https://pkg.go.dev/context")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !strings.Contains(strings.ToLower(page.Text), "context") {
		t.Errorf("extracted text does not look like the page: %.200q", page.Text)
	}
	t.Logf("title=%q chars=%d truncated=%v", page.Title, len(page.Text), page.Truncated)
}

// Concurrent calls are the real usage — the research pipeline fetches several
// pages at once. This checks they do not trip over each other's windows, and
// that the whole batch beats running them one after another.
func TestLiveSafariConcurrentFetches(t *testing.T) {
	b := requireLiveSafari(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	urls := []string{
		"https://pkg.go.dev/context",
		"https://pkg.go.dev/net/http",
		"https://pkg.go.dev/encoding/json",
	}

	start := time.Now()
	type res struct {
		page PageContent
		err  error
	}
	out := make([]res, len(urls))
	done := make(chan int, len(urls))
	for i, u := range urls {
		go func(i int, u string) {
			p, err := b.FetchPage(ctx, u)
			out[i] = res{p, err}
			done <- i
		}(i, u)
	}
	for range urls {
		<-done
	}
	elapsed := time.Since(start)

	for i, r := range out {
		if r.err != nil {
			t.Errorf("%s: %v", urls[i], r.err)
			continue
		}
		if len(r.page.Text) == 0 {
			t.Errorf("%s: no text extracted", urls[i])
		}
	}
	t.Logf("three concurrent fetches in %v", elapsed)
}

// A cancelled turn must not leave a window behind in someone's browser. The
// close path deliberately does not inherit the caller's context for exactly
// this case.
func TestLiveSafariCancellationClosesWindow(t *testing.T) {
	b := requireLiveSafari(t)

	before := stableWindowCount(t)
	ctx, cancel := context.WithTimeout(context.Background(), 900*time.Millisecond)
	defer cancel()

	// Long enough that the cancellation lands mid-load.
	_, err := b.loadHTML(ctx, "https://pkg.go.dev/net/http", "")
	if err == nil {
		t.Skip("page loaded before the cancellation landed; nothing to assert")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Logf("cancelled with: %v", err)
	}

	// Safari's `close` returns before the window is actually gone, so poll for
	// the count to come back rather than sleeping a fixed interval and hoping.
	deadline := time.Now().Add(8 * time.Second)
	var after int
	for time.Now().Before(deadline) {
		if after = windowCount(t); after == before {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Errorf("window leaked on cancellation: %d before, %d after", before, after)
}

// The leak the first version of this file missed: cancelling *during* window
// creation, before loadHTML has the id it needs to arm its cleanup. The
// original test only cancelled during the page load, by which point the defer
// was already in place — so it passed while windows piled up in the browser.
func TestLiveSafariCancellationDuringWindowOpen(t *testing.T) {
	b := requireLiveSafari(t)
	before := stableWindowCount(t)

	// Deadlines short enough to land inside openWindow's id-resolution loop.
	for _, d := range []time.Duration{60 * time.Millisecond, 120 * time.Millisecond, 250 * time.Millisecond} {
		ctx, cancel := context.WithTimeout(context.Background(), d)
		_, _ = b.loadHTML(ctx, "https://pkg.go.dev/errors", "")
		cancel()
	}

	deadline := time.Now().Add(15 * time.Second)
	var after int
	for time.Now().Before(deadline) {
		if after = windowCount(t); after == before {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Errorf("windows orphaned during open: %d before, %d after", before, after)
}

func windowCount(t *testing.T) int {
	t.Helper()
	out, err := runOSA(context.Background(), `tell application "Safari" to count windows`)
	if err != nil {
		t.Fatalf("count windows: %v", err)
	}
	n := 0
	_, _ = fmt.Sscan(out, &n)
	return n
}

// stableWindowCount waits for the count to stop moving before trusting it. A
// preceding test's window may still be tearing down, and a baseline taken
// mid-teardown reads one too high — which then looks exactly like a leak.
func stableWindowCount(t *testing.T) int {
	t.Helper()
	last := -1
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		n := windowCount(t)
		if n == last {
			return n
		}
		last = n
		time.Sleep(300 * time.Millisecond)
	}
	return last
}
