package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// echoTurn is one tool-using turn as the agent loop resends it: the prompt, a
// step that reasoned before calling a tool, the tool's result, and a step that
// answered without reasoning.
func echoTurn() []ModelMessage {
	return []ModelMessage{
		// Never sent from a user message, whatever a caller puts here.
		{Role: "user", Content: json.RawMessage(`"fix the bug"`), ReasoningText: "not an assistant's"},
		{Role: "assistant", ReasoningText: "read the file first",
			ToolCalls: json.RawMessage(`[{"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]`)},
		{Role: "tool", ToolCallID: "c1", Name: "read", Content: json.RawMessage(`"package main"`)},
		{Role: "assistant", Content: json.RawMessage(`"found it"`)},
	}
}

func echoRequest(model string, messages []ModelMessage) StreamRequest {
	return StreamRequest{Model: model, Messages: messages, Tools: shapeTool, Thinking: true, CacheKey: "ses_1"}
}

// Each host gets an assistant message's reasoning back under the name it reads
// it by, and a host that does not read it gets nothing: an unknown message
// field is a 400 on the strict ones.
func TestReasoningEchoOnTheWire(t *testing.T) {
	const (
		none             = ""
		reasoningContent = "reasoning_content"
		reasoning        = "reasoning"
	)
	cases := []struct {
		name  string
		p     *OpenAIProvider
		model string
		field string
	}{
		{"DeepSeek", effortProvider("openai", "https://api.deepseek.com/v1"), "deepseek-flash", reasoningContent},
		{"Z.ai", effortProvider("openai", "https://api.z.ai/api/paas/v4"), "glm-5.3", reasoningContent},
		{"Moonshot", effortProvider("openai", "https://api.moonshot.ai/v1"), "kimi-k3", reasoningContent},
		{"Qwen Cloud", effortProvider("openai", "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"), "qwen3.8-max", reasoningContent},
		{"local Ollama", effortProvider("ollama", "http://localhost:11434/v1"), "gpt-oss:20b", reasoning},
		{"Ollama's cloud in the OpenAI slot", effortProvider("openai", "https://ollama.com/v1"), "deepseek-v4-pro", reasoning},
		{"OGX", effortProvider(OGXProviderID, "https://ogx.ogcode.in/v1"), "deepseek-v4-pro", reasoning},
		{"OpenAI", effortProvider("openai", "https://api.openai.com/v1"), "gpt-5.2", none},
		{"Groq", effortProvider("openai", "https://api.groq.com/openai/v1"), "openai/gpt-oss-120b", none},
		{"Fireworks", effortProvider("openai", "https://api.fireworks.ai/inference/v1"), "accounts/fireworks/models/deepseek-v4-pro", none},
		{"Mistral", effortProvider("openai", "https://api.mistral.ai/v1"), "magistral-medium-latest", none},
		// OpenRouter takes reasoning_details back, which ogcode does not keep.
		{"OpenRouter", effortProvider("openrouter", "https://openrouter.ai/api/v1"), "deepseek/deepseek-v4.1-flash", none},
		{"OpenRouter in the OpenAI slot", effortProvider("openai", "https://openrouter.ai/api/v1"), "deepseek/deepseek-v4.1-flash", none},
		{"Gemini", effortProvider("openai", "https://generativelanguage.googleapis.com/v1beta/openai/"), "gemini-3.8-flash", none},
		{"MiniMax", effortProvider("openai", "https://api.minimax.io/v1"), "MiniMax-M3", none},
		{"Together", effortProvider("openai", "https://api.together.xyz/v1"), "moonshotai/Kimi-K3", none},
		{"an unknown endpoint", effortProvider("openai", "http://localhost:1234/v1"), "qwen3-32b", none},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// The agent loop attaches reasoning only where it will be sent, so
			// the two must agree.
			if got := c.p.EchoesReasoning(); got != (c.field != none) {
				t.Errorf("EchoesReasoning() = %v, but the wire carries %q", got, c.field)
			}
			body, _ := captureWireRequest(t, c.p, echoRequest(c.model, echoTurn()))
			msgs, _ := body["messages"].([]any)
			if len(msgs) != 4 {
				t.Fatalf("sent %d messages, want 4: %v", len(msgs), body["messages"])
			}
			for i, raw := range msgs {
				m, _ := raw.(map[string]any)
				rc, hasRC := m["reasoning_content"]
				r, hasR := m["reasoning"]
				if c.field != reasoningContent && hasRC {
					t.Errorf("message %d (%v): reasoning_content sent to a host that does not take it: %v", i, m["role"], rc)
				}
				if c.field != reasoning && hasR {
					t.Errorf("message %d (%v): reasoning sent to a host that does not take it: %v", i, m["role"], r)
				}
				if m["role"] != "assistant" {
					if hasRC || hasR {
						t.Errorf("message %d (%v) carries reasoning; only an assistant's goes back", i, m["role"])
					}
					continue
				}
				want := map[int]string{1: "read the file first", 3: ""}[i]
				switch c.field {
				case reasoningContent:
					// On every assistant message, empty where there was none:
					// DeepSeek fails a tool request that leaves the field out.
					if !hasRC || rc != want {
						t.Errorf("message %d: reasoning_content = %v (present %v), want %q", i, rc, hasRC, want)
					}
				case reasoning:
					if want == "" && hasR {
						t.Errorf("message %d: empty reasoning sent: %v", i, r)
					}
					if want != "" && r != want {
						t.Errorf("message %d: reasoning = %v, want %q", i, r, want)
					}
				}
			}
		})
	}
}

