package provider

import (
	"context"
	"encoding/json"
	"testing"
)

// parallelToolCallsRequest is the smallest request that carries tools, so the
// switch is the only thing the assertion below can be reading.
func parallelToolCallsRequest(tools []ToolDefinition) StreamRequest {
	return StreamRequest{
		Model:     "gpt-6-sol",
		Messages:  []ModelMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
		Tools:     tools,
		MaxTokens: 100,
	}
}

// The request-side switch asks the server to run the calls of one assistant
// turn side by side. It is documented by OpenAI and forwarded by OpenRouter,
// and both must carry it on every request that has tools — a model that has to
// be told "send several calls per turn" in the prompt also needs the endpoint
// to accept them in one turn.
func TestWire_ParallelToolCallsOnDocumentingEndpoints(t *testing.T) {
	for _, p := range []*OpenAIProvider{
		{id: "openai", apiKey: "k", baseURL: "https://api.openai.com/v1"},
		{id: "openrouter", apiKey: "k", baseURL: "https://openrouter.ai/api/v1"},
	} {
		body, _ := captureWireRequest(t, p, parallelToolCallsRequest(shapeTool))
		got, sent := body["parallel_tool_calls"]
		if !sent {
			t.Errorf("%s: parallel_tool_calls absent, want true", p.id)
			continue
		}
		if got != true {
			t.Errorf("%s: parallel_tool_calls = %v, want true", p.id, got)
		}
	}
}

// Without tools the field says nothing, and a strict server that does not list
// it answers 400 on an unknown field — so it must not be sent.
func TestWire_ParallelToolCallsNeedsTools(t *testing.T) {
	p := &OpenAIProvider{id: "openai", apiKey: "k", baseURL: "https://api.openai.com/v1"}
	body, _ := captureWireRequest(t, p, parallelToolCallsRequest(nil))
	if _, sent := body["parallel_tool_calls"]; sent {
		t.Error("parallel_tool_calls sent on a request with no tools")
	}
}

// An endpoint that only SPEAKS the OpenAI shape is not an endpoint that
// documents this field. A custom provider on an unknown host keeps its body
// byte-for-byte as before, tools and all.
func TestWire_ParallelToolCallsOmittedForUnknownHosts(t *testing.T) {
	p := &OpenAIProvider{id: "groq", apiKey: "k", baseURL: "https://api.groq.com/openai/v1"}
	body, _ := captureWireRequest(t, p, parallelToolCallsRequest(shapeTool))
	if _, sent := body["parallel_tool_calls"]; sent {
		t.Error("parallel_tool_calls sent to an endpoint that does not document it")
	}
}

// The gate's own truth table, independent of the wire: the host decides, not
// the provider id alone, so a custom provider pointed at api.openai.com gets
// the field and one pointed anywhere else does not.
func TestSendsParallelToolCalls(t *testing.T) {
	cases := []struct {
		name    string
		id      string
		baseURL string
		want    bool
	}{
		{"openai id", "openai", "https://api.openai.com/v1", true},
		{"openai host, custom id", "my-openai", "https://api.openai.com/v1", true},
		{"openrouter host", "openrouter", "https://openrouter.ai/api/v1", true},
		{"unknown host", "groq", "https://api.groq.com/openai/v1", false},
		{"ollama host", "ollama", "http://localhost:8090/v1", false},
		{"ogx host", "ogx", "https://ogx.ogcode.in/v1", false},
	}
	for _, tc := range cases {
		p := &OpenAIProvider{id: tc.id, baseURL: tc.baseURL}
		if got := p.sendsParallelToolCalls(); got != tc.want {
			t.Errorf("%s (%s @ %s) = %v, want %v", tc.name, tc.id, tc.baseURL, got, tc.want)
		}
	}
}

// Guard against the transport shim drifting: captureWireRequest must really
// see the request body, or every test in this file passes vacuously.
func TestWire_ParallelToolCallsCaptureSeesTheBody(t *testing.T) {
	p := &OpenAIProvider{id: "openai", apiKey: "k", baseURL: "https://api.openai.com/v1"}
	body, _ := captureWireRequest(t, p, parallelToolCallsRequest(shapeTool))
	if body["messages"] == nil {
		t.Fatal("captured body has no messages: the transport shim stopped intercepting")
	}
	if _, err := p.StreamChat(context.Background(), parallelToolCallsRequest(nil)); err != nil {
		t.Fatalf("StreamChat without tools: %v", err)
	}
}
