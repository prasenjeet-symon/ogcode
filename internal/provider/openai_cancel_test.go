package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestOpenAIStreamChat_CancelUnblocksSilentStream reproduces the mid-loop
// guidance "cancel current work" scenario against a stalled endpoint: the
// server accepts the request, returns 200 with SSE headers, then goes silent.
// Cancelling the context passed to StreamChat MUST close the event channel
// promptly — otherwise the agent loop blocks forever in `for evt := range ch`,
// never reaching the top of the next iteration to drain the guidance. That is
// exactly the observed "Guidance queued — nothing happens / 0-part assistant
// message" hang on the free Cohere endpoint.
func TestOpenAIStreamChat_CancelUnblocksSilentStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done() // stay connected, emit nothing
	}))
	defer srv.Close()

	p := &OpenAIProvider{id: "test", baseURL: srv.URL, model: "test-model"}

	ctx, cancel := context.WithCancel(context.Background())
	req := StreamRequest{
		Model:    "test-model",
		Messages: []ModelMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	}
	ch, err := p.StreamChat(ctx, req)
	if err != nil {
		t.Fatalf("StreamChat returned error: %v", err)
	}

	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // channel closed after cancellation — PASS
			}
		case <-deadline:
			t.Fatal("stream channel did not close within 3s of context cancellation — the agent loop would hang here")
		}
	}
}
