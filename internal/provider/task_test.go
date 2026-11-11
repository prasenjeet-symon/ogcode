package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestTaskHeaderReachesTheWire pins the routing contract on both transports: a
// chat request carries its kind in X-Client-Task, byte-identical to the Task
// the caller set, because a router maps on that exact spelling.
func TestTaskHeaderReachesTheWire(t *testing.T) {
	tests := []struct {
		name string
		new  func(baseURL string) Provider
	}{
		{"openai-compatible", func(u string) Provider {
			return &OpenAIProvider{id: "openai", apiKey: "sk-test", baseURL: u}
		}},
		{"anthropic", func(u string) Provider {
			return &AnthropicProvider{apiKey: "sk-test", baseURL: u}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var seen http.Header
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = r.Header.Clone()
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
			}))
			defer srv.Close()

			ch, err := tc.new(srv.URL).StreamChat(context.Background(), StreamRequest{
				Model:    "m",
				Messages: []ModelMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
				Task:     TaskIndex,
			})
			if err != nil {
				t.Fatalf("StreamChat: %v", err)
			}
			for range ch {
			}

			if got := seen.Get(headerTask); got != string(TaskIndex) {
				t.Errorf("%s = %q, want %q", headerTask, got, TaskIndex)
			}
		})
	}
}

// TestTaskHeaderOnlyOnChatRequests is the counterpart: the model-catalogue fetch
// is not task work, and a caller that sets no kind must not have one invented
// for it. Both would otherwise send the router a request it cannot route.
func TestTaskHeaderOnlyOnChatRequests(t *testing.T) {
	var seen http.Header
	srv := assertingGateway(t, &seen)

	p := &OpenAIProvider{id: "openai", apiKey: "sk-test", baseURL: srv.URL}
	p.fetchDynamicModels(context.Background())
	if got := seen.Get(headerTask); got != "" {
		t.Errorf("models call: %s = %q, want empty", headerTask, got)
	}

	ch, err := p.StreamChat(context.Background(), StreamRequest{
		Model:    "m",
		Messages: []ModelMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	for range ch {
	}
	if got := seen.Get(headerTask); got != "" {
		t.Errorf("chat call with no task: %s = %q, want empty", headerTask, got)
	}
}

// TestTaskRiskWireValue pins the auto-mode risk kind's spelling. The value is a
// wire contract with oglab's X-Client-Task reader, which maps "risk" onto its
// risk-profile arm; renaming the constant is free, renaming what goes over the
// wire is not.
func TestTaskRiskWireValue(t *testing.T) {
	if got := string(TaskRisk); got != "risk" {
		t.Fatalf("TaskRisk = %q, want %q (the router maps on this spelling)", got, "risk")
	}
}

// TestSetTaskHeaderLeavesAnUnsetTaskAlone covers the helper itself, which both
// transports call unconditionally on every attempt.
func TestSetTaskHeaderLeavesAnUnsetTaskAlone(t *testing.T) {
	req, err := http.NewRequest("POST", "https://example.test/chat/completions", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	setTaskHeader(req, "")
	if got := req.Header.Get(headerTask); got != "" {
		t.Errorf("%s = %q on an unset task, want empty", headerTask, got)
	}
	setTaskHeader(req, TaskCompaction)
	if got := req.Header.Get(headerTask); got != string(TaskCompaction) {
		t.Errorf("%s = %q, want %q", headerTask, got, TaskCompaction)
	}
}
