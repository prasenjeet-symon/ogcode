package search

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Live You.com checks, opt-in for the same reasons as the native live tests:
// they cost credits and fail on a flaky network.
//
//	YDC_API_KEY=<key> OGCODE_LIVE_SEARCH_TEST=1 go test ./internal/search/ -run TestLiveYoucom -v
func TestLiveYoucom(t *testing.T) {
	requireLive(t)
	key := os.Getenv("YDC_API_KEY")
	if key == "" {
		t.Skip("set YDC_API_KEY to run live You.com tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	b := NewYoucomBackend(key)

	results, err := b.Search(ctx, "golang context cancellation", 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("got 0 results, want at least 1")
	}
	for i, r := range results {
		if !strings.HasPrefix(r.URL, "http") || r.Title == "" {
			t.Errorf("result %d is malformed: %+v", i, r)
		}
		if r.Provider != ProviderYoucom {
			t.Errorf("result %d provider = %q, want %q", i, r.Provider, ProviderYoucom)
		}
	}

	page, err := b.FetchPage(ctx, results[0].URL)
	if err != nil {
		t.Fatalf("fetch %s: %v", results[0].URL, err)
	}
	if strings.TrimSpace(page.Text) == "" {
		t.Errorf("page %s extracted no text", results[0].URL)
	}
	if page.Provider != ProviderYoucom {
		t.Errorf("page provider = %q, want %q", page.Provider, ProviderYoucom)
	}
}
