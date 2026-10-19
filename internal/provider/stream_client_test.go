package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestStreamHTTPClientHasNoRequestDeadline pins the reason streaming does not
// use http.Client.Timeout: that deadline covers the whole request including the
// body read, so a long generation loses its connection mid-stream. Liveness is
// the idle watchdog's job instead.
func TestStreamHTTPClientHasNoRequestDeadline(t *testing.T) {
	if streamHTTPClient.Timeout != 0 {
		t.Errorf("streamHTTPClient.Timeout = %s, want 0 — a whole-request deadline truncates long streams", streamHTTPClient.Timeout)
	}
}

// TestStreamHTTPClientIdleConnTimeout pins the pool below the shortest idle
// close we have measured on a relay endpoint (~60s: connections survive 50s
// idle and are gone by 70s). Pooling for longer than the peer holds a
// connection open means handing dead sockets to live requests.
func TestStreamHTTPClientIdleConnTimeout(t *testing.T) {
	tr, ok := streamHTTPClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("streamHTTPClient.Transport is %T, want *http.Transport", streamHTTPClient.Transport)
	}
	const shortestObservedPeerIdleClose = 60 * time.Second
	if tr.IdleConnTimeout <= 0 {
		t.Fatal("IdleConnTimeout = 0 (unbounded) — pooled connections outlive the peer's idle close")
	}
	if tr.IdleConnTimeout >= shortestObservedPeerIdleClose {
		t.Errorf("IdleConnTimeout = %s, want well under %s", tr.IdleConnTimeout, shortestObservedPeerIdleClose)
	}
	if tr.ResponseHeaderTimeout <= 0 {
		t.Error("ResponseHeaderTimeout = 0 — a peer that never answers would hang until the caller gives up")
	}
}

// localWriteErr is the shape net/http hands back when a network extension on
// the host refuses a socket write: the errno is ERANGE ("result too large"),
// which the kernel's own TCP stack never produces for a write.
func localWriteErr() error {
	return &net.OpError{Op: "write", Net: "tcp", Err: os.NewSyscallError("write", syscall.ERANGE)}
}

// TestIsLocalWriteFailure pins the identity match. The whole point is that the
// decision survives the provider's own wrapping — a write the local machine
// refused is the one case a re-send cannot duplicate — while a read failure
// carrying the same errno, or a different write errno, is left alone.
func TestIsLocalWriteFailure(t *testing.T) {
	wrapped := &url.Error{Op: "Post", URL: "https://ogx.ogcode.in/v1/chat/completions", Err: localWriteErr()}
	if !isLocalWriteFailure(wrapped) {
		t.Error("a url.Error around an ERANGE write is not recognised")
	}
	if !isLocalWriteFailure(fmt.Errorf("send request: %w", wrapped)) {
		t.Error("the provider's own wrapping hides the failure")
	}

	for name, err := range map[string]error{
		"read carrying the same errno": &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", syscall.ERANGE)},
		"a different write errno":      &net.OpError{Op: "write", Net: "tcp", Err: os.NewSyscallError("write", syscall.ECONNRESET)},
		"text only, no identity":       errors.New("write tcp 192.0.2.49:1->104.21.52.233:443: write: result too large"),
	} {
		if isLocalWriteFailure(err) {
			t.Errorf("%s: isLocalWriteFailure = true, want false", name)
		}
	}
}

// TestSendStreamingRequestRetriesLocalWriteOnce pins the recovery itself: one
// immediate re-send on a rebuilt request, and no more than one — a host whose
// network layer is deterministically refusing writes must not spend the turn in
// a retry loop.
func TestSendStreamingRequestRetriesLocalWriteOnce(t *testing.T) {
	var calls int
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		_, _ = io.Copy(io.Discard, r.Body)
		if calls == 1 {
			return nil, localWriteErr()
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("ok")),
			Request:    r,
		}, nil
	})}

	newReq := func() (*http.Request, error) {
		return http.NewRequest("POST", "https://ogx.ogcode.in/v1/chat/completions", strings.NewReader(`{"model":"m"}`))
	}
	req, err := newReq()
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := sendStreamingRequest(client, req, newReq)
	if err != nil {
		t.Fatalf("sendStreamingRequest = %v, want a retried success", err)
	}
	defer resp.Body.Close()
	if calls != 2 {
		t.Errorf("transport calls = %d, want 2 (the failed send plus one fresh-connection retry)", calls)
	}
}

// TestSendStreamingRequestGivesUpAfterOneRetry keeps the recovery bounded.
func TestSendStreamingRequestGivesUpAfterOneRetry(t *testing.T) {
	var calls int
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return nil, localWriteErr()
	})}
	newReq := func() (*http.Request, error) {
		return http.NewRequest("POST", "https://ogx.ogcode.in/v1/chat/completions", strings.NewReader(`{}`))
	}
	req, _ := newReq()
	if _, err := sendStreamingRequest(client, req, newReq); err == nil {
		t.Fatal("sendStreamingRequest = nil error, want the write failure")
	}
	if calls != 2 {
		t.Errorf("transport calls = %d, want 2 — one retry, never a loop", calls)
	}
}

// TestSendStreamingRequestLeavesOtherFailuresAlone guards the blast radius: a
// failure that is not a locally rejected write must reach the caller untouched,
// with no request re-sent.
func TestSendStreamingRequestLeavesOtherFailuresAlone(t *testing.T) {
	var calls int
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("some unrelated failure")
	})}
	newReq := func() (*http.Request, error) {
		return http.NewRequest("POST", "https://ogx.ogcode.in/v1/chat/completions", strings.NewReader(`{}`))
	}
	req, _ := newReq()
	if _, err := sendStreamingRequest(client, req, newReq); err == nil {
		t.Fatal("sendStreamingRequest = nil error, want the transport failure")
	}
	if calls != 1 {
		t.Errorf("transport calls = %d, want 1 — only a locally refused write is retried", calls)
	}
}

// TestStreamChatRetriesLocalWriteOnAFreshConnection proves the wiring, not just
// the helper: the failure that killed a real turn — ERANGE on the write of an
// OGX request — must now be recovered inside StreamChat, before the agent loop
// ever sees an error and turns it into a dead, resumable-looking turn.
func TestStreamChatRetriesLocalWriteOnAFreshConnection(t *testing.T) {
	saved := streamHTTPClient.Transport
	t.Cleanup(func() { streamHTTPClient.Transport = saved })

	var bodies []string
	streamHTTPClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		if len(bodies) == 1 {
			return nil, localWriteErr()
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    r,
		}, nil
	})

	p := &OpenAIProvider{id: "ogx", baseURL: "https://ogx.ogcode.in/v1", model: "test-model"}
	ch, err := p.StreamChat(context.Background(), StreamRequest{
		Model:    "test-model",
		Messages: []ModelMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	})
	if err != nil {
		t.Fatalf("StreamChat = %v, want the retried stream to open", err)
	}
	for range ch {
	}

	if len(bodies) != 2 {
		t.Fatalf("transport calls = %d, want 2", len(bodies))
	}
	// The retry has to carry the same request. A body left empty here is the
	// classic net/http retry bug: the first send consumed it.
	if bodies[0] == "" || bodies[1] == "" {
		t.Fatalf("request bodies = %q, want both non-empty", bodies)
	}
	if bodies[0] != bodies[1] {
		t.Errorf("retried body differs from the original:\n%s\n%s", bodies[0], bodies[1])
	}
}
