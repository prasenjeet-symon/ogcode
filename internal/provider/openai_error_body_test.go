package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestOpenAIStreamChat_400BodyReachesError guards the retry loop's inspection of
// a 400 body. That check reads the body to look for "failed to read request
// body", which consumes it; if it is not restored, the generic error path below
// builds an APIError with an empty Body. The user then sees a bare
// "<provider> API error 400: " with the provider's actual complaint gone, and
// the empty body used to classify the failure as a context-window overflow —
// so a first "hello" reported the conversation as too large.
func TestOpenAIStreamChat_400BodyReachesError(t *testing.T) {
	const body = `{"error":{"message":"model not found","code":400}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	p := &OpenAIProvider{id: "ogcode-openrouter", baseURL: srv.URL, model: "test-model"}
	_, err := p.StreamChat(context.Background(), StreamRequest{
		Model:    "test-model",
		Messages: []ModelMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}},
	})
	if err == nil {
		t.Fatal("StreamChat returned no error for a 400 response")
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not *APIError: %T %v", err, err)
	}
	if apiErr.Body != body {
		t.Errorf("APIError.Body = %q, want %q", apiErr.Body, body)
	}
	if !strings.Contains(err.Error(), "model not found") {
		t.Errorf("error text lost the provider's message: %q", err.Error())
	}
	if apiErr.IsContextLength() {
		t.Error("a 400 that names a missing model was classified as a context-window overflow")
	}
}

// TestOpenAIStreamChat_RetriesTransientBodyError checks the branch the body
// inspection exists for: "failed to read request body" is retried, and the
// eventual success is returned normally.
func TestOpenAIStreamChat_RetriesTransientBodyError(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("failed to read request body"))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	p := &OpenAIProvider{id: "test", baseURL: srv.URL, model: "test-model"}
	ch, err := p.StreamChat(context.Background(), StreamRequest{
		Model:    "test-model",
		Messages: []ModelMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}},
	})
	if err != nil {
		t.Fatalf("StreamChat returned error after a retryable 400: %v", err)
	}
	for range ch {
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
}

// TestOpenRouterAttributionHeaders covers the attribution gate. It keyed on the
// provider id "openrouter", so the community free pool — whose id is
// "ogcode-openrouter" — reached the same endpoint without the headers and went
// unattributed. The gate is the base URL now, so every id that points at
// OpenRouter is credited, and other endpoints still stay clean.
func TestOpenRouterAttributionHeaders(t *testing.T) {
	cases := []struct {
		name    string
		id      string
		baseURL string
		want    bool
	}{
		{"user's own openrouter provider", "openrouter", "https://openrouter.ai/api/v1", true},
		{"free pool openrouter provider", "ogcode-openrouter", "https://openrouter.ai/api/v1", true},
		{"openai provider aimed at openrouter", "openai", "https://OpenRouter.ai/api/v1", true},
		{"local ollama endpoint", "ollama", "http://localhost:11434/v1", false},
		{"openai proper", "openai", "https://api.openai.com/v1", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &OpenAIProvider{id: c.id, baseURL: c.baseURL, model: "m", apiKey: "k"}
			if got := p.isOpenRouter(); got != c.want {
				t.Fatalf("isOpenRouter(%q) = %v, want %v", c.baseURL, got, c.want)
			}

			req, err := http.NewRequest("POST", c.baseURL+"/chat/completions", nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			p.setChatHeaders(req)

			referer, title := req.Header.Get("HTTP-Referer"), req.Header.Get("X-Title")
			if c.want {
				if referer != "https://ogcode.xyz" || title != "ogcode" {
					t.Errorf("attribution headers missing: referer=%q title=%q", referer, title)
				}
			} else if referer != "" || title != "" {
				t.Errorf("attribution sent to a non-OpenRouter endpoint: referer=%q title=%q", referer, title)
			}

			if got := req.Header.Get("Authorization"); got != "Bearer k" {
				t.Errorf("Authorization = %q, want %q", got, "Bearer k")
			}
			if got := req.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
		})
	}
}
