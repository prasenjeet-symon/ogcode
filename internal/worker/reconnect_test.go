package worker

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	cpv1 "github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1"
	"github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1/controlplanev1connect"
)

// fakeMaster is a minimal in-memory control-plane that lets a test drive the
// worker's connect/reconnect/re-pair lifecycle without the real master.
type fakeMaster struct {
	registerCount   atomic.Int32
	helloCount      atomic.Int32
	rejectHeartbeat atomic.Bool
	workerOpened    chan struct{}
	// tunnel state — only active when the test installs onTunnel (below).
	tunnelMu     sync.Mutex
	tunnelRoutes map[string]int // route -> handshake count
	tunnelTokens []string       // token from each handshake, in order
	tunnelSeen   chan string    // buffered; gets the route of each handshake
	tunnelDone   chan struct{}  // closed when the test wants Tunnel handlers to return
	// onTunnel, when set, is called after the handshake frame is recorded; the
	// acceptance test uses it to open a yamux client session and drive a real
	// HTTP request through the tunnel.
	onTunnel func(route string, stream *connect.BidiStream[cpv1.TunnelChunk, cpv1.TunnelChunk])
	controlplanev1connect.UnimplementedControlPlaneServiceHandler
}

func (f *fakeMaster) Register(ctx context.Context, req *connect.Request[cpv1.RegisterRequest]) (*connect.Response[cpv1.RegisterResponse], error) {
	f.registerCount.Add(1)
	return connect.NewResponse(&cpv1.RegisterResponse{
		WorkerId:         req.Msg.GetWorkerId(),
		WorkerToken:      "tok",
		TokenExpiresUnix: time.Now().Add(time.Hour).Unix(),
	}), nil
}

func (f *fakeMaster) Heartbeat(ctx context.Context, req *connect.Request[cpv1.HeartbeatRequest]) (*connect.Response[cpv1.HeartbeatResponse], error) {
	if f.rejectHeartbeat.Load() {
		return nil, connect.NewError(connect.CodeUnauthenticated, io.ErrUnexpectedEOF)
	}
	return connect.NewResponse(&cpv1.HeartbeatResponse{}), nil
}

func (f *fakeMaster) WorkerStream(_ context.Context, stream *connect.BidiStream[cpv1.WorkerToMaster, cpv1.MasterToWorker]) error {
	// First frame must be Hello — count it so a test can observe the worker
	// actually opened a session.
	_, err := stream.Receive()
	if err != nil {
		return err
	}
	f.helloCount.Add(1)
	select {
	case f.workerOpened <- struct{}{}:
	default:
	}
	// Stay open; the test cancels via the worker's parent context.
	frames := 0
	for {
		if _, err := stream.Receive(); err != nil {
			return err
		}
		frames++
	}
}

// Tunnel mirrors the master's handler: consume the auth+route first frame,
// record it, then hold the stream open (the worker's yamux server writes
// keepalive frames that must be consumed until the test ends the stream).
func (f *fakeMaster) Tunnel(_ context.Context, stream *connect.BidiStream[cpv1.TunnelChunk, cpv1.TunnelChunk]) error {
	frame, err := stream.Receive()
	if err != nil {
		return err
	}
	f.tunnelMu.Lock()
	if f.tunnelRoutes == nil {
		f.tunnelRoutes = make(map[string]int)
	}
	f.tunnelRoutes[frame.GetRoute()]++
	f.tunnelTokens = append(f.tunnelTokens, string(frame.GetData()))
	f.tunnelMu.Unlock()
	if f.tunnelSeen != nil {
		select {
		case f.tunnelSeen <- frame.GetRoute():
		default:
		}
	}
	if f.onTunnel != nil {
		f.onTunnel(frame.GetRoute(), stream)
		return nil
	}
	<-f.tunnelDone // hold the RPC open until the test closes it
	return nil
}

// tunnelHandshakes snapshots the recorded route handshake counts.
func (f *fakeMaster) tunnelHandshakes() map[string]int {
	f.tunnelMu.Lock()
	defer f.tunnelMu.Unlock()
	out := make(map[string]int, len(f.tunnelRoutes))
	for k, v := range f.tunnelRoutes {
		out[k] = v
	}
	return out
}

func (f *fakeMaster) serve(t *testing.T) string {
	t.Helper()
	f.workerOpened = make(chan struct{}, 1)
	f.tunnelDone = make(chan struct{})
	f.tunnelSeen = make(chan string, 16)
	mux := http.NewServeMux()
	path, handler := controlplanev1connect.NewControlPlaneServiceHandler(f)
	mux.Handle(path, handler)
	hs := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	t.Cleanup(hs.Close)
	return hs.URL
}

// newTestWorker builds a Worker pointing at the given master URL with a
// discard logger (no client set, so openTunnel no-ops).
func newTestWorker(masterURL string) *Worker {
	return New(Options{
		MasterURL:     masterURL,
		PairingSecret: "secret",
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// TestRun_ResumesStoredTokenSkipsRegister verifies the Phase C resume path: a
// worker with a persisted, unexpired token reconnects by opening the stream
// directly (Hello) without calling Register — no re-pair.
func TestRun_ResumesStoredTokenSkipsRegister(t *testing.T) {
	isolateHome(t)
	if err := persistCred("good-token", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	fm := &fakeMaster{}
	url := fm.serve(t)
	w := newTestWorker(url)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	<-fm.workerOpened // Hello received => a session is up

	if n := fm.registerCount.Load(); n != 0 {
		t.Fatalf("Register called %d times for a resume, want 0 (no re-pair)", n)
	}

	cancel()
	select {
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not exit after cancel")
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned: %v", err)
		}
	}
}

// TestRun_HeartbeatRejectionTriggersReregister verifies the reauth fix: once the
// master rejects a heartbeat as Unauthenticated, the worker tears the session
// down and DOES call Register again (a fresh token), instead of looping forever.
func TestRun_HeartbeatRejectionTriggersReregister(t *testing.T) {
	isolateHome(t)
	fm := &fakeMaster{}
	url := fm.serve(t)
	// Reject every heartbeat so the first one triggers reauth.
	fm.rejectHeartbeat.Store(true)

	w := newTestWorker(url)
	w.heartbeatInterval = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	// Initial register + session up.
	<-fm.workerOpened
	// Wait for the re-pair: a second Register after the heartbeat rejection, and
	// the freshly issued token adopted. The rejection → reauth → register cycle
	// repeats while rejectHeartbeat is set (each new session is rejected in
	// turn), so poll both conditions together: the counter may advance again and
	// a later reauth may have already cleared the newest token when only the
	// counter is checked.
	deadline := time.Now().Add(5 * time.Second)
	registered, hasToken := false, false
	for time.Now().Before(deadline) {
		if fm.registerCount.Load() >= 2 {
			registered = true
		}
		if w.getToken() != "" {
			hasToken = true
		}
		if registered && hasToken {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := fm.registerCount.Load(); n < 2 {
		t.Fatalf("heartbeat rejection did not trigger a re-register: Register called %d times, want >= 2", n)
	}
	if !hasToken {
		t.Fatal("worker never adopted a fresh token after re-register")
	}

	cancel()
	<-done
}
