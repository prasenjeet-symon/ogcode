package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// YoucomBackend answers web_search and fetch_page through the You.com Search
// and Contents APIs (https://you.com) using the user's own API key. It
// implements the same Backend interface as NativeBackend, so once constructed
// it is indistinguishable to the tools and the deep-research pipeline.
//
// You.com authenticates with a static key sent in the X-API-Key header; there
// is no OAuth or token refresh to manage. When a call fails — a bad key, an
// exhausted quota, a network blip — the caller wraps this backend with
// NewFallbackBackend(youcom, native) so search transparently falls back to the
// built-in engines rather than going dark.
//
// The key is supplied through the environment (YDC_API_KEY) rather than stored
// config, so it travels with the deployment instead of the database.
type YoucomBackend struct {
	apiKey string
	client *http.Client
	// baseURL is the API root, overridable in tests. Defaults to youcomBaseURL.
	baseURL string
}

var _ Backend = (*YoucomBackend)(nil)

const (
	youcomBaseURL = "https://ydc-index.io"

	// Per-operation deadlines, mirroring the Tavily backend: extraction is
	// heavier than a search, so it gets the longer budget.
	youcomSearchTimeout = 15 * time.Second
	youcomFetchTimeout  = 30 * time.Second

	// Cap the requested result count. You.com defaults to 10; anything larger
	// is clamped rather than sent as-is.
	youcomMaxResults = 20

	// Match the native backend's per-page truncation so the fetch_page tool's
	// "[content truncated at 14,000 characters]" note stays accurate
	// regardless of which backend served the page.
	youcomPageChars = nativePageChars

	// Cap the body we read from an error response so a misbehaving endpoint
	// cannot stream an unbounded error into a log line.
	youcomErrBodyCap = 2 << 10
)

// NewYoucomBackend returns a backend that talks to the You.com API with
// apiKey. The key is not validated here; a bad key surfaces as an error on the
// first call.
func NewYoucomBackend(apiKey string) *YoucomBackend {
	return &YoucomBackend{
		apiKey:  strings.TrimSpace(apiKey),
		client:  &http.Client{Timeout: youcomFetchTimeout + 5*time.Second},
		baseURL: youcomBaseURL,
	}
}

// Name reports the provider name stamped on this backend's results.
func (y *YoucomBackend) Name() string { return ProviderYoucom }

// Search returns up to limit results for query via POST /v1/search.
func (y *YoucomBackend) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 8
	}
	if limit > youcomMaxResults {
		limit = youcomMaxResults
	}

	reqBody := map[string]any{
		"query": query,
		"count": limit,
	}
	var resp struct {
		Results struct {
			Web []struct {
				Title       string   `json:"title"`
				URL         string   `json:"url"`
				Description string   `json:"description"`
				Snippets    []string `json:"snippets"`
			} `json:"web"`
		} `json:"results"`
	}
	if err := y.post(ctx, "/v1/search", reqBody, &resp, youcomSearchTimeout); err != nil {
		return nil, err
	}

	out := make([]SearchResult, 0, len(resp.Results.Web))
	for _, r := range resp.Results.Web {
		if r.URL == "" {
			continue
		}
		// description is the one-line summary; the snippets array is the longer
		// form. Use whichever the response carried.
		snippet := r.Description
		if snippet == "" && len(r.Snippets) > 0 {
			snippet = strings.Join(r.Snippets, " … ")
		}
		out = append(out, SearchResult{Title: r.Title, URL: r.URL, Snippet: snippet, Provider: ProviderYoucom})
	}
	return out, nil
}

// FetchPage returns the readable content of url via POST /v1/contents.
func (y *YoucomBackend) FetchPage(ctx context.Context, rawURL string) (PageContent, error) {
	reqBody := map[string]any{
		"urls":    []string{rawURL},
		"formats": []string{"markdown"},
	}
	var resp struct {
		URL      string  `json:"url"`
		Title    string  `json:"title"`
		Markdown *string `json:"markdown"`
	}
	if err := y.post(ctx, "/v1/contents", reqBody, &resp, youcomFetchTimeout); err != nil {
		return PageContent{}, err
	}

	if resp.Markdown == nil || strings.TrimSpace(*resp.Markdown) == "" {
		return PageContent{}, fmt.Errorf("fetch %s: youcom returned no readable content", rawURL)
	}

	title := resp.Title
	if title == "" {
		title = titleFromURL(rawURL)
	}

	// You.com's markdown already carries its own structure, so like the Tavily
	// path this is not run through normalizeWhitespace. Only trim and truncate.
	text, truncated := truncateChars(strings.TrimSpace(*resp.Markdown), youcomPageChars)
	return PageContent{URL: rawURL, Title: title, Text: text, Truncated: truncated, Provider: ProviderYoucom}, nil
}

// post sends body as JSON to the You.com endpoint at path and decodes a 200
// response into out. A non-2xx status becomes an error carrying a short
// snippet of the response body, with 401 called out plainly since a bad key is
// the most common misconfiguration.
func (y *YoucomBackend) post(ctx context.Context, path string, body, out any, timeout time.Duration) error {
	if y.apiKey == "" {
		return fmt.Errorf("youcom: no API key configured")
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("youcom: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, y.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("youcom: build request: %w", err)
	}
	req.Header.Set("X-API-Key", y.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := y.client.Do(req)
	if err != nil {
		return fmt.Errorf("youcom %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, youcomErrBodyCap))
		msg := strings.TrimSpace(string(snippet))
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Errorf("youcom %s: the API key was rejected (status %d)", path, resp.StatusCode)
		case http.StatusTooManyRequests:
			return fmt.Errorf("youcom %s: rate limited or out of credits (status %d)", path, resp.StatusCode)
		default:
			if msg != "" {
				return fmt.Errorf("youcom %s: status %d: %s", path, resp.StatusCode, msg)
			}
			return fmt.Errorf("youcom %s: status %d", path, resp.StatusCode)
		}
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("youcom %s: decode response: %w", path, err)
	}
	return nil
}
