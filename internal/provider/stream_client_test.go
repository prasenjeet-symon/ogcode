package provider

import (
	"net/http"
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
