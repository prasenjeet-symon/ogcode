package worker

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestServerManager_EnsureSpawnsAnsweringServer pins the core Phase-2
// guarantee: ensure(dir) spawns a real ogcode server for that directory that
// answers /api/config on 127.0.0.1:<port>, and returns the same host on a
// second call for the same dir.
func TestServerManager_EnsureSpawnsAnsweringServer(t *testing.T) {
	w := newHostingWorker(t)
	m := newServerManager(t.Context(), w.logger)

	dir := t.TempDir()
	h, err := m.ensure(dir)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if h.port == 0 {
		t.Fatal("ensure returned a host with no port")
	}

	resp, err := http.Get(dialAddr(h.port) + "/api/config")
	if err != nil {
		t.Fatalf("GET /api/config: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/config status = %d, want 200", resp.StatusCode)
	}

	h2, err := m.ensure(dir)
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if h2 != h {
		t.Fatal("second ensure for the same dir returned a different host")
	}

	m.stopAll()
	// Graceful shutdown (listener close, DB close) takes a moment; the server
	// must stop accepting within a few seconds.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", tcpAddr(h.port), 500*time.Millisecond)
		if err != nil {
			return // listener is down
		}
		c.Close()
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("server still accepting 10s after stopAll")
}

// TestServerManager_DistinctDirsDistinctServers pins one server per worktree:
// two directories get two live servers on different ports.
func TestServerManager_DistinctDirsDistinctServers(t *testing.T) {
	w := newHostingWorker(t)
	m := newServerManager(t.Context(), w.logger)

	h1, err := m.ensure(t.TempDir())
	if err != nil {
		t.Fatalf("ensure dir1: %v", err)
	}
	h2, err := m.ensure(t.TempDir())
	if err != nil {
		t.Fatalf("ensure dir2: %v", err)
	}
	if h1 == h2 || h1.port == h2.port {
		t.Fatalf("expected distinct servers, both returned port %d", h1.port)
	}
	m.stopAll()
}

// TestServerManager_ManagerCtxCancelStopsServers pins that cancelling the
// manager's context tears down every spawned server — the worker's ctx owns
// its servers.
func TestServerManager_ManagerCtxCancelStopsServers(t *testing.T) {
	w := newHostingWorker(t)
	ctx, cancel := contextWithCancel()
	m := newServerManager(ctx, w.logger)

	h, err := m.ensure(t.TempDir())
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	cancel()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", tcpAddr(h.port), 500*time.Millisecond)
		if err != nil {
			return // listener is down
		}
		c.Close()
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("server still accepting 10s after manager ctx cancel")
}

// dialAddr is the loopback dial target (URL form) for a server port.
func dialAddr(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// tcpAddr is the dial target for a plain TCP probe of a server port.
func tcpAddr(port int) string {
	return net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port))
}

// contextWithCancel mirrors context.WithCancel for the tests' readability.
func contextWithCancel() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}
