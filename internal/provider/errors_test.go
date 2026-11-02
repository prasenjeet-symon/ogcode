package provider

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestAPIError_IsMalformedBody pins the gateway's terse 400 phrasing: only a
// 400 carrying it is the unreadable-body error, so no other 400 is rewritten as
// a size hint the user would act on wrongly.
func TestAPIError_IsMalformedBody(t *testing.T) {
	cases := []struct {
		name string
		code int
		body string
		want bool
	}{
		{"gateway unreadable body", 400, `{"error":"invalid request body"}`, true},
		{"other 400", 400, `{"error":"model not found"}`, false},
		{"same text on 500", 500, `{"error":"invalid request body"}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := &APIError{StatusCode: c.code, Body: c.body}
			if got := e.IsMalformedBody(); got != c.want {
				t.Errorf("IsMalformedBody(%d, %q) = %v, want %v", c.code, c.body, got, c.want)
			}
		})
	}
}

// TestCheckBodyFits pins the outgoing OGX size guard: the gateway rejects a
// body over its cap with an opaque 400, so the provider refuses first, with an
// error naming the limit and the usual cause. Every other provider (and a body
// exactly at the limit) is left alone.
func TestCheckBodyFits(t *testing.T) {
	if ogxMaxRequestBytes != 20<<20 {
		t.Fatalf("ogxMaxRequestBytes = %d, want %d — it mirrors nginx's client_max_body_size 20m on the gateway vhost", ogxMaxRequestBytes, 20<<20)
	}
	ogx := &OpenAIProvider{id: OGXProviderID}
	if err := ogx.checkBodyFits(ogxMaxRequestBytes); err != nil {
		t.Errorf("body at the limit refused: %v", err)
	}
	err := ogx.checkBodyFits(ogxMaxRequestBytes + 1)
	if !errors.Is(err, ErrRequestTooLarge) {
		t.Fatalf("over-limit body = %v, want ErrRequestTooLarge", err)
	}
	if !strings.Contains(err.Error(), "MiB") {
		t.Errorf("error %q does not name the limit", err)
	}

	other := &OpenAIProvider{id: "openai", baseURL: "https://api.openai.com/v1"}
	if err := other.checkBodyFits(ogxMaxRequestBytes * 2); err != nil {
		t.Errorf("non-OGX provider refused: %v", err)
	}

	// The gateway host is recognised even when the provider id is not ogx.
	byHost := &OpenAIProvider{id: "custom", baseURL: "https://ogx.ogcode.in/v1"}
	if err := byHost.checkBodyFits(ogxMaxRequestBytes + 1); !errors.Is(err, ErrRequestTooLarge) {
		t.Errorf("ogx host over-limit body = %v, want ErrRequestTooLarge", err)
	}
}

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

// TestParseContextWindowFromBody exercises the cap-figure extraction from
// overflow bodies across the real provider phrasings, plus the rejections:
// unparseable text, and numbers outside the plausible-window band (those are a
// token count of something else — an output cap, an id — not the window).
func TestParseContextWindowFromBody(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"openai", "This model's maximum context length is 8192 tokens. However, you requested 9000 tokens.", 8192},
		{"fireworks variant", "maximum context length of 131072 tokens. However, you requested 131073 tokens.", 131072},
		{"anthropic", "prompt is too long: 195000 tokens > 200000 maximum", 200000},
		{"gemini", "The input token count (250000) exceeds the maximum number of tokens allowed (200000).", 200000},
		{"mirror generic", "prompt is too long: it exceeds the maximum of 8192 tokens", 8192},
		{"bare statement", "request failed: context length exceeds 4096 tokens", 4096},
		{"json wrapped", `{"error":{"message":"maximum context length is 16385 tokens"}}`, 16385},
		{"no number", "prompt is too long", 0},
		{"junk", "invalid request body", 0},
		{"empty", "", 0},
		// Below the plausibility floor: a 100-token "window" is some other count.
		{"below floor", "your request of 100 tokens exceeds the context length of 100 tokens", 0},
		// Above the plausibility ceiling: not a window any provider ships.
		{"above ceiling", "maximum context length is 99999999 tokens", 0},
		// A non-window token count in an overflow body must not be taken as the
		// cap: OpenAI's phrasing states both the input ("... requested 9000
		// tokens") and the cap — only the cap phrase matches.
		{"requested count not the cap", "maximum context length is 8192 tokens; you requested 9000", 8192},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ParseContextWindowFromBody(c.body); got != c.want {
				t.Errorf("ParseContextWindowFromBody(%q) = %d, want %d", c.body, got, c.want)
			}
		})
	}
}

// TestParseContextWindowFromBody_PhrasingsClassifyAsOverflow pins the shared
// seam between learning and classification: every phrasing the parser matches
// must also classify as a context overflow via IsContextLengthMessage, because
// the loop only learns from bodies the classifier already accepted. A pattern
// whose sample is no longer classified is dead code at best and a mislabel at
// worst.
func TestParseContextWindowFromBody_PhrasingsClassifyAsOverflow(t *testing.T) {
	for i, p := range contextWindowPatterns {
		if !IsContextLengthMessage(p.sample) {
			t.Errorf("pattern %d (%s) sample %q is not classified as an overflow by IsContextLengthMessage", i, p.re.String(), p.sample)
		}
		if ParseContextWindowFromBody(p.sample) == 0 {
			t.Errorf("pattern %d (%s) does not parse its own sample: %q", i, p.re.String(), p.sample)
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
		{"openrouter", 400, "", false},
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

// TestAPIError_IsFreeAllowance pins the gateway's free-allowance 429 as its own
// shape, told apart from a plan-budget 429 by the body rather than by the word
// "free", and not transient: the allowance refills with the UTC day, so a
// retry inside the same day only fails again. The plan-budget 429 is unchanged
// and stays transient.
func TestAPIError_IsFreeAllowance(t *testing.T) {
	const freeBody = `{"error":"free daily allowance exhausted","plan":"none","free":true,"daily_limit":5,"daily_used":5.0002,"daily_reset":1793664000}`
	cases := []struct {
		name          string
		code          int
		body          string
		wantFree      bool
		wantTransient bool
	}{
		{"free allowance spent", 429, freeBody, true, false},
		// The body may arrive without the JSON object around it — a proxy or a
		// truncated read still hands back the sentence.
		{"free allowance, unparseable body", 429, "free daily allowance exhausted", true, false},
		{"plan budget spent", 429, `{"error":"plan budget exhausted","plan":"pro","daily_limit":25,"daily_used":25}`, false, true},
		{"plain rate limit", 429, "rate limited", false, true},
		// "free" alone is not the shape: another refusal that happens to say
		// the word must not be read as a spent allowance.
		{"free tier disabled", 429, `{"error":"free tier models are not enabled for this key"}`, false, true},
		{"free body on a server error", 500, freeBody, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := &APIError{StatusCode: c.code, Body: c.body}
			if got := e.IsFreeAllowance(); got != c.wantFree {
				t.Errorf("IsFreeAllowance(%d, %q) = %v, want %v", c.code, c.body, got, c.wantFree)
			}
			if got := e.IsTransient(); got != c.wantTransient {
				t.Errorf("IsTransient(%d, %q) = %v, want %v", c.code, c.body, got, c.wantTransient)
			}
		})
	}
}

// TestAPIError_FreeAllowance pins the figures the interruption text needs, and
// that they are reported only where the body really carries them: a body that
// does not parse still identifies the refusal, but says nothing about how much
// was used or when it resets.
func TestAPIError_FreeAllowance(t *testing.T) {
	const body = `{"error":"free daily allowance exhausted","plan":"none","free":true,"daily_limit":5,"daily_used":5.0002,"daily_reset":1793664000}`
	used, limit, reset, ok := (&APIError{StatusCode: 429, Body: body}).FreeAllowance()
	if !ok {
		t.Fatal("FreeAllowance() reported no figures for a full body")
	}
	if used != 5.0002 || limit != 5 || reset != 1793664000 {
		t.Errorf("FreeAllowance() = %v, %v, %v; want 5.0002, 5, 1793664000", used, limit, reset)
	}

	for _, c := range []struct {
		name string
		code int
		body string
	}{
		{"unparseable body", 429, "free daily allowance exhausted"},
		{"no figures", 429, `{"error":"free daily allowance exhausted","free":true}`},
		{"plan budget", 429, `{"error":"plan budget exhausted","daily_used":25}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if used, limit, reset, ok := (&APIError{StatusCode: c.code, Body: c.body}).FreeAllowance(); ok {
				t.Errorf("FreeAllowance(%q) = %v, %v, %v, true; want ok=false", c.body, used, limit, reset)
			}
		})
	}
}
