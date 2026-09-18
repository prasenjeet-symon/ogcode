package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// The default Anthropic cache lives 5 minutes, and an agent loop routinely goes
// longer than that between requests — a test run, a build, a deep_search, or the
// developer reading a diff. Each expiry re-reads the whole cacheable prefix
// (~7.8k tokens of tools plus system for the build agent, before any history).
func TestAnthropic_ExtendedTTLOnStaticPrefixOnly(t *testing.T) {
	p := &AnthropicProvider{baseURL: "https://api.anthropic.com/v1"}

	cc := p.staticPrefixCacheControl()
	if cc.TTL != "1h" {
		t.Errorf("static prefix TTL = %q, want 1h", cc.TTL)
	}
	if cc.Type != "ephemeral" {
		t.Errorf("cache type = %q, want ephemeral", cc.Type)
	}

	// The message breakpoint keeps the 5-minute default. It is rewritten on
	// every step, so a 2x write on content superseded seconds later costs more
	// than it returns — and Anthropic requires 1h breakpoints to precede 5m ones
	// in the tools -> system -> messages prefix, which this arrangement is the
	// only valid way to satisfy.
	msgs := []anthropicMessage{{Role: "user", Content: "hi"}}
	attachMessageCacheBreakpoint(msgs)
	blocks := msgs[0].Content.([]map[string]any)
	ccMsg := blocks[0]["cache_control"].(map[string]any)
	if _, hasTTL := ccMsg["ttl"]; hasTTL {
		t.Errorf("message breakpoint must keep the default TTL, got %v", ccMsg)
	}
}

// ANTHROPIC_BASE_URL points proxies and gateways at this code path. An unknown
// beta header is ignored harmlessly, but an unknown "ttl" inside cache_control
// can be a hard 400 — which would take the session down to save tokens.
func TestAnthropic_ExtendedTTLOnlyOnCanonicalHost(t *testing.T) {
	for _, url := range []string{
		"https://my-proxy.internal/v1",
		"http://localhost:8080/v1",
		"https://gateway.example.com/anthropic/v1",
	} {
		p := &AnthropicProvider{baseURL: url}
		if p.usesExtendedCacheTTL() {
			t.Errorf("%s: extended TTL must not be requested from a non-canonical host", url)
		}
		if ttl := p.staticPrefixCacheControl().TTL; ttl != "" {
			t.Errorf("%s: TTL = %q, want empty (provider default)", url, ttl)
		}
	}
}

// OpenRouter is a passthrough: for its Anthropic models it forwards
// cache_control and invents nothing, so a request with no breakpoints gets NO
// caching. That was the default configuration — OpenRouter's default model here
// is anthropic/claude-sonnet-4.6.
func TestOpenAI_ExplicitBreakpointsOnlyForOpenRouterAnthropic(t *testing.T) {
	cases := []struct {
		baseURL string
		model   string
		want    bool
	}{
		{"https://openrouter.ai/api/v1", "anthropic/claude-sonnet-4.6", true},
		{"https://openrouter.ai/api/v1", "anthropic/claude-opus-4.6", true},
		// Automatic on these; adding parts would be churn, not caching.
		{"https://openrouter.ai/api/v1", "openai/gpt-4o", false},
		{"https://openrouter.ai/api/v1", "google/gemini-2.5-pro", false},
		// Every other endpoint this one struct serves.
		{"https://api.openai.com/v1", "gpt-4o", false},
		{"http://localhost:11434/v1", "qwen2.5-coder", false},
		{"https://api.groq.com/openai/v1", "llama-3.3-70b", false},
	}
	for _, c := range cases {
		p := &OpenAIProvider{baseURL: c.baseURL}
		if got := p.needsExplicitCacheBreakpoints(c.model); got != c.want {
			t.Errorf("%s + %s: needsExplicitCacheBreakpoints = %v, want %v", c.baseURL, c.model, got, c.want)
		}
	}
}

