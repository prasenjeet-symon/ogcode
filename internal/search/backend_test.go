package search

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeBackend records how often it was asked and answers however the test says.
type fakeBackend struct {
	results []SearchResult
	page    PageContent
	err     error

	searches atomic.Int32
	fetches  atomic.Int32
}

func (f *fakeBackend) Search(context.Context, string, int) ([]SearchResult, error) {
	f.searches.Add(1)
	return f.results, f.err
}

func (f *fakeBackend) FetchPage(context.Context, string) (PageContent, error) {
	f.fetches.Add(1)
	return f.page, f.err
}

func oneResult() []SearchResult {
	return []SearchResult{{Title: "t", URL: "https://example.com", Snippet: "s"}}
}

// The point of the ordering: the browser is expensive — several seconds and a
// window on the user's screen — so it must not run when the fast path answered.
func TestFallbackBackend_PrimaryAnswersAlone(t *testing.T) {
	primary := &fakeBackend{results: oneResult()}
	secondary := &fakeBackend{results: oneResult()}

	got, err := NewFallbackBackend(primary, secondary).Search(context.Background(), "q", 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d results, want 1", len(got))
	}
	if n := secondary.searches.Load(); n != 0 {
		t.Errorf("the browser fallback ran %d times for a search the primary answered", n)
	}
}

func TestFallbackBackend_FallsBackOnError(t *testing.T) {
	primary := &fakeBackend{err: errors.New("all search engines failed")}
	secondary := &fakeBackend{results: oneResult()}

	got, err := NewFallbackBackend(primary, secondary).Search(context.Background(), "q", 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d results, want the fallback's 1", len(got))
	}
	if n := secondary.searches.Load(); n != 1 {
		t.Errorf("fallback ran %d times, want 1", n)
	}
}

// An empty result set from the primary is indistinguishable from every engine
// having served a challenge page, so it has to be treated as a failure.
func TestFallbackBackend_FallsBackOnEmptyResults(t *testing.T) {
	primary := &fakeBackend{}
	secondary := &fakeBackend{results: oneResult()}

	got, err := NewFallbackBackend(primary, secondary).Search(context.Background(), "q", 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d results, want the fallback's 1", len(got))
	}
}

// When both paths fail the caller should hear about the one that runs every
// time, not the one that only exists as a backstop.
func TestFallbackBackend_ReportsThePrimaryFailure(t *testing.T) {
	primary := &fakeBackend{err: errors.New("all search engines failed")}
	secondary := &fakeBackend{err: errors.New("safari: the browser cannot be driven on this machine")}

	_, err := NewFallbackBackend(primary, secondary).Search(context.Background(), "q", 5)
	if err == nil {
		t.Fatal("expected an error when both paths fail")
	}
	if !strings.Contains(err.Error(), "all search engines failed") {
		t.Errorf("error = %q, want the primary's failure", err)
	}
}

// A cancelled turn is not a failed search. Opening a browser window to answer a
// question nobody is waiting for is the one outcome worse than no answer.
func TestFallbackBackend_DoesNotFallBackOnCancellation(t *testing.T) {
	primary := &fakeBackend{err: context.Canceled}
	secondary := &fakeBackend{results: oneResult()}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := NewFallbackBackend(primary, secondary).Search(ctx, "q", 5); err == nil {
		t.Error("expected the cancellation to surface")
	}
	if n := secondary.searches.Load(); n != 0 {
		t.Errorf("the browser fallback ran %d times on a cancelled turn", n)
	}
}

// A fetch that succeeded returned the page it was asked for; there is nothing
// for a second attempt to improve on.
func TestFallbackBackend_FetchPagePrefersPrimary(t *testing.T) {
	primary := &fakeBackend{page: PageContent{URL: "https://example.com", Text: "body"}}
	secondary := &fakeBackend{page: PageContent{URL: "https://example.com", Text: "browser body"}}

	got, err := NewFallbackBackend(primary, secondary).FetchPage(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got.Text != "body" {
		t.Errorf("got %q, want the primary's page", got.Text)
	}
	if n := secondary.fetches.Load(); n != 0 {
		t.Errorf("the browser fallback ran %d times for a page the primary returned", n)
	}
}

// The fetches worth retrying in a browser are exactly the ones that failed: a
// bot challenge, or a page that only exists once its scripts have run.
func TestFallbackBackend_FetchPageFallsBackOnError(t *testing.T) {
	primary := &fakeBackend{err: errors.New("http 403 Forbidden")}
	secondary := &fakeBackend{page: PageContent{URL: "https://example.com", Text: "browser body"}}

	got, err := NewFallbackBackend(primary, secondary).FetchPage(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got.Text != "browser body" {
		t.Errorf("got %q, want the fallback's page", got.Text)
	}
}

// Both errors are kept, because "the site refused us" and "the browser could
// not be driven" send the user to entirely different fixes.
func TestFallbackBackend_FetchPageReportsBothFailures(t *testing.T) {
	primary := &fakeBackend{err: errors.New("http 403 Forbidden")}
	secondary := &fakeBackend{err: errSafariUnavailable}

	_, err := NewFallbackBackend(primary, secondary).FetchPage(context.Background(), "https://example.com")
	if err == nil {
		t.Fatal("expected an error when both paths fail")
	}
	for _, want := range []string{"403", "cannot be driven"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}

// Off macOS, NewSafariBackend returns nil and the chain has to collapse rather
// than wrap a nil in a struct that will panic on the first blocked search.
func TestFallbackBackend_NilSecondaryCollapses(t *testing.T) {
	primary := &fakeBackend{results: oneResult()}
	if got := NewFallbackBackend(primary, nil); got != Backend(primary) {
		t.Errorf("a nil secondary should yield the primary unchanged, got %T", got)
	}
	if got := NewFallbackBackend(nil, primary); got != Backend(primary) {
		t.Errorf("a nil primary should yield the secondary unchanged, got %T", got)
	}
}
