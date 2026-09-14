package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"

	cpv1 "github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1"
	"github.com/prasenjeet-symon/ogcode-control-plane/tunnel"
)

// eagerWorkspaceWorker builds a worker wired for eager-tunnel tests: isolated
// home (stable worker id), neutralized provider env (no key probes from the
// spawned per-dir servers), and one workspace root.
func eagerWorkspaceWorker(t *testing.T, masterURL, dir string) *Worker {
	t.Helper()
	isolateHome(t)
	neutralizeProviderEnv(t)
	w := newTestWorker(masterURL)
	w.opts.Workspaces = []string{dir}
	return w
}

// runWorkerForTest drives w.Run until the fake master has received the Hello
// (which happens right after the worker kicks the eager pass), and arranges
// cancellation + shutdown on cleanup.
func runWorkerForTest(t *testing.T, w *Worker, fm *fakeMaster) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		w.shutdown()
	})
	go func() { _ = w.Run(ctx) }()
	select {
	case <-fm.workerOpened:
	case <-time.After(10 * time.Second):
		t.Fatal("worker never sent its Hello")
	}
}

// waitForRoute blocks until the fake master records a handshake for route.
func waitForRoute(t *testing.T, seen <-chan string, route string) {
	t.Helper()
	select {
	case got := <-seen:
		if got != route {
			t.Fatalf("tunnel handshake for wrong route: got %q, want %q", got, route)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("tunnel handshake for route %q never arrived", route)
	}
}

// TestRun_OpensEagerTunnelsForDiscoveredWorkspaces pins Phase F's eager pass:
// at connect time — before any StartAgent — the worker spawns a per-dir server
// for each discovered workspace and opens exactly one tunnel for it, carrying
// the worktree's route label and an authentic worker token.
func TestRun_OpensEagerTunnelsForDiscoveredWorkspaces(t *testing.T) {
	fm := &fakeMaster{}
	masterURL := fm.serve(t)

	dir := t.TempDir() // any existing dir counts as a workspace root
	route := worktreeLabel(dir)

	w := eagerWorkspaceWorker(t, masterURL, dir)
	runWorkerForTest(t, w, fm)

	waitForRoute(t, fm.tunnelSeen, route)

	// The per-dir server must already be up, before any StartAgent.
	sh := w.srvs.get(dir)
	if sh == nil {
		t.Fatal("no server spawned for discovered workspace")
	}
	if sh.port == 0 {
		t.Fatal("spawned server has no port")
	}

	// Exactly one tunnel per dir: no duplicate handshake after a grace period.
	time.Sleep(300 * time.Millisecond)
	if n := fm.tunnelHandshakes()[route]; n != 1 {
		t.Fatalf("tunnel handshakes for %q = %d, want exactly 1", route, n)
	}

	// And the token on the first frame authenticates the worker.
	fm.tunnelMu.Lock()
	tokens := append([]string(nil), fm.tunnelTokens...)
	fm.tunnelMu.Unlock()
	if len(tokens) != 1 || tokens[0] != "tok" {
		t.Fatalf("tunnel handshake tokens = %v, want [tok]", tokens)
	}
}

// TestTunnelSplicesToLocalWorktreeUI is the Phase F(a) acceptance: a real HTTP
// request driven through the tunnel — the master side opens a yamux stream, the
// worker accepts and splices it to the worktree's local ogcode server — must be
// answered by THAT worktree's native UI surface (GET /api/config reporting the
// worktree directory). No special-cased UI server involved.
func TestTunnelSplicesToLocalWorktreeUI(t *testing.T) {
	fm := &fakeMaster{}
	masterURL := fm.serve(t)

	dir := t.TempDir()
	route := worktreeLabel(dir)

	answer := make(chan string, 1)
	report := func(msg string) {
		select {
		case answer <- msg:
		default:
		}
	}
	fm.onTunnel = func(r string, stream *connect.BidiStream[cpv1.TunnelChunk, cpv1.TunnelChunk]) {
		if r != route {
			report(fmt.Sprintf("unexpected route %q", r))
			return
		}
		// Master side of the tunnel: yamux client over the bidi stream.
		sess, err := tunnel.ClientSession(stream, nil)
		if err != nil {
			report(fmt.Sprintf("client session: %v", err))
			return
		}
		defer sess.Close()
		client := &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return sess.Open()
				},
			},
		}
		resp, err := client.Get("http://ogcode-worker/api/config")
		if err != nil {
			report(fmt.Sprintf("GET through tunnel: %v", err))
			return
		}
		defer resp.Body.Close()
		var payload struct {
			Directory string `json:"directory"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			report(fmt.Sprintf("decode /api/config: %v", err))
			return
		}
		report(payload.Directory)
	}

	w := eagerWorkspaceWorker(t, masterURL, dir)
	runWorkerForTest(t, w, fm)

	select {
	case got := <-answer:
		if got != dir {
			t.Fatalf("tunneled /api/config answered directory %q, want the workspace dir %q", got, dir)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no answer from the tunneled UI")
	}
}

// TestStartAgent_AfterEagerTunnelDoesNotDuplicateTunnel pins that a StartAgent
// for a directory the eager pass already wired adds nothing: still exactly one
// tunnel handshake per dir.
func TestStartAgent_AfterEagerTunnelDoesNotDuplicateTunnel(t *testing.T) {
	fm := &fakeMaster{}
	masterURL := fm.serve(t)

	dir := t.TempDir()
	route := worktreeLabel(dir)

	w := eagerWorkspaceWorker(t, masterURL, dir)
	runWorkerForTest(t, w, fm)

	waitForRoute(t, fm.tunnelSeen, route)

	// A real StartAgent command for the same dir must not re-handshake.
	if err := w.startAgent(&cpv1.StartAgent{
		SessionId: "ses_eagertest",
		Workspace: dir,
		Prompt:    "test prompt",
	}); err != nil {
		t.Fatalf("startAgent: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if n := fm.tunnelHandshakes()[route]; n != 1 {
		t.Fatalf("tunnel handshakes for %q = %d after StartAgent, want still 1", route, n)
	}
}
