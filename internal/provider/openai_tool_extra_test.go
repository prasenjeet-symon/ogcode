package provider

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// Only the Gemini endpoint attaches a per-tool-call extra (its thought
// signature) that must be replayed, so only it echoes one back.
func TestEchoesToolExtraIsGeminiOnly(t *testing.T) {
	cases := []struct {
		base string
		want bool
	}{
		{"https://generativelanguage.googleapis.com/v1beta/openai/", true},
		{"https://api.openai.com/v1", false},
		{"https://api.deepseek.com/v1", false},
		{"https://openrouter.ai/api/v1", false},
		{"http://localhost:11434/v1", false},
	}
	for _, c := range cases {
		p := effortProvider("openai", c.base)
		if got := p.EchoesToolExtra(); got != c.want {
			t.Errorf("%s: EchoesToolExtra = %v, want %v", c.base, got, c.want)
		}
	}
}

// sameJSON reports whether two raw JSON values are equal ignoring whitespace.
func sameJSON(a, b json.RawMessage) bool {
	var ab, bb bytes.Buffer
	if json.Compact(&ab, a) != nil || json.Compact(&bb, b) != nil {
		return false
	}
	return ab.String() == bb.String()
}

// The parser captures a tool call's extra_content only for a host that echoes
// it, and leaves it empty for every other host.
func TestStreamEventsCapturesToolExtra(t *testing.T) {
	sse := `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","extra_content":{"google":{"thought_signature":"abc123"}},"function":{"name":"read","arguments":"{\"path\":\"a\"}"}}]}}]}` + "\n\n" +
		"data: [DONE]\n\n"

	start := func(baseURL string) StreamEvent {
		p := effortProvider("openai", baseURL)
		ch := make(chan StreamEvent, 8)
		p.streamEvents(io.NopCloser(strings.NewReader(sse)), ch, func() {}, map[string]bool{"read": true})
		for evt := range ch {
			if evt.Type == EventToolCallStart {
				return evt
			}
		}
		return StreamEvent{}
	}

	gemini := start("https://generativelanguage.googleapis.com/v1beta/openai/")
	if gemini.ToolName != "read" {
		t.Fatalf("ToolName = %q, want %q", gemini.ToolName, "read")
	}
	want := json.RawMessage(`{"google":{"thought_signature":"abc123"}}`)
	if !sameJSON(gemini.ToolExtra, want) {
		t.Errorf("Gemini ToolExtra = %s, want %s", gemini.ToolExtra, want)
	}

	openai := start("https://api.openai.com/v1")
	if len(openai.ToolExtra) != 0 {
		t.Errorf("non-Gemini ToolExtra = %s, want empty", openai.ToolExtra)
	}
}
