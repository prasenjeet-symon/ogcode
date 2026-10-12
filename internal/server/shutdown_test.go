package server

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// With a UI tab open the event stream never goes idle, so http.Server.Shutdown
// waited out its whole timeout for it on every exit — ten seconds the user sat
// through after Ctrl+C. Shutdown now releases the stream first, so Serve returns
// as soon as the real work is done, well inside the grace period.
func TestServe_ShutdownReleasesTheEventStream(t *testing.T) {
	srv := NewWithOptions(0, t.TempDir(), ModeBuild, Options{Loopback: true, NoBrowser: true})
	done := make(chan error, 1)
	go func() { done <- srv.Serve(context.Background()) }()
	waitUp(t, srv)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/event", srv.Port()))
	if err != nil {
		t.Fatalf("open event stream: %v", err)
	}
	defer resp.Body.Close()
	// The first frame proves the handler is in its loop, holding the stream.
	stream := bufio.NewReader(resp.Body)
	if _, err := stream.ReadString('\n'); err != nil {
		t.Fatalf("read first frame: %v", err)
	}
	streamEnded := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, stream)
		close(streamEnded)
	}()

	start := time.Now()
	srv.Stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned error after Stop: %v", err)
		}
	case <-time.After(httpShutdownGrace + 10*time.Second):
		t.Fatal("Serve did not return after Stop")
	}
	if elapsed := time.Since(start); elapsed >= httpShutdownGrace {
		t.Fatalf("Serve took %v to return with an event stream open: the stream held up shutdown", elapsed)
	}
	select {
	case <-streamEnded:
	case <-time.After(2 * time.Second):
		t.Fatal("the event stream was never closed")
	}
}

// Stop flushes queued analytics, but each send can take the client's 10 s
// timeout, so on a slow or absent network the flush held up quitting for as
// long as the queue took to fail. It now gives up after posthogStopTimeout.
func TestPostHogStopIsBounded(t *testing.T) {
	block := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer hang.Close()
	defer close(block) // runs first: lets the hung sends return so Close can finish

	c := NewPostHogClient("test-key", hang.URL)
	c.Capture("first", "test-install", nil)
	c.Capture("second", "test-install", nil)

	start := time.Now()
	c.Stop()
	if elapsed := time.Since(start); elapsed > posthogStopTimeout+time.Second {
		t.Fatalf("Stop took %v against an unresponsive host; want it bounded near %v", elapsed, posthogStopTimeout)
	}
}
