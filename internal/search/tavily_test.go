package search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestTavily points a backend at a stub server instead of the real API.
func newTestTavily(url string) *TavilyBackend {
	b := NewTavilyBackend("tvly-test-key")
	b.baseURL = url
	return b
}

func TestTavilySearchParsesResults(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = io.WriteString(w, `{"results":[
			{"title":"First","url":"https://a.example","content":"snippet a"},
			{"title":"Second","url":"https://b.example","content":"snippet b"},
			{"title":"NoURL","url":"","content":"dropped"}
		]}`)
	}))
	defer srv.Close()

	results, err := newTestTavily(srv.URL).Search(context.Background(), "hello", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotAuth != "Bearer tvly-test-key" {
		t.Errorf("auth header = %q, want Bearer tvly-test-key", gotAuth)
	}
	if gotPath != "/search" {
		t.Errorf("path = %q, want /search", gotPath)
	}
	if gotBody["max_results"] != float64(5) {
		t.Errorf("max_results = %v, want 5", gotBody["max_results"])
	}
	// The result with an empty URL is dropped.
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Title != "First" || results[0].URL != "https://a.example" || results[0].Snippet != "snippet a" {
		t.Errorf("first result mapped wrong: %+v", results[0])
	}
}

func TestTavilySearchClampsLimit(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = io.WriteString(w, `{"results":[]}`)
	}))
	defer srv.Close()

	if _, err := newTestTavily(srv.URL).Search(context.Background(), "q", 999); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotBody["max_results"] != float64(tavilyMaxResults) {
		t.Errorf("max_results = %v, want clamp to %d", gotBody["max_results"], tavilyMaxResults)
	}
}

func TestTavilyFetchPageParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/extract" {
			t.Errorf("path = %q, want /extract", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"results":[{"url":"https://a.example/post","raw_content":"# Title\n\nBody text."}],"failed_results":[]}`)
	}))
	defer srv.Close()

	page, err := newTestTavily(srv.URL).FetchPage(context.Background(), "https://www.a.example/post")
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	if !strings.Contains(page.Text, "Body text.") {
		t.Errorf("text missing body: %q", page.Text)
	}
	// Markdown structure (the blank line) is preserved, not flattened.
	if !strings.Contains(page.Text, "\n") {
		t.Errorf("expected newlines preserved, got %q", page.Text)
	}
	if page.Title != "a.example/post" {
		t.Errorf("title = %q, want a.example/post", page.Title)
	}
}

func TestTavilyFetchPageReportsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"results":[],"failed_results":[{"url":"https://x","error":"paywall"}]}`)
	}))
	defer srv.Close()

	_, err := newTestTavily(srv.URL).FetchPage(context.Background(), "https://x")
	if err == nil || !strings.Contains(err.Error(), "paywall") {
		t.Fatalf("want error mentioning paywall, got %v", err)
	}
}

func TestTavilyRejectedKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"detail":"unauthorized"}`)
	}))
	defer srv.Close()

	_, err := newTestTavily(srv.URL).Search(context.Background(), "q", 3)
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("want a 'rejected' error on 401, got %v", err)
	}
}

func TestTavilyMissingKey(t *testing.T) {
	if err := ValidateTavilyKey(context.Background(), "   "); err == nil {
		t.Fatal("expected error for blank key")
	}
}

// The provider stamp is how web_search and fetch_page show which backend
// answered; Tavily must leave it on both kinds of answer.
func TestTavilyStampsProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"results":[{"title":"First","url":"https://a.example","content":"snippet a"}]}`)
	}))
	defer srv.Close()
	b := newTestTavily(srv.URL)

	results, err := b.Search(context.Background(), "q", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for i, r := range results {
		if r.Provider != ProviderTavily {
			t.Errorf("result %d provider = %q, want %q", i, r.Provider, ProviderTavily)
		}
	}

	extract := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"results":[{"url":"https://a.example/post","raw_content":"Body text."}],"failed_results":[]}`)
	}))
	defer extract.Close()
	b = newTestTavily(extract.URL)

	page, err := b.FetchPage(context.Background(), "https://www.a.example/post")
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	if page.Provider != ProviderTavily {
		t.Errorf("page provider = %q, want %q", page.Provider, ProviderTavily)
	}
	if b.Name() != ProviderTavily {
		t.Errorf("Name() = %q, want %q", b.Name(), ProviderTavily)
	}
}
