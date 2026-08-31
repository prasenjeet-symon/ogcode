package search

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// liveTestEnv gates the tests below. They talk to real search engines, so they
// are opt-in: they are slow, they fail on a flaky network, and hammering the
// engines from CI is exactly what gets an IP rate-limited.
//
//	OGCODE_LIVE_SEARCH_TEST=1 go test ./internal/search/ -run Live -v
const liveTestEnv = "OGCODE_LIVE_SEARCH_TEST"

func requireLive(t *testing.T) {
	t.Helper()
	if os.Getenv(liveTestEnv) != "1" {
		t.Skipf("set %s=1 to run live network tests", liveTestEnv)
	}
}

func TestLiveSearch(t *testing.T) {
	requireLive(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	results, err := NewNativeBackend().Search(ctx, "golang context cancellation", 8)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) < 3 {
		t.Fatalf("got %d results, want at least 3", len(results))
	}
	for i, r := range results {
		if !strings.HasPrefix(r.URL, "http") || r.Title == "" {
			t.Errorf("result %d is malformed: %+v", i, r)
		}
		t.Logf("%d. %s — %s", i+1, r.Title, r.URL)
	}
}

func TestLiveFetchPage(t *testing.T) {
	requireLive(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	page, err := NewNativeBackend().FetchPage(ctx, "https://pkg.go.dev/context")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(page.Text) < 2000 {
		t.Errorf("extracted only %d chars: %.200q", len(page.Text), page.Text)
	}
	t.Logf("title=%q chars=%d truncated=%v", page.Title, len(page.Text), page.Truncated)
}

// TestLiveSearchIsOnTopic guards against an engine that answers, parses and
// then returns results for something else entirely.
//
// This is not a hypothetical failure mode. Bing was the original primary engine
// here; over plain HTTP it returns ten well-formed organic results for
// unrelated spam domains — a parse that looks perfectly healthy while feeding
// the research pipeline garbage. Structural assertions cannot see that, so this
// checks the one thing that matters: are the results about the query.
//
// Each engine is asserted separately. Folding them together would let a healthy
// engine mask a poisoned one.
func TestLiveSearchIsOnTopic(t *testing.T) {
	requireLive(t)

	const query = "chromedp set user data dir"
	// Any on-topic result for this query mentions at least one of these.
	wantAny := []string{"chromedp", "chrome", "user-data-dir", "user_data_dir", "userdatadir"}

	// One backend for every engine, so the per-host spacing applies here the
	// same way it does in a real run.
	b := NewNativeBackend()
	for _, e := range b.engines {
		t.Run(e.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			results, err := e.fn(b, ctx, query, 5)
			if err != nil {
				// This test hits one engine directly, bypassing the fallback
				// chain that Search() would use, so a transient rate-limit or
				// network blip here says nothing about relevance — which is the
				// only thing this test exists to measure. Skip those; a
				// genuinely broken engine still fails.
				if isTransientEngineError(err) {
					t.Skipf("engine temporarily unavailable: %v", err)
				}
				t.Fatalf("search: %v", err)
			}
			if len(results) == 0 {
				t.Fatal("no results")
			}

			onTopic := 0
			for _, r := range results {
				haystack := strings.ToLower(r.Title + " " + r.URL + " " + r.Snippet)
				for _, want := range wantAny {
					if strings.Contains(haystack, want) {
						onTopic++
						break
					}
				}
			}
			t.Logf("%d/%d results on topic", onTopic, len(results))
			if onTopic*2 < len(results) {
				for i, r := range results {
					t.Logf("    %d. %s | %s", i+1, r.Title, r.URL)
				}
				t.Errorf("only %d of %d results are on topic — engine is not answering the query",
					onTopic, len(results))
			}
		})
	}
}

// isTransientEngineError reports whether err is a rate-limit, upstream outage
// or transport blip rather than a broken parser or a hostile response.
func isTransientEngineError(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, frag := range []string{
		"http 429", "http 503", "http 502", "http 504",
		"timeout", "deadline exceeded", "connection reset", "eof",
	} {
		if strings.Contains(msg, frag) {
			return true
		}
	}
	return false
}
