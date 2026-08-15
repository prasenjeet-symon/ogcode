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
		code int
		body string
		want bool
	}{
		{400, "This model's maximum context length is 8192 tokens", true},
		{400, `{"error":{"code":"context_length_exceeded"}}`, true},
		{400, "prompt is too long", true},
		{400, "", true}, // Ollama empty-body overflow
		{400, `{"error":"invalid api key format"}`, false},
		{429, "context length exceeded", false}, // not a 400 → not a context error
		{500, "", false},
	}
	for _, c := range cases {
		if got := (&APIError{StatusCode: c.code, Body: c.body}).IsContextLength(); got != c.want {
			t.Errorf("IsContextLength(%d,%q) = %v, want %v", c.code, c.body, got, c.want)
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