// An assistant message is resent on every later step of its turn, and these
// hosts cache by prefix: each step must send the history before it exactly as
// the step before did, reasoning included, and only append to it.
func TestReasoningEchoKeepsTheResentPrefixByteIdentical(t *testing.T) {
	for _, c := range []struct {
		name string
		p    *OpenAIProvider
	}{
		{"DeepSeek", effortProvider("openai", "https://api.deepseek.com/v1")},
		{"Ollama", effortProvider("ollama", "http://localhost:11434/v1")},
	} {
		p := c.p
		t.Run(c.name, func(t *testing.T) {
			step1 := echoTurn()
			step2 := append(slices.Clone(step1),
				ModelMessage{Role: "assistant", ReasoningText: "now the test",
					ToolCalls: json.RawMessage(`[{"id":"c2","type":"function","function":{"name":"read","arguments":"{}"}}]`)},
				ModelMessage{Role: "tool", ToolCallID: "c2", Name: "read", Content: json.RawMessage(`"package main_test"`)},
			)
			first := captureWireBody(t, p, echoRequest("deepseek-flash", step1))
			again := captureWireBody(t, p, echoRequest("deepseek-flash", step1))
			second := captureWireBody(t, p, echoRequest("deepseek-flash", step2))

			if !bytes.Equal(first, again) {
				t.Fatalf("the same history serialised differently twice:\n%s\n%s", first, again)
			}
			// Messages precede tools on the wire; everything through the first
			// step's last message is the prefix the next step must repeat.
			end := bytes.Index(first, []byte(`],"tools":`))
			if end < 0 {
				t.Fatalf("no messages/tools boundary in %s", first)
			}
			if !bytes.HasPrefix(second, first[:end]) {
				t.Errorf("the next step changed the resent history:\nstep 1: %s\nstep 2: %s", first[:end], second[:end])
			}
			if second[end] != ',' {
				t.Errorf("the next step did not append after the resent history: %q", second[end:min(end+40, len(second))])
			}
			if !bytes.Contains(second[end:], []byte(`now the test`)) {
				t.Errorf("the new step's reasoning was not sent: %s", second[end:])
			}
		})
	}
}

// captureWireBody is captureWireRequest for a test that needs the exact bytes
// sent rather than their decoded form.
func captureWireBody(t *testing.T, p Provider, req StreamRequest) []byte {
	t.Helper()
	var raw []byte
	saved := streamHTTPClient.Transport
	t.Cleanup(func() { streamHTTPClient.Transport = saved })
	streamHTTPClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ = io.ReadAll(r.Body)
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
	return raw
}
