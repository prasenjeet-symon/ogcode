package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Anthropic has no "tool" role — results travel as tool_result blocks inside a
// user message. The converter used to recognise a result only by its call id,
// so a result whose id had gone missing fell through to the generic branch and
// was sent with role "tool", which the API rejects outright:
//
//	messages[11].role: unknown variant `tool`, expected one of `user`,
//	`assistant`, `system`
//
// That 400 is not recoverable by retrying or resuming: the same history is
// rebuilt every time, so the session stays broken until the history changes.
func TestAnthropicStreamChat_NeverEmitsAToolRole(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := &AnthropicProvider{apiKey: "test-key", baseURL: srv.URL, model: "test-model"}
	_, err := p.StreamChat(context.Background(), StreamRequest{
		Model: "test-model",
		Messages: []ModelMessage{
			{Role: "user", Content: json.RawMessage(`"do the thing"`)},
			{Role: "assistant", ToolCalls: json.RawMessage(`[{"id":"call_1","type":"function","function":{"name":"grep","arguments":"{}"}}]`)},
			// The shape that caused the 400: marked as a result by role, with no
			// id to be recognised by.
			{Role: "tool", Name: "grep", Content: json.RawMessage(`"result text"`)},
		},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("no request body captured")
	}
	if strings.Contains(string(body), `"role":"tool"`) {
		t.Errorf("request carries a \"tool\" role, which Anthropic rejects:\n%s", body)
	}

	var sent struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	for _, m := range sent.Messages {
		switch m.Role {
		case "user", "assistant", "system":
		default:
			t.Errorf("message sent with role %q; Anthropic accepts only user/assistant/system", m.Role)
		}
	}
	// It must land as a tool_result, not be silently turned into prose — the
	// model has to see it as the answer to the call it made.
	if !strings.Contains(string(body), `"tool_result"`) {
		t.Errorf("the tool result was not emitted as a tool_result block:\n%s", body)
	}
}