// The breakpoint goes at the end of entry [0] — the session-static base. Entries
// [1:] are the per-turn tail (viewport, index status, date, skills, a compaction
// summary), so a breakpoint past them would be invalidated by the very content
// it was meant to protect.
func TestSystemMessageFor_BreakpointMarksTheStaticBaseOnly(t *testing.T) {
	system := []string{"STATIC BASE", "viewport", "date"}

	msg, ok := systemMessageFor(system, true)
	if !ok {
		t.Fatal("expected a system message")
	}
	parts, isParts := msg.Content.([]any)
	if !isParts || len(parts) != 2 {
		t.Fatalf("expected 2 content parts, got %T %v", msg.Content, msg.Content)
	}
	base := parts[0].(map[string]any)
	if base["text"] != "STATIC BASE" {
		t.Errorf("first part should be the static base, got %v", base["text"])
	}
	if _, marked := base["cache_control"]; !marked {
		t.Error("the static base must carry the breakpoint")
	}
	tail := parts[1].(map[string]any)
	if _, marked := tail["cache_control"]; marked {
		t.Error("the per-turn tail must not be marked")
	}
	if tail["text"] != "viewport\n\ndate" {
		t.Errorf("tail = %q", tail["text"])
	}

	// Without breakpoints the shape must not change at all: this struct also
	// serves Ollama, Groq and anything a user points a base URL at.
	plain, _ := systemMessageFor(system, false)
	if s, isString := plain.Content.(string); !isString || s != "STATIC BASE\n\nviewport\n\ndate" {
		t.Errorf("unbreakpointed system message must stay a plain string, got %T %v", plain.Content, plain.Content)
	}

	if _, ok := systemMessageFor(nil, true); ok {
		t.Error("no system entries should produce no system message")
	}
}

// A tool-result message is the last message on most steps of an agent loop, and
// its content maps to an Anthropic tool_result block — not the shape this marker
// belongs on. Walking back to the previous user/assistant message still caches
// nearly all of the history and cannot produce a rejected request.
func TestAttachOAIMessageBreakpoint_SkipsToolResults(t *testing.T) {
	msgs := []oaiMessage{
		{Role: "user", Content: "the task"},
		{Role: "assistant", Content: "let me look"},
		{Role: "tool", Content: "file contents", ToolCallID: "call_1"},
		{Role: "tool", Content: "more output", ToolCallID: "call_2"},
	}
	attachOAIMessageBreakpoint(msgs)

	if _, marked := msgs[2].Content.([]any); marked {
		t.Error("a tool result must not be converted to parts")
	}
	if _, marked := msgs[3].Content.([]any); marked {
		t.Error("a tool result must not be converted to parts")
	}
	parts, ok := msgs[1].Content.([]any)
	if !ok {
		t.Fatalf("expected the last assistant message to carry the breakpoint, got %T", msgs[1].Content)
	}
	if _, marked := parts[0].(map[string]any)["cache_control"]; !marked {
		t.Error("the chosen message must carry cache_control")
	}
	if msgs[0].Content != "the task" {
		t.Error("earlier messages must be left alone")
	}
}

// Nothing markable must not panic or corrupt the request.
func TestAttachOAIMessageBreakpoint_NoEligibleMessage(t *testing.T) {
	msgs := []oaiMessage{{Role: "tool", Content: "out", ToolCallID: "c1"}}
	attachOAIMessageBreakpoint(msgs)
	if _, changed := msgs[0].Content.([]any); changed {
		t.Error("a tool-only history must be left untouched")
	}
	attachOAIMessageBreakpoint(nil)
}

// prompt_cache_key is OpenAI's documented routing hint. It goes only where it is
// understood: an unknown top-level field is a 400 on a strict server.
func TestOpenAI_PromptCacheKeyEndpointGate(t *testing.T) {
	for _, c := range []struct {
		baseURL string
		want    bool
	}{
		{"https://api.openai.com/v1", true},
		{"https://openrouter.ai/api/v1", true},
		{"http://localhost:11434/v1", false},
		{"https://api.groq.com/openai/v1", false},
		{"https://api.deepseek.com/v1", false},
	} {
		p := &OpenAIProvider{baseURL: c.baseURL}
		if got := p.sendsPromptCacheKey(); got != c.want {
			t.Errorf("%s: sendsPromptCacheKey = %v, want %v", c.baseURL, got, c.want)
		}
	}
}

// roundTripperFunc lets a test intercept the request at the transport, so the
// provider keeps its REAL base URL. That matters here: every gate in this file
// reads p.baseURL, so pointing the provider at a stub server switches off the
// behaviour under test and leaves only the negative case proved.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// captureWireRequest runs one StreamChat against an intercepting transport and
// returns the decoded body and the request headers.
func captureWireRequest(t *testing.T, p Provider, req StreamRequest) (map[string]any, http.Header) {
	t.Helper()
	var body map[string]any
	var headers http.Header

	saved := streamHTTPClient.Transport
	t.Cleanup(func() { streamHTTPClient.Transport = saved })
	streamHTTPClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		headers = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("decode request body: %v\n%s", err, raw)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    r,
		}, nil
	})

	if _, err := p.StreamChat(context.Background(), req); err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	return body, headers
}

