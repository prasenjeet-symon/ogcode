package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAssembleEmbeddings(t *testing.T) {
	e := func(f float32) []float32 { return []float32{f} }

	t.Run("in order", func(t *testing.T) {
		got, err := assembleEmbeddings([]embedItem{{0, e(1)}, {1, e(2)}, {2, e(3)}}, 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 3 || got[0][0] != 1 || got[1][0] != 2 || got[2][0] != 3 {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("placed by index, not arrival order", func(t *testing.T) {
		got, err := assembleEmbeddings([]embedItem{{2, e(3)}, {0, e(1)}, {1, e(2)}}, 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got[0][0] != 1 || got[1][0] != 2 || got[2][0] != 3 {
			t.Fatalf("not placed by index: %v", got)
		}
	})

	// The whole point of the helper: a provider index past the end must be an
	// error, never an out-of-range panic.
	t.Run("index past end is an error, not a panic", func(t *testing.T) {
		if _, err := assembleEmbeddings([]embedItem{{0, e(1)}, {5, e(2)}}, 2); err == nil {
			t.Fatal("want an error for an out-of-range index, got nil")
		}
	})

	t.Run("negative index is an error", func(t *testing.T) {
		if _, err := assembleEmbeddings([]embedItem{{-1, e(1)}}, 1); err == nil {
			t.Fatal("want an error for a negative index")
		}
	})

	t.Run("count mismatch is an error", func(t *testing.T) {
		if _, err := assembleEmbeddings([]embedItem{{0, e(1)}}, 2); err == nil {
			t.Fatal("want an error when the response count != inputs")
		}
	})

	t.Run("duplicate index is an error", func(t *testing.T) {
		if _, err := assembleEmbeddings([]embedItem{{0, e(1)}, {0, e(2)}}, 2); err == nil {
			t.Fatal("want an error for a duplicate index that would leave a gap")
		}
	})
}

// fullSSEServer serves a complete SSE stream (the given chunks, then [DONE]).
func fullSSEServer(t *testing.T, chunks []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			if fl != nil {
				fl.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		if fl != nil {
			fl.Flush()
		}
	}))
}

func collectStreamEvents(t *testing.T, ch <-chan StreamEvent) []StreamEvent {
	t.Helper()
	var out []StreamEvent
	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, evt)
		case <-deadline:
			t.Fatal("stream channel did not close within 5s")
		}
	}
}

// TestOpenAIStreamChat_ArgDeltaFallsBackWhenIndexDoesNotMatch pins the hardening
// for non-conforming endpoints: the tool call starts (implicit index 0) with an
// id, but its argument deltas carry a different index (5) that maps to no started
// call. The deltas must still route to the started call via the last-started
// fallback — never to an empty id, which the agent silently drops, truncating the
// arguments.
func TestOpenAIStreamChat_ArgDeltaFallsBackWhenIndexDoesNotMatch(t *testing.T) {
	chunks := []string{
		`{"choices":[{"delta":{"tool_calls":[{"id":"call_1","function":{"name":"read","arguments":""}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":5,"function":{"arguments":"{\"path\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":5,"function":{"arguments":"\"x\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	srv := fullSSEServer(t, chunks)
	defer srv.Close()

	p := &OpenAIProvider{id: "test", baseURL: srv.URL, model: "test-model"}
	ch, err := p.StreamChat(context.Background(), StreamRequest{
		Model:    "test-model",
		Messages: []ModelMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	var startID string
	var args []byte
	sawDelta := false
	for _, evt := range collectStreamEvents(t, ch) {
		switch evt.Type {
		case EventToolCallStart:
			startID = evt.ToolCallID
			args = append(args, evt.ToolInput...)
		case EventToolCallDelta:
			sawDelta = true
			if evt.ToolCallID == "" {
				t.Error("argument delta emitted with an empty tool-call id; the agent would drop these bytes")
			}
			if evt.ToolCallID != startID {
				t.Errorf("delta routed to %q, want the started call %q", evt.ToolCallID, startID)
			}
			args = append(args, evt.ToolInput...)
		}
	}

	if startID != "call_1" {
		t.Fatalf("tool-call start id = %q, want call_1", startID)
	}
	if !sawDelta {
		t.Fatal("no argument deltas were emitted")
	}
	if string(args) != `{"path":"x"}` {
		t.Errorf("accumulated arguments = %q, want %q", string(args), `{"path":"x"}`)
	}
}
