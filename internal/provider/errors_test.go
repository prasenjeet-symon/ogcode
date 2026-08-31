package provider

import (
	"net/http"
	"testing"
	"time"
)

func TestAPIError_Error(t *testing.T) {
	e := &APIError{Provider: "anthropic", StatusCode: 429, Body: "slow down"}
	if got, want := e.Error(), "anthropic API error 429: slow down"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestAPIError_IsTransient(t *testing.T) {
	cases := map[int]bool{
		200: false,
		400: false,
		404: false,
		408: false, // request timeout is a 4xx we don't treat as transient here
		429: true,  // rate limited
		500: true,
		502: true,
		503: true,
		529: true, // Anthropic overloaded
	}
	for code, want := range cases {
		if got := (&APIError{StatusCode: code}).IsTransient(); got != want {
			t.Errorf("IsTransient(%d) = %v, want %v", code, got, want)
		}
	}
}

func TestAPIError_IsContextLength(t *testing.T) {
	cases := []struct {
		provider string
		code     int
		body     string
		want     bool
	}{
		{"openai", 400, "This model's maximum context length is 8192 tokens", true},
		{"openai", 400, `{"error":{"code":"context_length_exceeded"}}`, true},
		{"openai", 400, "prompt is too long", true},
		{"ollama", 400, "", true},   // Ollama empty-body overflow
		{"ollama", 400, "  ", true}, // whitespace-only body counts as empty
		// A body-less 400 from a remote provider is an opaque rejection, not an
		// overflow: reporting it as one sends the user compacting a conversation
		// that was never too big.
		{"ogcode-openrouter", 400, "", false},
		{"openai", 400, "", false},
		{"openai", 400, `{"error":"invalid api key format"}`, false},
		{"openai", 429, "context length exceeded", false}, // not a 400 → not a context error
		{"ollama", 500, "", false},
	}
	for _, c := range cases {
		err := &APIError{Provider: c.provider, StatusCode: c.code, Body: c.body}
		if got := err.IsContextLength(); got != c.want {
			t.Errorf("IsContextLength(%s,%d,%q) = %v, want %v", c.provider, c.code, c.body, got, c.want)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	if got := parseRetryAfter("5"); got != 5*time.Second {
		t.Errorf("parseRetryAfter(\"5\") = %v, want 5s", got)
	}
	for _, v := range []string{"", "0", "-3", "not-a-number"} {
		if got := parseRetryAfter(v); got != 0 {
			t.Errorf("parseRetryAfter(%q) = %v, want 0", v, got)
		}
	}
	// HTTP-date in the future yields a positive, bounded duration.
	future := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(future); got <= 0 || got > 31*time.Second {
		t.Errorf("parseRetryAfter(future date) = %v, want ~30s", got)
	}
}

func TestNewAPIError_ParsesRetryAfter(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"12"}},
	}
	e := NewAPIError("openai", resp, "rate limited")
	if e.StatusCode != 429 || e.Provider != "openai" || e.Body != "rate limited" {
		t.Errorf("unexpected APIError fields: %+v", e)
	}
	if e.RetryAfter != 12*time.Second {
		t.Errorf("RetryAfter = %v, want 12s", e.RetryAfter)
	}
	if !e.IsTransient() {
		t.Error("429 should be transient")
	}
}

func TestAPIError_IsImageRejection(t *testing.T) {
	cases := []struct {
		name string
		code int
		body string
		want bool
	}{
		{"image not supported", 400, "this model does not support image input", true},
		{"vision not supported", 400, "model lacks vision capabilities", true},
		{"multimodal error", 400, "multimodal input not accepted", true},
		{"modality error", 400, "unsupported modality", true},
		{"no images", 400, "no images allowed", true},
		{"not support image", 400, "does not support image", true},
		{"generic 400", 400, "invalid request body", false},
		{"context length 400", 400, "prompt is too long", false},
		{"500 not image rejection", 500, "image error", false},
		{"429 not image rejection", 429, "rate limited", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := (&APIError{StatusCode: c.code, Body: c.body}).IsImageRejection()
			if got != c.want {
				t.Errorf("IsImageRejection(%d,%q) = %v, want %v", c.code, c.body, got, c.want)
			}
		})
	}
}
