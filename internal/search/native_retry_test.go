package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A rate-limited engine used to be fatal: any non-2xx became a hard error, so a
// single 429 took that engine out for the call. With both engines limited the
// whole run reported "all search engines failed" — which is what a burst of
// sub-queries produces.
func TestGet_RetriesRateLimit(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	body, _, _, err := NewNativeBackend().get(context.Background(), srv.URL, 5*time.Second, navigation{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(body) != "ok" {
		t.Errorf("body = %q, want ok", body)
	}
	if got := hits.Load(); got != 3 {
		t.Errorf("attempts = %d, want 3 (two retries)", got)
	}
}

// A block is not a "later". An engine that refuses non-browser clients answers
// 403 every time, so retrying only delays the fallback to the next engine.
func TestGet_DoesNotRetryBlocked(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	if _, _, _, err := NewNativeBackend().get(context.Background(), srv.URL, 5*time.Second, navigation{}); err == nil {
		t.Fatal("expected an error for 403")
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1 — 403 must not be retried", got)
	}
}

// Exhausting the retries has to surface the real status, not a generic failure:
// the pipeline logs it and the user needs to see "http 429" to know what
// happened.
func TestGet_ReportsStatusAfterExhaustingRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	_, _, _, err := NewNativeBackend().get(context.Background(), srv.URL, 5*time.Second, navigation{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error = %q, want it to name the status", err)
	}
}

func TestParseRetryAfter(t *testing.T) {
	for _, tc := range []struct {
		name, header string
		want         time.Duration
	}{
		{"empty", "", 0},
		{"seconds", "3", 3 * time.Second},
		{"zero", "0", 0},
		{"garbage", "soon", 0},
		// An engine asking for a minute is asking for longer than a research
		// turn can wait; fall back to ordinary backoff and let the chain move on.
		{"beyond the cap", "120", 0},
		{"http-date in the past", "Mon, 02 Jan 2006 15:04:05 GMT", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRetryAfter(tc.header); got != tc.want {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tc.header, got, tc.want)
			}
		})
	}
}

// A server-supplied Retry-After wins over the computed backoff, so a polite
// engine is obeyed rather than second-guessed.
func TestRetryDelay_HonoursRetryAfter(t *testing.T) {
	he := &httpError{status: 429, retryable: true, retryAfter: 2 * time.Second}
	if got := he.retryDelay(1); got != 2*time.Second {
		t.Errorf("retryDelay = %v, want the server's 2s", got)
	}
}

// Without Retry-After the delay grows and carries jitter — several sub-queries
// retrying in lockstep would rebuild the burst that caused the limit.
func TestRetryDelay_BacksOffAndJitters(t *testing.T) {
	he := &httpError{status: 503, retryable: true}
	first, second := he.retryDelay(1), he.retryDelay(2)
	if first < nativeRetryBase || first >= 2*nativeRetryBase {
		t.Errorf("first delay %v outside [base, 1.5x base) after jitter", first)
	}
	if second <= first {
		t.Errorf("second delay %v did not grow beyond the first %v", second, first)
	}

	seen := make(map[time.Duration]bool)
	for i := 0; i < 20; i++ {
		seen[he.retryDelay(2)] = true
	}
	if len(seen) < 2 {
		t.Error("delay never varied — jitter is not being applied")
	}
}

// Spacing exists so a burst of sub-queries to one engine does not arrive at
// once. Reserving slots up front is what makes concurrent callers queue rather
// than all wake together.
func TestHostLimiter_SpacesRequestsToOneHost(t *testing.T) {
	l := newHostLimiter(40*time.Millisecond, 0)
	start := time.Now()
	for i := 0; i < 3; i++ {
		if err := l.wait(context.Background(), "search.example"); err != nil {
			t.Fatalf("wait: %v", err)
		}
	}
	// Three requests: the first is immediate, the next two are spaced.
	if elapsed := time.Since(start); elapsed < 80*time.Millisecond {
		t.Errorf("three requests took %v, want at least two intervals", elapsed)
	}
}

