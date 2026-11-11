package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestWorkloadHeaderReachesTheWire pins that a child agent's predicted workload
// rides its chat request on both transports, byte-identical to the Workload the
// spawner set, because the router maps on that exact spelling. The task and
// complexity headers keep their own values beside it.
func TestWorkloadHeaderReachesTheWire(t *testing.T) {
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
				Workload:   WorkloadReadHeavy,
			})
			if err != nil {
				t.Fatalf("StreamChat: %v", err)
			}
			for range ch {
			}

			if got := seen.Get(headerWorkload); got != string(WorkloadReadHeavy) {
				t.Errorf("%s = %q, want %q", headerWorkload, got, WorkloadReadHeavy)
			}
			// The task kind and complexity class keep their own headers; workload
			// must not replace either.
			if got := seen.Get(headerTask); got != string(TaskSubagent) {
				t.Errorf("%s = %q, want %q", headerTask, got, TaskSubagent)
			}
			if got := seen.Get(headerComplexity); got != string(ComplexityEasy) {
				t.Errorf("%s = %q, want %q", headerComplexity, got, ComplexityEasy)
			}
		})
	}
}

// TestWorkloadHeaderOnlyWhenPredicted is the counterpart: a caller with no
// prediction must not have one invented for it, so an unclassified child stays
// byte-identical to the requests this code sent before the field existed.
func TestWorkloadHeaderOnlyWhenPredicted(t *testing.T) {
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
	if got := seen.Get(headerWorkload); got != "" {
		t.Errorf("chat call with no workload: %s = %q, want empty", headerWorkload, got)
	}
}

// TestWorkloadWireValues pins the two workloads' spelling. They are a wire
// contract with the router's model-selection arm; renaming the constants is
// free, renaming what goes over the wire is not.
func TestWorkloadWireValues(t *testing.T) {
	for _, tc := range []struct {
		w    Workload
		want string
	}{
		{WorkloadReadHeavy, "read-heavy"},
		{WorkloadWriteHeavy, "write-heavy"},
	} {
		if got := string(tc.w); got != tc.want {
			t.Errorf("workload = %q, want %q (the router maps on this spelling)", got, tc.want)
		}
		if !tc.w.Valid() {
			t.Errorf("%q should be Valid", tc.w)
		}
	}
	if Workload("").Valid() {
		t.Error("the empty string is not a workload")
	}
	if Workload("mixed").Valid() {
		t.Error("an unlisted spelling is not a workload")
	}
}

// TestSetWorkloadHeaderLeavesAnUnclassifiedTaskAlone covers the helper itself,
// which both transports call unconditionally on every attempt.
func TestSetWorkloadHeaderLeavesAnUnclassifiedTaskAlone(t *testing.T) {
	req, err := http.NewRequest("POST", "https://example.test/chat/completions", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	setWorkloadHeader(req, "")
	if got := req.Header.Get(headerWorkload); got != "" {
		t.Errorf("%s = %q with no prediction, want empty", headerWorkload, got)
	}
	setWorkloadHeader(req, WorkloadWriteHeavy)
	if got := req.Header.Get(headerWorkload); got != string(WorkloadWriteHeavy) {
		t.Errorf("%s = %q, want %q", headerWorkload, got, WorkloadWriteHeavy)
	}
}
