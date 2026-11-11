package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestComplexityHeaderReachesTheWire pins that a child agent's predicted
// difficulty rides its chat request on both transports, byte-identical to the
// Complexity the spawner set, because the router maps on that exact spelling.
func TestComplexityHeaderReachesTheWire(t *testing.T) {
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
				Model:      "m",
				Messages:   []ModelMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
				Task:       TaskSubagent,
				Complexity: ComplexityEasy,
			})
			if err != nil {
				t.Fatalf("StreamChat: %v", err)
			}
			for range ch {
			}

			if got := seen.Get(headerComplexity); got != string(ComplexityEasy) {
				t.Errorf("%s = %q, want %q", headerComplexity, got, ComplexityEasy)
			}
			// The task kind keeps its own header; complexity must not replace it.
			if got := seen.Get(headerTask); got != string(TaskSubagent) {
				t.Errorf("%s = %q, want %q", headerTask, got, TaskSubagent)
			}
		})
	}
}

// TestComplexityHeaderOnlyWhenPredicted is the counterpart: a caller with no
// prediction must not have one invented for it, so an unclassified child stays
// byte-identical to the requests this code sent before the field existed.
func TestComplexityHeaderOnlyWhenPredicted(t *testing.T) {
	var seen http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	p := &OpenAIProvider{id: "openai", apiKey: "sk-test", baseURL: srv.URL}
	ch, err := p.StreamChat(context.Background(), StreamRequest{
		Model:    "m",
		Messages: []ModelMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	for range ch {
	}
	if got := seen.Get(headerComplexity); got != "" {
		t.Errorf("chat call with no complexity: %s = %q, want empty", headerComplexity, got)
	}
}

// TestComplexityWireValues pins the three classes' spelling. They are a wire
// contract with the router's model-selection arm; renaming the constants is
// free, renaming what goes over the wire is not.
func TestComplexityWireValues(t *testing.T) {
	for _, tc := range []struct {
		c    Complexity
		want string
	}{
		{ComplexityEasy, "easy"},
		{ComplexityMedium, "medium"},
		{ComplexityHard, "hard"},
	} {
		if got := string(tc.c); got != tc.want {
			t.Errorf("complexity = %q, want %q (the router maps on this spelling)", got, tc.want)
		}
		if !tc.c.Valid() {
			t.Errorf("%q should be Valid", tc.c)
		}
	}
	if Complexity("").Valid() {
		t.Error("the empty string is not a class")
	}
	if Complexity("trivial").Valid() {
		t.Error("an unlisted spelling is not a class")
	}
}

// TestSetComplexityHeaderLeavesAnUnpredictedTaskAlone covers the helper itself,
// which both transports call unconditionally on every attempt.
func TestSetComplexityHeaderLeavesAnUnpredictedTaskAlone(t *testing.T) {
	req, err := http.NewRequest("POST", "https://example.test/chat/completions", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	setComplexityHeader(req, "")
	if got := req.Header.Get(headerComplexity); got != "" {
		t.Errorf("%s = %q with no prediction, want empty", headerComplexity, got)
	}
	setComplexityHeader(req, ComplexityHard)
	if got := req.Header.Get(headerComplexity); got != string(ComplexityHard) {
		t.Errorf("%s = %q, want %q", headerComplexity, got, ComplexityHard)
	}
}
