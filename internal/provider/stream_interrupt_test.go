package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// truncatingSSEServer serves one well-formed SSE event and then rips the
// connection out from under the client mid-response — no finish event, no
// [DONE]. That is what a provider dropping a long generation looks like on the
// wire, and the failure must reach the caller as a described error rather than
// as a silently closed channel.
func truncatingSSEServer(t *testing.T, firstEvent string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("response writer is not a Flusher")
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", firstEvent)
		flusher.Flush()

		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("response writer is not a Hijacker")
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		// Raw bytes bypass the chunked framing, so the client sees a body that
		// ends part-way through — exactly a connection dropped mid-stream.
		buf.WriteString("data: {\"type\":\"content_block_delta\"")
		buf.Flush()
		conn.Close()
	}))
}

// collectStreamError drains ch and returns the text of the first error event.
func collectStreamError(t *testing.T, ch <-chan StreamEvent) string {
	t.Helper()
	var errText string
	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return errText
			}
			if evt.Type == EventError && errText == "" {
				errText = evt.Error
			}
		case <-deadline:
			t.Fatal("stream channel did not close within 5s")
		}
	}
}

func TestOpenAIStreamChat_TruncatedStreamReportsError(t *testing.T) {
	srv := truncatingSSEServer(t, `{"choices":[{"delta":{"content":"hi"}}]}`)
	defer srv.Close()

	p := &OpenAIProvider{id: "test", baseURL: srv.URL, model: "test-model"}
	ch, err := p.StreamChat(context.Background(), StreamRequest{
		Model:    "test-model",
		Messages: []ModelMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	})
	if err != nil {
		t.Fatalf("StreamChat returned error: %v", err)
	}

	errText := collectStreamError(t, ch)
	if errText == "" {
		t.Fatal("truncated stream closed the channel with no error event — the agent loop can only report an unexplained interruption")
	}
	if !strings.HasPrefix(errText, "stream read failed") {
		t.Errorf("error event does not describe the read failure: %q", errText)
	}
}

func TestAnthropicStreamChat_TruncatedStreamReportsError(t *testing.T) {
	srv := truncatingSSEServer(t, `{"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}`)
	defer srv.Close()

	p := &AnthropicProvider{apiKey: "test-key", baseURL: srv.URL, model: "test-model"}
	ch, err := p.StreamChat(context.Background(), StreamRequest{
		Model:    "test-model",
		Messages: []ModelMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	})
	if err != nil {
		t.Fatalf("StreamChat returned error: %v", err)
	}

	errText := collectStreamError(t, ch)
	if errText == "" {
		t.Fatal("truncated stream closed the channel with no error event — the agent loop can only report an unexplained interruption")
	}
	if !strings.HasPrefix(errText, "stream read failed") {
		t.Errorf("error event does not describe the read failure: %q", errText)
	}
}

func TestDescribeStreamReadError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		idleFired bool
		want      string
	}{
		{"over-long line", bufio.ErrTooLong, false, "larger than 16 MB"},
		{"idle timeout wins over its own cancel", context.Canceled, true, "no data received for 2m0s"},
		{"caller cancelled", context.Canceled, false, "request cancelled"},
		{"deadline", context.DeadlineExceeded, false, "deadline exceeded"},
		{"truncated body", io.ErrUnexpectedEOF, false, "closed the connection mid-response"},
		{"wrapped read error", fmt.Errorf("read tcp: %w", errors.New("connection reset by peer")), false, "connection reset by peer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := describeStreamReadError(tt.err, tt.idleFired, streamIdleTimeout)
			if !strings.Contains(got, tt.want) {
				t.Errorf("describeStreamReadError(%v, %v) = %q, want it to mention %q", tt.err, tt.idleFired, got, tt.want)
			}
		})
	}
}

// TestIdleWatchdogResetsOnRead pins the reason the watchdog wraps the body:
// it must extend its deadline when bytes arrive off the wire, independently of
// how fast the consumer drains the parsed events.
func TestIdleWatchdogResetsOnRead(t *testing.T) {
	cancelled := false
	w := newIdleWatchdog(strings.NewReader("hello"), func() { cancelled = true }, streamIdleTimeout)
	defer w.Stop()

	buf := make([]byte, 5)
	n, err := w.Read(buf)
	if err != nil || n != 5 {
		t.Fatalf("Read = (%d, %v), want (5, nil)", n, err)
	}
	if string(buf) != "hello" {
		t.Errorf("Read returned %q, want %q", buf, "hello")
	}
	if w.Fired() || cancelled {
		t.Error("watchdog fired on a stream that just delivered data")
	}
}
