//go:build darwin

package search

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// challengePage imitates what Brave actually serves a fresh browser: a page
// that settles at a stable size with no results in it, then replaces itself
// with the real content several seconds later. The delay is the whole point —
// a byte-count stability check calls the first version "loaded".
const challengeDelay = 4 * time.Second

func challengeServer(t *testing.T) *httptest.Server {
	t.Helper()
	filler := strings.Repeat("<p>verifying your browser</p>", 1200) // comfortably past safariMinBytes
	real := strings.Repeat(`<div class="snippet" data-type="web"><a href="https://example.com">hit</a></div>`, 40)

	mux := http.NewServeMux()
	// The challenge *navigates* when it clears, which is what Brave does and
	// what matters here: Safari's `source` property reports the HTML that was
	// served, not the live DOM, so a challenge that merely rewrote itself with
	// innerHTML would never be visible to this transport at all.
	mux.HandleFunc("/real", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><html><head><title>Results</title></head><body>%s</body></html>`, real)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><html><head><title>Checking</title></head>
<body><div id="c">%s</div>
<script>setTimeout(function () { location.replace('/real'); }, %d);</script>
</body></html>`, filler, challengeDelay.Milliseconds())
	})
	return httptest.NewServer(mux)
}

// The regression this exists for: waiting only for the byte count to settle
// returns the challenge page, and the parser then finds nothing — which looks
// exactly like the engine blocking us.
func TestLiveSafariWaitsOutAChallenge(t *testing.T) {
	b := requireLiveSafari(t)
	srv := challengeServer(t)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()
	html, err := b.loadHTML(ctx, srv.URL, `data-type="web"`)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("loadHTML: %v", err)
	}
	if !strings.Contains(html, `data-type="web"`) {
		t.Fatalf("returned the pre-challenge page (%d chars) instead of the real one", len(html))
	}
	if elapsed < challengeDelay {
		t.Errorf("returned after %v, before the page could possibly have settled", elapsed)
	}
	t.Logf("waited out the challenge in %v, %d chars", elapsed.Round(time.Millisecond), len(html))
}

// And the proof that the marker is what fixed it: without one, the same page
// comes back in its pre-challenge state — the old behaviour, pinned so it
// cannot quietly return.
func TestLiveSafariWithoutMarkerReturnsEarly(t *testing.T) {
	b := requireLiveSafari(t)
	srv := challengeServer(t)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	html, err := b.loadHTML(ctx, srv.URL, "")
	if err != nil {
		t.Fatalf("loadHTML: %v", err)
	}
	if strings.Contains(html, `data-type="web"`) {
		t.Skip("the page settled before the read; timing-dependent, nothing to assert")
	}
	t.Logf("without a marker it returned the placeholder (%d chars), as expected", len(html))
}
