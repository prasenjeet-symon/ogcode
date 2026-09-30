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

// YoucomBackend answers web_search and fetch_page through the You.com Web
// Search and Contents APIs (https://you.com/docs) using the user's own API
// key. It implements the same Backend interface as NativeBackend and
// TavilyBackend, so once constructed it is indistinguishable to the tools and
// the deep-research pipeline.
//
// You.com authenticates with a static API key sent in the X-API-Key header;
// there is no OAuth or token refresh to manage. When a call fails — a bad key,
// an exhausted quota, a network blip — the caller wraps this backend with
// NewFallbackBackend(youcom, native) so search transparently falls back to the
// built-in engine rather than going dark.
type YoucomBackend struct {
	apiKey string
	client *http.Client
	// baseURL is the API root, overridable in tests. Defaults to youcomBaseURL.
	baseURL string
}

var _ Backend = (*YoucomBackend)(nil)

const (
	youcomBaseURL = "https://ydc-index.io"

	// Per-operation deadlines. Contents crawls the page server-side, which is
	// heavier than a search, so it gets the longer budget.
	youcomSearchTimeout = 15 * time.Second
	youcomFetchTimeout  = 30 * time.Second

	// You.com caps count at 100 results per section.
	youcomMaxResults = 100

	// Match the native backend's per-page truncation so the fetch_page tool's
	// "[content truncated at 14,000 characters]" note stays accurate regardless
	// of which backend served the page.
	youcomPageChars = nativePageChars

	// Cap the body we read from an error response so a misbehaving endpoint
	// cannot stream an unbounded error into a log line.
	youcomErrBodyCap = 2 << 10
)

// NewYoucomBackend returns a backend that talks to the You.com APIs with
// apiKey. The key is not validated here; a bad key surfaces as an error on the
// first call (and, via the settings "Test key" action, before that).
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
		// The description is the plain result blurb; snippets are the keyword
		// fragments the API returns alongside it. Either can be empty, so the
		// snippet shown to the agent falls back from one to the other.
		snippet := r.Description
		if snippet == "" && len(r.Snippets) > 0 {
			snippet = r.Snippets[0]
		}
		out = append(out, SearchResult{Title: r.Title, URL: r.URL, Snippet: snippet, Provider: ProviderYoucom})
	}
	return out, nil
}

// FetchPage returns the readable content of rawURL via POST /v1/contents,
// asking for the same markdown format the Tavily path prefers.
func (y *YoucomBackend) FetchPage(ctx context.Context, rawURL string) (PageContent, error) {
	reqBody := map[string]any{
		"urls":    []string{rawURL},
		"formats": []string{"markdown"},
	}
	var resp []struct {
		URL      string  `json:"url"`
		Title    string  `json:"title"`
		Markdown *string `json:"markdown"`
	}
	if err := y.post(ctx, "/v1/contents", reqBody, &resp, youcomFetchTimeout); err != nil {
		return PageContent{}, err
	}

	if len(resp) == 0 || resp[0].Markdown == nil || strings.TrimSpace(*resp[0].Markdown) == "" {
		return PageContent{}, fmt.Errorf("fetch %s: you.com returned no readable content", rawURL)
	}

	title := strings.TrimSpace(resp[0].Title)
	if title == "" {
		title = titleFromURL(rawURL)
	}
	// You.com's markdown already carries its own structure, so like the Tavily
	// path this is not run through normalizeWhitespace. Only trim and truncate.
	text, truncated := truncateChars(strings.TrimSpace(*resp[0].Markdown), youcomPageChars)
	return PageContent{URL: rawURL, Title: title, Text: text, Truncated: truncated, Provider: ProviderYoucom}, nil
}

// post sends body as JSON to the You.com endpoint at path and decodes a 200
// response into out. A non-2xx status becomes an error carrying a short snippet
// of the response body, with 401 called out plainly since a bad key is the most
// common misconfiguration.
func (y *YoucomBackend) post(ctx context.Context, path string, body, out any, timeout time.Duration) error {
	if y.apiKey == "" {
		return fmt.Errorf("you.com: no API key configured")
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("you.com: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, y.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("you.com: build request: %w", err)
	}
	req.Header.Set("X-API-Key", y.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := y.client.Do(req)
	if err != nil {
		return fmt.Errorf("you.com %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, youcomErrBodyCap))
		msg := strings.TrimSpace(string(snippet))
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Errorf("you.com %s: the API key was rejected (status %d)", path, resp.StatusCode)
		case http.StatusTooManyRequests:
			return fmt.Errorf("you.com %s: rate limited or out of credits (status %d)", path, resp.StatusCode)
		default:
			if msg != "" {
				return fmt.Errorf("you.com %s: status %d: %s", path, resp.StatusCode, msg)
			}
			return fmt.Errorf("you.com %s: status %d", path, resp.StatusCode)
		}
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("you.com %s: decode response: %w", path, err)
	}
	return nil
}

// ValidateYoucomKey reports whether apiKey is accepted by You.com. It performs
// a single minimal search — You.com has no dedicated auth-check endpoint — so
// a success confirms both that the key is valid and that the account can serve
// requests. Used by the settings "Test key" action before the user restarts.
func ValidateYoucomKey(ctx context.Context, apiKey string) error {
	if strings.TrimSpace(apiKey) == "" {
		return fmt.Errorf("no API key provided")
	}
	b := NewYoucomBackend(apiKey)
	if _, err := b.Search(ctx, "ogcode connectivity check", 1); err != nil {
		return err
	}
	return nil
}