// The headline case: a Claude model through OpenRouter. Without breakpoints in
// the body this request is not cached at all, and it is the default
// configuration — OpenRouter's default model here is anthropic/claude-sonnet-4.6.
func TestWire_OpenRouterAnthropicIsCacheable(t *testing.T) {
	p := &OpenAIProvider{id: "openrouter", apiKey: "k", baseURL: "https://openrouter.ai/api/v1"}
	body, _ := captureWireRequest(t, p, StreamRequest{
		Model:    "anthropic/claude-sonnet-4.6",
		System:   []string{"STATIC BASE", "per-turn tail"},
		CacheKey: "ses_abc",
		Messages: []ModelMessage{
			{Role: "user", Content: json.RawMessage(`"the task"`)},
			{Role: "assistant", Content: json.RawMessage(`"looking"`)},
			{Role: "tool", Name: "read", ToolCallID: "c1", Content: json.RawMessage(`"file contents"`)},
		},
	})

	msgs := body["messages"].([]any)
	sys := msgs[0].(map[string]any)
	parts, ok := sys["content"].([]any)
	if !ok {
		t.Fatalf("system content is %T, want an array of parts carrying a breakpoint", sys["content"])
	}
	base := parts[0].(map[string]any)
	if base["text"] != "STATIC BASE" || base["cache_control"] == nil {
		t.Errorf("the static base must carry the breakpoint, got %v", base)
	}
	if parts[1].(map[string]any)["cache_control"] != nil {
		t.Error("the per-turn tail must not be marked")
	}

	// History breakpoint lands on the assistant message, not the tool result.
	if msgs[3].(map[string]any)["content"] == nil {
		t.Fatal("tool result content went missing")
	}
	if _, converted := msgs[3].(map[string]any)["content"].([]any); converted {
		t.Error("a tool result must not be rewritten into content parts")
	}
	asst, ok := msgs[2].(map[string]any)["content"].([]any)
	if !ok {
		t.Fatalf("assistant content is %T, want parts with a breakpoint", msgs[2].(map[string]any)["content"])
	}
	if asst[0].(map[string]any)["cache_control"] == nil {
		t.Error("the conversation prefix must carry a breakpoint")
	}

	if body["prompt_cache_key"] != "ses_abc" {
		t.Errorf("prompt_cache_key = %v, want ses_abc", body["prompt_cache_key"])
	}
}

// A non-Anthropic model on the same endpoint caches automatically; adding parts
// would be churn, and the gate must not fire.
func TestWire_OpenRouterGPTStaysPlain(t *testing.T) {
	p := &OpenAIProvider{id: "openrouter", apiKey: "k", baseURL: "https://openrouter.ai/api/v1"}
	body, _ := captureWireRequest(t, p, StreamRequest{
		Model:    "openai/gpt-4o",
		System:   []string{"STATIC BASE", "tail"},
		CacheKey: "ses_abc",
		Messages: []ModelMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	})
	sys := body["messages"].([]any)[0].(map[string]any)
	if _, isString := sys["content"].(string); !isString {
		t.Errorf("system content is %T, want a plain string", sys["content"])
	}
	if body["prompt_cache_key"] != "ses_abc" {
		t.Error("the routing hint still applies to OpenAI-backed models")
	}
}

// Everything else this one struct serves must be byte-for-byte unchanged: an
// unknown field or an unexpected content shape is a 400 on a strict server.
func TestWire_OtherEndpointsUnchanged(t *testing.T) {
	for _, baseURL := range []string{
		"http://localhost:11434/v1",
		"https://api.groq.com/openai/v1",
		"https://api.deepseek.com/v1",
	} {
		p := &OpenAIProvider{id: "x", apiKey: "k", baseURL: baseURL}
		body, _ := captureWireRequest(t, p, StreamRequest{
			Model:    "some-model",
			System:   []string{"STATIC BASE", "tail"},
			CacheKey: "ses_abc",
			Messages: []ModelMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
		})
		sys := body["messages"].([]any)[0].(map[string]any)
		if _, isString := sys["content"].(string); !isString {
			t.Errorf("%s: system content is %T, want a plain string", baseURL, sys["content"])
		}
		if _, sent := body["prompt_cache_key"]; sent {
			t.Errorf("%s: prompt_cache_key must not be sent to an unrecognised endpoint", baseURL)
		}
	}
}

