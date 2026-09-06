package search

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// stubBackend is a Backend that reports a fixed name via each result's Title.
type stubBackend struct{ name string }

func (b stubBackend) Name() string { return b.name }

func (b stubBackend) Search(_ context.Context, _ string, _ int) ([]SearchResult, error) {
	return []SearchResult{{Title: b.name}}, nil
}
func (b stubBackend) FetchPage(_ context.Context, url string) (PageContent, error) {
	return PageContent{URL: url, Title: b.name}, nil
}

func TestSwitchableBackendSwaps(t *testing.T) {
	sw := NewSwitchableBackend(stubBackend{name: "native"})

	res, err := sw.Search(context.Background(), "q", 1)
	if err != nil || len(res) != 1 || res[0].Title != "native" {
		t.Fatalf("before swap: got %+v, %v", res, err)
	}

	sw.Set(stubBackend{name: "tavily"})

	res, err = sw.Search(context.Background(), "q", 1)
	if err != nil || res[0].Title != "tavily" {
		t.Fatalf("after swap: got %+v, %v", res, err)
	}
	page, err := sw.FetchPage(context.Background(), "https://x")
	if err != nil || page.Title != "tavily" {
		t.Fatalf("fetch after swap: got %+v, %v", page, err)
	}
}

func TestSwitchableBackendNilInner(t *testing.T) {
	sw := NewSwitchableBackend(nil)
	if _, err := sw.Search(context.Background(), "q", 1); err == nil {
		t.Fatal("expected error from Search with no backend")
	}
	if _, err := sw.FetchPage(context.Background(), "https://x"); err == nil || !strings.Contains(err.Error(), "no search backend") {
		t.Fatalf("expected 'no search backend' error, got %v", err)
	}
}

// Exercises the race detector: concurrent Set and Search must not race.
func TestSwitchableBackendConcurrent(t *testing.T) {
	sw := NewSwitchableBackend(stubBackend{name: "a"})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); sw.Set(stubBackend{name: "b"}) }()
		go func() { defer wg.Done(); _, _ = sw.Search(context.Background(), "q", 1) }()
	}
	wg.Wait()
}
