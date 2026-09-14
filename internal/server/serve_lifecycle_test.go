package server

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

// waitUp polls until the server answers on 127.0.0.1:<Port()>, or fails the
// test. Port() is only meaningful after the listener binds, so this doubles as
// the "did Serve actually get to the listen step" barrier.
func waitUp(t *testing.T, srv *Server) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if srv.Port() != 0 {
			c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(srv.Port())))
			if err == nil {
				c.Close()
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server never answered on 127.0.0.1:%d", srv.Port())
}

// TestServe_BindsLoopbackAndReportsPort pins the worker-hosting contract: a
// Loopback server binds 127.0.0.1 (never a LAN address), surfaces the actual
// bound port even when constructed with port 0 (the kernel-assigned value —
// the worker dials the tunnel with it), and Serve returns cleanly when the
// caller's context is cancelled.
func TestServe_BindsLoopbackAndReportsPort(t *testing.T) {
	srv := NewWithOptions(0, t.TempDir(), ModeBuild, Options{Loopback: true, NoBrowser: true})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()

	waitUp(t, srv)
	if srv.Port() == 0 {
		t.Fatal("Port() still 0 after bind — kernel-assigned port not surfaced")
	}

	resp, err := http.Get("http://127.0.0.1:" + itoa(srv.Port()) + "/api/config")
	if err != nil {
		t.Fatalf("GET /api/config: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/config status = %d, want 200", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned error after ctx cancel: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Serve did not return after context cancel")
	}
}

// TestServe_StopFromAnotherGoroutine pins the programmatic path: Stop() ends
// Serve's wait loop and Serve returns nil, without any signal or ctx cancel —
// the seam a supervisor uses to stop a hosted worktree server.
func TestServe_StopFromAnotherGoroutine(t *testing.T) {
	srv := NewWithOptions(0, t.TempDir(), ModeBuild, Options{Loopback: true, NoBrowser: true})

	done := make(chan error, 1)
	go func() { done <- srv.Serve(context.Background()) }()

	waitUp(t, srv)
	srv.Stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned error after Stop: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Serve did not return after Stop")
	}
}

// TestServe_DoubleCallRejected pins the one-Serve-per-Server contract: a second
// Serve on an already-served Server fails fast instead of double-binding or
// double-closing the DBs.
func TestServe_DoubleCallRejected(t *testing.T) {
	srv := NewWithOptions(0, t.TempDir(), ModeBuild, Options{Loopback: true, NoBrowser: true})

	done := make(chan error, 1)
	go func() { done <- srv.Serve(context.Background()) }()
	waitUp(t, srv)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(context.Background()) }()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("second Serve on a served Server must return an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second Serve did not fail fast")
	}

	srv.Stop()
	<-done
}

// TestServe_OnListenReportsActualBoundPort pins the per-project-port contract:
// OnListen fires with the port the server ACTUALLY bound, not the one it was
// asked for. That distinction is the whole point — the CLI records this value as
// the project's port, so when the requested port is busy and the bind loop walks
// past it, the walked-to port (not the busy one) is what gets remembered.
func TestServe_OnListenReportsActualBoundPort(t *testing.T) {
	// Occupy a loopback port so the server's requested port is in use and must walk.
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy a port: %v", err)
	}
	defer occupied.Close()
	busyPort := occupied.Addr().(*net.TCPAddr).Port

	// Wait on the callback directly rather than via waitUp: NewWithOptions seeds
	// Port() with the REQUESTED port, and the occupied listener answers on it, so
	// a port-based readiness probe would race ahead of the real bind.
	listened := make(chan int, 1)
	srv := NewWithOptions(busyPort, t.TempDir(), ModeBuild, Options{
		Loopback:  true,
		NoBrowser: true,
		OnListen:  func(p int) { listened <- p },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()

	var got int
	select {
	case got = <-listened:
	case <-time.After(20 * time.Second):
		t.Fatal("OnListen was never called")
	}

	if got == 0 {
		t.Fatal("OnListen reported port 0")
	}
	if got != srv.Port() {
		t.Errorf("OnListen reported %d but Port() is %d", got, srv.Port())
	}
	if got == busyPort {
		t.Errorf("OnListen reported the busy port %d; it must report the walked-to port", busyPort)
	}

	cancel()
	<-done
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