// Anthropic's own endpoint: the static prefix asks for the 1-hour cache, the
// message breakpoint keeps the 5-minute default, and the beta header that
// unlocks the longer TTL is present.
func TestWire_AnthropicExtendedTTL(t *testing.T) {
	p := &AnthropicProvider{apiKey: "k", model: "claude-sonnet-4-6", baseURL: "https://api.anthropic.com/v1"}
	body, headers := captureWireRequest(t, p, StreamRequest{
		Model:    "claude-sonnet-4-6",
		System:   []string{"STATIC BASE", "per-turn tail"},
		Messages: []ModelMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	})

	if got := headers.Get("anthropic-beta"); got != extendedCacheTTLBeta {
		t.Errorf("anthropic-beta = %q, want %q", got, extendedCacheTTLBeta)
	}

	sys := body["system"].([]any)
	cc := sys[0].(map[string]any)["cache_control"].(map[string]any)
	if cc["ttl"] != "1h" {
		t.Errorf("system breakpoint ttl = %v, want 1h", cc["ttl"])
	}
	if sys[1].(map[string]any)["cache_control"] != nil {
		t.Error("the per-turn tail must not be marked")
	}

	tools := body["tools"]
	if tools != nil {
		list := tools.([]any)
		last := list[len(list)-1].(map[string]any)["cache_control"].(map[string]any)
		if last["ttl"] != "1h" {
			t.Errorf("tool breakpoint ttl = %v, want 1h", last["ttl"])
		}
	}

	// 1h breakpoints must precede 5m ones in the tools -> system -> messages
	// prefix, and the message tail is rewritten every step, so it stays on the
	// short default.
	msg := body["messages"].([]any)[0].(map[string]any)
	blocks := msg["content"].([]any)
	msgCC := blocks[len(blocks)-1].(map[string]any)["cache_control"].(map[string]any)
	if _, hasTTL := msgCC["ttl"]; hasTTL {
		t.Errorf("message breakpoint must keep the default TTL, got %v", msgCC)
	}
}

// A proxy behind ANTHROPIC_BASE_URL gets neither the header nor the ttl field:
// an unknown beta header is ignored, but an unknown ttl can be a hard 400.
func TestWire_AnthropicProxyGetsNoExtendedTTL(t *testing.T) {
	p := &AnthropicProvider{apiKey: "k", model: "claude-sonnet-4-6", baseURL: "https://gateway.internal/anthropic/v1"}
	body, headers := captureWireRequest(t, p, StreamRequest{
		Model:    "claude-sonnet-4-6",
		System:   []string{"STATIC BASE", "tail"},
		Messages: []ModelMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	})
	if got := headers.Get("anthropic-beta"); got != "" {
		t.Errorf("anthropic-beta = %q, want none", got)
	}
	cc := body["system"].([]any)[0].(map[string]any)["cache_control"].(map[string]any)
	if _, hasTTL := cc["ttl"]; hasTTL {
		t.Errorf("a proxy must not receive a ttl field, got %v", cc)
	}
	if cc["type"] != "ephemeral" {
		t.Error("the default breakpoint must still be sent")
	}
}

