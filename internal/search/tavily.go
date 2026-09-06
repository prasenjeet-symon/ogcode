package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TavilyBackend answers web_search and fetch_page through the Tavily API
// (https://tavily.com) using the user's own API key. It implements the same
// Backend interface as NativeBackend, so once constructed it is
// indistinguishable to the tools and the deep-research pipeline.
//
// Tavily authenticates with a static bearer key (tvly-…); there is no OAuth or
// token refresh to manage. When a call fails — a bad key, an exhausted quota, a
// network blip — the caller wraps this backend with NewFallbackBackend(tavily,
// native) so search transparently falls back to the built-in engine rather than
// going dark.
type TavilyBackend struct {
	apiKey string
	client *http.Client
	// baseURL is the API root, overridable in tests. Defaults to tavilyBaseURL.
	baseURL string
}

var _ Backend = (*TavilyBackend)(nil)

const (
	tavilyBaseURL = "https://api.tavily.com"

	// Per-operation deadlines. Extract with the default depth is heavier than a
	// search, so it gets the longer budget.
	tavilySearchTimeout = 15 * time.Second
	tavilyFetchTimeout  = 30 * time.Second

	// Tavily caps max_results at 20.
	tavilyMaxResults = 20

	// Match the native backend's per-page truncation so the fetch_page tool's
	// "[content truncated at 14,000 characters]" note stays accurate regardless
	// of which backend served the page.
	tavilyPageChars = nativePageChars

	// Cap the body we read from an error response so a misbehaving endpoint
	// cannot stream an unbounded error into a log line.
	tavilyErrBodyCap = 2 << 10
)

// NewTavilyBackend returns a backend that talks to the Tavily API with apiKey.
// The key is not validated here; a bad key surfaces as an error on the first
// call (and, via the settings "Test key" action, before that).
func NewTavilyBackend(apiKey string) *TavilyBackend {
	return &TavilyBackend{
		apiKey:  strings.TrimSpace(apiKey),
		client:  &http.Client{Timeout: tavilyFetchTimeout + 5*time.Second},
		baseURL: tavilyBaseURL,
	}
}

// Name reports the provider name stamped on this backend's results.
func (t *TavilyBackend) Name() string { return ProviderTavily }

// Search returns up to limit results for query via POST /search.
func (t *TavilyBackend) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 8
	}
	if limit > tavilyMaxResults {
		limit = tavilyMaxResults
	}

	reqBody := map[string]any{
		"query":        query,
		"max_results":  limit,
		"search_depth": "basic",
	}
	var resp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := t.post(ctx, "/search", reqBody, &resp, tavilySearchTimeout); err != nil {
		return nil, err
	}

	out := make([]SearchResult, 0, len(resp.Results))
	for _, r := range resp.Results {
		if r.URL == "" {
			continue
		}
		out = append(out, SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Content, Provider: ProviderTavily})
	}
	return out, nil
}

// FetchPage returns the readable content of url via POST /extract.
func (t *TavilyBackend) FetchPage(ctx context.Context, rawURL string) (PageContent, error) {
	reqBody := map[string]any{
		"urls":          rawURL,
		"format":        "markdown",
		"extract_depth": "basic",
	}
	var resp struct {
		Results []struct {
			URL        string `json:"url"`
			RawContent string `json:"raw_content"`
		} `json:"results"`
		FailedResults []struct {
			URL   string `json:"url"`
			Error string `json:"error"`
		} `json:"failed_results"`
	}
	if err := t.post(ctx, "/extract", reqBody, &resp, tavilyFetchTimeout); err != nil {
		return PageContent{}, err
	}

	if len(resp.Results) == 0 || strings.TrimSpace(resp.Results[0].RawContent) == "" {
		if len(resp.FailedResults) > 0 && resp.FailedResults[0].Error != "" {
			return PageContent{}, fmt.Errorf("fetch %s: tavily could not extract it: %s", rawURL, resp.FailedResults[0].Error)
		}
		return PageContent{}, fmt.Errorf("fetch %s: tavily returned no readable content", rawURL)
	}

	// Tavily's markdown already carries its own structure, so unlike the native
	// path this is not run through normalizeWhitespace (which would flatten every
	// paragraph onto one line). Only trim and truncate.
	text, truncated := truncateChars(strings.TrimSpace(resp.Results[0].RawContent), tavilyPageChars)
	return PageContent{URL: rawURL, Title: titleFromURL(rawURL), Text: text, Truncated: truncated, Provider: ProviderTavily}, nil
}

// post sends body as JSON to the Tavily endpoint at path and decodes a 200
// response into out. A non-2xx status becomes an error carrying a short snippet
// of the response body, with 401 called out plainly since a bad key is the most
// common misconfiguration.
func (t *TavilyBackend) post(ctx context.Context, path string, body, out any, timeout time.Duration) error {
	if t.apiKey == "" {
		return fmt.Errorf("tavily: no API key configured")
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("tavily: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("tavily: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("tavily %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, tavilyErrBodyCap))
		msg := strings.TrimSpace(string(snippet))
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Errorf("tavily %s: the API key was rejected (status %d)", path, resp.StatusCode)
		case http.StatusTooManyRequests:
			return fmt.Errorf("tavily %s: rate limited or out of credits (status %d)", path, resp.StatusCode)
		default:
			if msg != "" {
				return fmt.Errorf("tavily %s: status %d: %s", path, resp.StatusCode, msg)
			}
			return fmt.Errorf("tavily %s: status %d", path, resp.StatusCode)
		}
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("tavily %s: decode response: %w", path, err)
	}
	return nil
}

// ValidateTavilyKey reports whether apiKey is accepted by Tavily. It performs a
// single minimal search — Tavily has no dedicated auth-check endpoint — so a
// success confirms both that the key is valid and that the account can serve
// requests. Used by the settings "Test key" action before the user restarts.
func ValidateTavilyKey(ctx context.Context, apiKey string) error {
	if strings.TrimSpace(apiKey) == "" {
		return fmt.Errorf("no API key provided")
	}
	b := NewTavilyBackend(apiKey)
	if _, err := b.Search(ctx, "ogcode connectivity check", 1); err != nil {
		return err
	}
	return nil
}

// titleFromURL derives a human-readable title from a URL, since Tavily's extract
// response carries no page title. Falls back to the raw URL when it cannot be
// parsed.
func titleFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL
	}
	host := strings.TrimPrefix(u.Host, "www.")
	path := strings.Trim(u.Path, "/")
	if path == "" {
		return host
	}
	return host + "/" + path
}
