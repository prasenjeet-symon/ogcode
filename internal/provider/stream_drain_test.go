package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestStreamEventsDrainsSoHTTP11ConnectionsAreReused pins the reason a finished
// stream keeps reading after "data: [DONE]": on HTTP/1.1 a response body closed
// before EOF takes the socket with it, because net/http cannot know how much of
// the chunked body went unread. Every completion on such an endpoint then pays
// its own TCP+TLS handshake — local model servers, any proxy without h2, and any
// tapped connection are all in that set. Measured before the drain: 3 requests
// opened 3 connections; with it, 3 open 1.
func TestStreamEventsDrainsSoHTTP11ConnectionsAreReused(t *testing.T) {
	var opened atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		// A well-behaved SSE server: one chunk, the sentinel, then the end of
		// the response. The pause models the real thing — the terminator is the
		// last of a stream written over seconds, so it lands in a later packet
		// than the data the reader stopped on. Without the gap the whole body
		// arrives in one read and the socket is reused by accident, which would
		// let this test pass with no drain at all.
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"hi"}}]}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
		time.Sleep(50 * time.Millisecond)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			opened.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)

	client := &http.Client{Transport: &http.Transport{}}
	t.Cleanup(client.CloseIdleConnections)
	p := &OpenAIProvider{}

	const requests = 3
	for i := 0; i < requests; i++ {
		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
		ch := make(chan StreamEvent, 64)
		p.streamEvents(resp.Body, ch, func() {}, nil)

		sawText := false
		for ev := range ch {
			if ev.Type == EventError {
				t.Fatalf("request %d: %s", i+1, ev.Error)
			}
			if ev.Type == EventTextDelta && ev.Text == "hi" {
				sawText = true
			}
		}
		if !sawText {
			t.Fatalf("request %d: stream delivered no text", i+1)
		}
	}

	if got := opened.Load(); got != 1 {
		t.Errorf("connections opened = %d for %d requests, want 1 — a stream that stops at [DONE] without draining discards its HTTP/1.1 socket", got, requests)
	}
}

// TestStreamEventsDrainIsBounded pins the one way the drain could hurt: a server
// that sends its sentinel and then holds the connection open without closing it.
// The drain must give up on its budget instead of blocking the reader goroutine
// — and with it the request's slot — indefinitely.
func TestStreamEventsDrainIsBounded(t *testing.T) {
	saved := postDoneDrainBudget
	postDoneDrainBudget = 150 * time.Millisecond
	t.Cleanup(func() { postDoneDrainBudget = saved })

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	client := &http.Client{Transport: &http.Transport{}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}

	ch := make(chan StreamEvent, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&OpenAIProvider{}).streamEvents(resp.Body, ch, cancel, nil)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("streamEvents blocked past its drain budget on a server that never closes")
	}
	for range ch {
	}
}

// TestStreamChatReusesOneConnectionAcrossCompletions proves the drain where it
// is actually bought: the reader sits behind StreamChat's goroutine and the
// budgetBody that holds the request's concurrency slot, so a drain wired up
// wrongly there (reading the reservation out from under a closed body, say)
// would keep the unit test above green and still hand every step its own
// handshake. Three completions on one HTTP/1.1 endpoint must share one
// connection.
func TestStreamChatReusesOneConnectionAcrossCompletions(t *testing.T) {
	savedTransport := streamHTTPClient.Transport
	transport := &http.Transport{}
	streamHTTPClient.Transport = transport
	t.Cleanup(func() {
		streamHTTPClient.Transport = savedTransport
		transport.CloseIdleConnections()
	})

	var opened atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"hi"},"finish_reason":"stop"}]}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
		// Same reason as the reader-level pin: the terminator has to land in a
		// later packet than the data the stream reader stopped on, or the body
		// arrives in one read and the socket is reused with no drain at all.
		time.Sleep(50 * time.Millisecond)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			opened.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)

	p := &OpenAIProvider{id: "openai", apiKey: "test-key", baseURL: srv.URL, model: "test-model"}
	const requests = 3
	for i := 0; i < requests; i++ {
		ch, err := p.StreamChat(context.Background(), StreamRequest{
			Model:    "test-model",
			Messages: []ModelMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
		})
		if err != nil {
			t.Fatalf("completion %d: StreamChat = %v", i+1, err)
		}
		sawText := false
		for ev := range ch {
			if ev.Type == EventError {
				t.Fatalf("completion %d: %s", i+1, ev.Error)
			}
			if ev.Type == EventTextDelta && ev.Text == "hi" {
				sawText = true
			}
		}
		if !sawText {
			t.Fatalf("completion %d: stream delivered no text", i+1)
		}
	}

	if got := opened.Load(); got != 1 {
		t.Errorf("connections opened = %d for %d completions, want 1 — each step is paying its own handshake", got, requests)
	}
}

// blockingAfterErrorBody serves one SSE line, then a read error, then blocks on
// every later read — so a drain attempted on the error path hangs instead of
// reporting the failure.
type blockingAfterErrorBody struct {
	mu     sync.Mutex
	reads  int
	closed chan struct{}
}

var errBrokenStream = errors.New("stream broke")

func newBlockingAfterErrorBody() *blockingAfterErrorBody {
	return &blockingAfterErrorBody{closed: make(chan struct{})}
}

func (b *blockingAfterErrorBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	b.reads++
	n := b.reads
	b.mu.Unlock()
	switch n {
	case 1:
		return copy(p, `data: {"choices":[{"delta":{"content":"hi"}}]}`+"\n\n"), nil
	case 2:
		return 0, errBrokenStream
	default:
		<-b.closed
		return 0, io.ErrClosedPipe
	}
}

func (b *blockingAfterErrorBody) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}

// TestStreamEventsDoesNotDrainAfterAReadError guards the other half of the
// drain's contract: it runs only after a clean "data: [DONE]". A stream that
// failed mid-read has no terminator to reach, and reading on would turn a
// reported failure into a hang.
func TestStreamEventsDoesNotDrainAfterAReadError(t *testing.T) {
	body := newBlockingAfterErrorBody()
	ch := make(chan StreamEvent, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&OpenAIProvider{}).streamEvents(body, ch, func() {}, nil)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = body.Close()
		t.Fatal("streamEvents blocked draining a stream that failed mid-read")
	}

	sawError := false
	for ev := range ch {
		if ev.Type == EventError {
			sawError = true
		}
	}
	if !sawError {
		t.Error("no EventError reported for a stream that failed mid-read")
	}
}