// Mid-loop guidance is text the user sends while the agent is already working.
// The caller's fallback appends it to the turn's FIRST user message, which is
// the earliest possible byte to change — so a prompt cache stops matching right
// there and every tool result behind it is re-processed at full price, on the
// step immediately after the user steers.
//
// Anthropic carries tool results as blocks inside a user message, so the
// guidance can be one more block on that same message: no new message, no
// question about roles, and nothing behind it to invalidate.
func TestWire_AnthropicGuidanceGoesAtTheEnd(t *testing.T) {
	p := &AnthropicProvider{apiKey: "k", model: "claude-sonnet-4-6", baseURL: "https://api.anthropic.com/v1"}
	if !PlacesGuidance(p) {
		t.Fatal("Anthropic should place guidance itself")
	}

	body, _ := captureWireRequest(t, p, StreamRequest{
		Model:    "claude-sonnet-4-6",
		System:   []string{"BASE"},
		Guidance: "\n\n[guidance] skip the tests",
		Messages: []ModelMessage{
			{Role: "user", Content: json.RawMessage(`"refactor the auth module"`)},
			{Role: "assistant", ToolCalls: json.RawMessage(`[{"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]`)},
			{Role: "tool", Name: "read", ToolCallID: "c1", Content: json.RawMessage(`"3000 lines of file"`)},
		},
	})

	msgs := body["messages"].([]any)

	// The turn's opening prompt is untouched — that is the whole point.
	first := msgs[0].(map[string]any)
	if first["content"] != "refactor the auth module" {
		t.Errorf("the first user message must not be rewritten, got %v", first["content"])
	}

	// The guidance rides as a trailing block on the message that already carries
	// the tool results, so no new message was created.
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages (no new one), got %d", len(msgs))
	}
	last := msgs[len(msgs)-1].(map[string]any)
	if last["role"] != "user" {
		t.Fatalf("last message role = %v, want user (tool results)", last["role"])
	}
	blocks := last["content"].([]any)
	tail := blocks[len(blocks)-1].(map[string]any)
	if tail["type"] != "text" || !strings.Contains(tail["text"].(string), "skip the tests") {
		t.Errorf("guidance should be the final text block, got %v", tail)
	}
	// tool_result must come first in a user message; text after it is fine.
	if blocks[0].(map[string]any)["type"] != "tool_result" {
		t.Errorf("tool_result must stay first, got %v", blocks[0])
	}

	// The cache breakpoint stays on the stable content, NOT on the guidance:
	// with it on the guidance, changing the guidance would mean no exact match
	// at the breakpoint.
	if _, marked := tail["cache_control"]; marked {
		t.Error("the breakpoint must not sit on the guidance block")
	}
	if _, marked := blocks[len(blocks)-2].(map[string]any)["cache_control"]; !marked {
		t.Error("the breakpoint should sit on the last stable block")
	}
}

// No guidance must change nothing at all.
func TestWire_AnthropicNoGuidanceIsUnchanged(t *testing.T) {
	p := &AnthropicProvider{apiKey: "k", model: "claude-sonnet-4-6", baseURL: "https://api.anthropic.com/v1"}
	body, _ := captureWireRequest(t, p, StreamRequest{
		Model:    "claude-sonnet-4-6",
		System:   []string{"BASE"},
		Messages: []ModelMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	})
	msgs := body["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	blocks := msgs[0].(map[string]any)["content"].([]any)
	if len(blocks) != 1 {
		t.Errorf("expected the single text block the breakpoint normalises to, got %v", blocks)
	}
}

// A conversation ending on an assistant message gets its own user message —
// ordinary alternation, not a malformed request.
func TestAppendGuidanceBlock_AssistantTailGetsItsOwnMessage(t *testing.T) {
	msgs := []anthropicMessage{
		{Role: "user", Content: "start"},
		{Role: "assistant", Content: []map[string]any{{"type": "text", "text": "thinking"}}},
	}
	out := appendGuidanceBlock(msgs, "  do X instead  ")
	if len(out) != 3 {
		t.Fatalf("expected a new trailing message, got %d", len(out))
	}
	if out[2].Role != "user" {
		t.Errorf("new message role = %q, want user", out[2].Role)
	}
	blocks := out[2].Content.([]map[string]any)
	if blocks[0]["text"] != "do X instead" {
		t.Errorf("guidance should be trimmed, got %q", blocks[0]["text"])
	}

	// Empty guidance is a no-op.
	if got := appendGuidanceBlock(msgs, "   "); len(got) != len(msgs) {
		t.Error("blank guidance must not add a message")
	}
	if got := appendGuidanceBlock(nil, "x"); len(got) != 1 || got[0].Role != "user" {
		t.Error("guidance with no history should become a lone user message")
	}
}

// A string-content tail is normalised into blocks rather than concatenated, so
// the guidance stays visibly separate from the user's own words.
func TestAppendGuidanceBlock_StringTailBecomesBlocks(t *testing.T) {
	msgs := []anthropicMessage{{Role: "user", Content: "the task"}}
	out := appendGuidanceBlock(msgs, "steer left")
	blocks := out[0].Content.([]map[string]any)
	if len(blocks) != 2 || blocks[0]["text"] != "the task" || blocks[1]["text"] != "steer left" {
		t.Errorf("unexpected blocks: %v", blocks)
	}
}
