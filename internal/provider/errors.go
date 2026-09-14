package provider

import (
	"fmt"
	"net/http"
	"regexp"
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
// loop's string-matching fallback so the two never drift. The hints must stay a
// superset of the phrasings contextWindowPatterns matches — the loop learns a
// window from exactly the bodies this function classifies, so a pattern whose
// sample body is not classified is dead code (pinned by
// TestParseContextWindowFromBody_PhrasingsClassifyAsOverflow).
func IsContextLengthMessage(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "too long") ||
		strings.Contains(lower, "context length") ||
		strings.Contains(lower, "maximum context") ||
		strings.Contains(lower, "context_length_exceeded") ||
		strings.Contains(lower, "prompt is too long") ||
		strings.Contains(lower, "exceeds the maximum number of tokens")
}

// minPlausibleContextWindow / maxPlausibleContextWindow bound the windows
// ParseContextWindowFromBody will accept: every shipped model has at least a
// 4k window, and no known endpoint ships a 10M-token window, so anything
// outside the range is a token count of something else (output cap, error id,
// timestamp) misread as a window.
const (
	minPlausibleContextWindow = 4096
	maxPlausibleContextWindow = 10_000_000
)

// ParseContextWindowFromBody extracts the model's context window from a
// context-overflow error body — the one message a provider reliably states the
// figure in ("maximum context length is 8192 tokens", "195000 tokens >
// 200000 maximum"). Returns 0 when nothing plausible is found; the caller
// knows the request that just overflowed, so the sanity check that the window
// exceeds the prompt size lives at the call site, not here. Shared seam: the
// loop calls this on exactly the errors isContextLengthError classified, so
// the phrasings below must stay a superset of IsContextLengthMessage's hints.
func ParseContextWindowFromBody(body string) int {
	lower := strings.ToLower(body)
	// Patterns are ordered most- to least-specific; the first match wins, so a
	// body stating both "8192 tokens" (the input) and "16385" (the cap) in one
	// sentence resolves to the phrase that names the cap, not the largest
	// number on the page.
	for _, p := range contextWindowPatterns {
		if m := p.re.FindStringSubmatch(lower); m != nil {
			n, err := strconv.Atoi(m[1])
			if err != nil {
				continue
			}
			if n >= minPlausibleContextWindow && n <= maxPlausibleContextWindow {
				return n
			}
		}
	}
	return 0
}

// contextWindowPattern pairs a cap-stating phrasing with a real-world sample
// body it matches. The samples are load-bearing: the pin test
// (TestParseContextWindowFromBody_PhrasingsClassifyAsOverflow) asserts every
// sample still classifies as a context overflow via IsContextLengthMessage and
// still parses — the loop only learns from classified overflow errors, so a
// pattern whose sample is no longer classified is dead code, and a sample that
// no longer parses means the phrasing was changed without checking a real body.
type contextWindowPattern struct {
	re     *regexp.Regexp
	sample string
}

// contextWindowPatterns matches the cap-stating phrasings of the major
// providers (capture group 1 = the window in tokens). Compiled once.
var contextWindowPatterns = []contextWindowPattern{
	// OpenAI and Fireworks: "This model's maximum context length is 8192
	// tokens" / "... maximum context length of 131072 tokens. However, you
	// requested ...".
	{
		re:     regexp.MustCompile(`maximum context length (?:is|of) (\d+) tokens`),
		sample: "this model's maximum context length is 8192 tokens. however, you requested 9000 tokens (9001) in the messages",
	},
	// Anthropic: "prompt is too long: 195000 tokens > 200000 maximum".
	{
		re:     regexp.MustCompile(`tokens > (\d+) maximum`),
		sample: "prompt is too long: 195000 tokens > 200000 maximum",
	},
	// Gemini: "The input token count (250000) exceeds the maximum number of
	// tokens allowed (200000)."
	{
		re:     regexp.MustCompile(`maximum number of tokens allowed \((\d+)\)`),
		sample: "the input token count (250000) exceeds the maximum number of tokens allowed (200000)",
	},
	// Generic OpenAI-compatible mirrors: "prompt is too long: it exceeds the
	// maximum of 8192 tokens".
	{
		re:     regexp.MustCompile(`maximum of (\d+) tokens`),
		sample: "prompt is too long: it exceeds the maximum of 8192 tokens",
	},
	// Bare overflow statements seen from smaller endpoints: "context length
	// exceeds 4096 tokens".
	{
		re:     regexp.MustCompile(`context length exceeds (\d+)`),
		sample: "request failed: context length exceeds 4096 tokens",
	},
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