// Different hosts must not queue behind each other — a slow engine would
// otherwise hold up the next one in the chain.
func TestHostLimiter_DoesNotSpaceAcrossHosts(t *testing.T) {
	l := newHostLimiter(200*time.Millisecond, 0)
	start := time.Now()
	for _, h := range []string{"a.example", "b.example", "c.example"} {
		if err := l.wait(context.Background(), h); err != nil {
			t.Fatalf("wait: %v", err)
		}
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("distinct hosts waited %v on each other", elapsed)
	}
}

func TestHostLimiter_RespectsCancellation(t *testing.T) {
	l := newHostLimiter(time.Hour, 0)
	ctx, cancel := context.WithCancel(context.Background())
	_ = l.wait(ctx, "slow.example") // takes the immediate slot
	cancel()
	if err := l.wait(ctx, "slow.example"); err == nil {
		t.Error("wait ignored a cancelled context")
	}
}

func TestParseSearxngResults(t *testing.T) {
	// Dedup matches the scrapers': dedupKey only normalises a trailing slash,
	// so "…/doc" and "…/doc/" are one result and a differing query string is
	// deliberately a different page.
	body, _ := json.Marshal(map[string]any{"results": []map[string]string{
		{"url": "https://go.dev/doc", "title": "Docs", "content": "The Go docs"},
		{"url": "https://go.dev/doc/", "title": "Dup", "content": "same page"},
		{"url": "javascript:void(0)", "title": "Junk", "content": "not a page"},
		{"url": "https://pkg.go.dev/context", "title": "context", "content": "package context"},
	}})

	got, err := parseSearxngResults(body, 10)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2 (a trailing-slash dup and a non-http entry dropped): %+v", len(got), got)
	}
	if got[0].URL != "https://go.dev/doc" || got[0].Title != "Docs" {
		t.Errorf("first result = %+v", got[0])
	}
}

func TestParseSearxngResults_RespectsLimit(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"results": []map[string]string{
		{"url": "https://a.example", "title": "a"},
		{"url": "https://b.example", "title": "b"},
		{"url": "https://c.example", "title": "c"},
	}})
	got, err := parseSearxngResults(body, 2)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d results, want the limit of 2", len(got))
	}
}

// The likeliest misconfiguration is an instance with the JSON format switched
// off, which answers with the HTML page. The error has to name that, or the
// user reads an unmarshal error against a wall of markup.
func TestParseSearxngResults_ExplainsHTMLResponse(t *testing.T) {
	_, err := parseSearxngResults([]byte("<!doctype html><html><body>results</body></html>"), 5)
	if err == nil {
		t.Fatal("expected an error for an HTML response")
	}
	if !strings.Contains(err.Error(), "json format") {
		t.Errorf("error = %q, want it to point at the json format setting", err)
	}
}

// A SearxNG URL is opt-in, and when it is set it goes first: it is the user's
// own instance, so it neither rate-limits them nor breaks on a vendor reskin.
//
// Which built-ins make up the rest of the chain is pinned separately, by
// TestBuildEngines_ChainOrder — asserting a count here too would mean every
// engine added or removed breaks two tests for one deliberate change.
func TestBuildEngines(t *testing.T) {
	builtin := engineNames(buildEngines(""))
	for _, name := range builtin {
		if name == "searxng" {
			t.Fatal("searxng is in the default chain; it must be opt-in")
		}
	}
	configured := engineNames(buildEngines("https://searx.example.org/"))
	if len(configured) != len(builtin)+1 || configured[0] != "searxng" {
		t.Errorf("configured chain = %v, want searxng ahead of %v", configured, builtin)
	}
}

func engineNames(engines []searchEngine) []string {
	out := make([]string, len(engines))
	for i, e := range engines {
		out[i] = e.name
	}
	return out
}

// A trailing slash in the configured URL must not produce "//search".
func TestSearxngEngine_BuildsCleanEndpoint(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RequestURI()
		fmt.Fprint(w, `{"results":[]}`)
	}))
	defer srv.Close()

	engines := buildEngines(srv.URL + "/")
	if _, err := engines[0].fn(NewNativeBackend(), context.Background(), "go context", 5); err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.HasPrefix(got, "/search?") || strings.Contains(got, "//search") {
		t.Errorf("requested %q, want a single-slash /search path", got)
	}
	if !strings.Contains(got, "format=json") {
		t.Errorf("requested %q, want format=json", got)
	}
}
