package provider

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// APIError is a structured error returned by a provider when the LLM API responds
// with a non-2xx status. It carries the HTTP status code and any Retry-After hint
// so the agent loop can classify the failure and back off precisely instead of
// sniffing the error string. Error() preserves the historical
// "<provider> API error <code>: <body>" format, so existing logs and the loop's
// string-matching fallbacks keep working for non-HTTP (stream/network) errors.
type APIError struct {
	Provider   string
	StatusCode int
	RetryAfter time.Duration // parsed from the Retry-After header; 0 if absent
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s API error %d: %s", e.Provider, e.StatusCode, e.Body)
}

// IsTransient reports whether the status is worth retrying: 429 (rate limited),
// 529 (Anthropic overloaded), or any 5xx.
func (e *APIError) IsTransient() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

// IsContextLength reports whether the error is a context-window overflow. These
// arrive as 400s whose body mentions the context length. A bare 400 with an empty
// body counts too, but only from Ollama, which is the one endpoint that answers an
// overflowing prompt that way. Every other provider explains its 400s, so treating
// a body-less one as overflow there just mislabels an unrelated rejection — and
// sends the user off compacting a conversation that was never too big.
func (e *APIError) IsContextLength() bool {
	if e.StatusCode != http.StatusBadRequest {
		return false
	}
	if strings.TrimSpace(e.Body) == "" {
		return e.Provider == "ollama"
	}
	return IsContextLengthMessage(e.Body)
}

// IsImageRejection reports whether the error is a 400 caused by the model not
// accepting image input. The body of such a response mentions image/modality/
// vision support. Used by the agent loop to make these failures resumable
// (resume strips the offending images) instead of fatal, since a non-vision
// model producing a tool image is a capability mismatch, not a malformed
// request — switching models or retrying without images fixes it.
func (e *APIError) IsImageRejection() bool {
	if e.StatusCode != http.StatusBadRequest {
		return false
	}
	return IsImageRejectionMessage(e.Body)
}

// IsImageRejectionMessage reports whether a message/body indicates the model
// does not accept image input. Shared by APIError.IsImageRejection, the probe's
// classifyProbeError, and the loop's string-matching fallback so they never drift.
func IsImageRejectionMessage(s string) bool {
	lower := strings.ToLower(s)
	for _, hint := range imageRejectionHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

// NewAPIError builds an APIError from a non-2xx HTTP response, parsing the
// Retry-After header. The caller supplies the already-read body.
func NewAPIError(providerID string, resp *http.Response, body string) *APIError {
	var retryAfter time.Duration
	if resp != nil {
		retryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
	}
	return &APIError{
		Provider:   providerID,
		StatusCode: resp.StatusCode,
		RetryAfter: retryAfter,
		Body:       body,
	}
}

// IsContextLengthMessage reports whether a message/body indicates the prompt
// exceeded the model's context window. Shared by APIError.IsContextLength and the
// loop's string-matching fallback so the two never drift.
func IsContextLengthMessage(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "too long") ||
		strings.Contains(lower, "context length") ||
		strings.Contains(lower, "maximum context") ||
		strings.Contains(lower, "context_length_exceeded") ||
		strings.Contains(lower, "prompt is too long")
}

// parseRetryAfter parses a Retry-After header value, which per RFC 7231 is either
// an integer number of seconds or an HTTP-date. Returns 0 when absent, negative,
// or unparseable.
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
