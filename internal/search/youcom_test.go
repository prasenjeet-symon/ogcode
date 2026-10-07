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

// newTestYoucom points a backend at a stub server instead of the real API.
func newTestYoucom(url string) *YoucomBackend {
	b := NewYoucomBackend("ydc-test-key")
	b.baseURL = url
	return b
}

func TestYoucomSearchParsesResults(t *testing.T) {
	var gotKey, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = io.WriteString(w, `{"results":{"web":[
			{"title":"First","url":"https://a.example","description":"snippet a"},
			{"title":"Second","url":"https://b.example","snippets":["snippet b1","snippet b2"]},
			{"title":"NoURL","url":"","description":"dropped"}
		]}}`)
	}))
	defer srv.Close()

	results, err := newTestYoucom(srv.URL).Search(context.Background(), "hello", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotKey != "ydc-test-key" {
		t.Errorf("X-API-Key header = %q, want ydc-test-key", gotKey)
	}
	if gotPath != "/v1/search" {
		t.Errorf("path = %q, want /v1/search", gotPath)
	}
	if gotBody["count"] != float64(5) {
		t.Errorf("count = %v, want 5", gotBody["count"])
	}
	// The result with an empty URL is dropped.
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Title != "First" || results[0].URL != "https://a.example" || results[0].Snippet != "snippet a" {
		t.Errorf("first result mapped wrong: %+v", results[0])
	}
	// A result without a description falls back to joining its snippets.
	if results[1].Snippet != "snippet b1 … snippet b2" {
		t.Errorf("snippet fallback = %q, want joined snippets", results[1].Snippet)
	}
}

func TestYoucomSearchClampsLimit(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = io.WriteString(w, `{"results":{"web":[]}}`)
	}))
	defer srv.Close()

	if _, err := newTestYoucom(srv.URL).Search(context.Background(), "q", 999); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotBody["count"] != float64(youcomMaxResults) {
		t.Errorf("count = %v, want clamp to %d", gotBody["count"], youcomMaxResults)
	}
}

func TestYoucomFetchPageParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/contents" {
			t.Errorf("path = %q, want /v1/contents", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"url":"https://a.example/post","title":"A Post","markdown":"# Title\n\nBody text."}`)
	}))
	defer srv.Close()

	page, err := newTestYoucom(srv.URL).FetchPage(context.Background(), "https://www.a.example/post")
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
	if page.Title != "A Post" {
		t.Errorf("title = %q, want A Post", page.Title)
	}
}

func TestYoucomFetchPageDerivesTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"markdown":"Body text."}`)
	}))
	defer srv.Close()

	page, err := newTestYoucom(srv.URL).FetchPage(context.Background(), "https://www.a.example/post")
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	// No title in the response: one is derived from the URL, as the Tavily
	// backend does.
	if page.Title != "a.example/post" {
		t.Errorf("title = %q, want a.example/post", page.Title)
	}
}

func TestYoucomFetchPageReportsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"url":"https://x","markdown":null}`)
	}))
	defer srv.Close()

	if _, err := newTestYoucom(srv.URL).FetchPage(context.Background(), "https://x"); err == nil {
		t.Fatal("want an error when the API returns no markdown, got nil")
	}
}

func TestYoucomRejectedKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"detail":"unauthorized"}`)
	}))
	defer srv.Close()

	_, err := newTestYoucom(srv.URL).Search(context.Background(), "q", 3)
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("want a 'rejected' error on 401, got %v", err)
	}
}

func TestYoucomMissingKey(t *testing.T) {
	b := NewYoucomBackend("   ")
	if _, err := b.Search(context.Background(), "q", 3); err == nil || !strings.Contains(err.Error(), "no API key") {
		t.Fatalf("want a 'no API key' error for a blank key, got %v", err)
	}
}

// The provider stamp is how web_search and fetch_page show which backend
// answered; You.com must leave it on both kinds of answer.
func TestYoucomStampsProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"results":{"web":[{"title":"First","url":"https://a.example","description":"snippet a"}]}}`)
	}))
	defer srv.Close()
	b := newTestYoucom(srv.URL)

	results, err := b.Search(context.Background(), "q", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for i, r := range results {
		if r.Provider != ProviderYoucom {
			t.Errorf("result %d provider = %q, want %q", i, r.Provider, ProviderYoucom)
		}
	}

	extract := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"url":"https://a.example/post","markdown":"Body text."}`)
	}))
	defer extract.Close()
	b = newTestYoucom(extract.URL)

	page, err := b.FetchPage(context.Background(), "https://www.a.example/post")
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	if page.Provider != ProviderYoucom {
		t.Errorf("page provider = %q, want %q", page.Provider, ProviderYoucom)
	}
	if b.Name() != ProviderYoucom {
		t.Errorf("Name() = %q, want %q", b.Name(), ProviderYoucom)
	}
}
