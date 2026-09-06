package search

import (
	"context"
	"fmt"
	"sync"
)

// SwitchableBackend is a Backend whose underlying implementation can be replaced
// at runtime. The web_search / fetch_page tools and the deep-research pipeline
// hold this stable wrapper, so changing the search provider in settings takes
// effect on the next call — no restart, no re-registering tools.
//
// Swaps are guarded by a read-write mutex: searches take the read lock (many can
// run at once), a Set takes the write lock briefly. A search already in flight
// keeps running on the backend it captured; only calls that start after the swap
// see the new one.
type SwitchableBackend struct {
	mu    sync.RWMutex
	inner Backend
}

var _ Backend = (*SwitchableBackend)(nil)

// NewSwitchableBackend returns a switchable wrapper around b.
func NewSwitchableBackend(b Backend) *SwitchableBackend {
	return &SwitchableBackend{inner: b}
}

// Set replaces the active backend. Safe to call concurrently with Search and
// FetchPage.
func (s *SwitchableBackend) Set(b Backend) {
	s.mu.Lock()
	s.inner = b
	s.mu.Unlock()
}

func (s *SwitchableBackend) get() Backend {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.inner
}

// Name reports the active backend's name, or "none" before one is configured.
// A live swap changes what this returns on the next call, in step with what
// Search and FetchPage then answer.
func (s *SwitchableBackend) Name() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.inner == nil {
		return "none"
	}
	return s.inner.Name()
}

func (s *SwitchableBackend) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	b := s.get()
	if b == nil {
		return nil, fmt.Errorf("search: no backend configured")
	}
	return b.Search(ctx, query, limit)
}

func (s *SwitchableBackend) FetchPage(ctx context.Context, url string) (PageContent, error) {
	b := s.get()
	if b == nil {
		return PageContent{}, fmt.Errorf("fetch %s: no search backend configured", url)
	}
	return b.FetchPage(ctx, url)
}
