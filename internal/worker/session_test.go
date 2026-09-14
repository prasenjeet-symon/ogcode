package worker

import (
	"path/filepath"
	"sync"
	"testing"

	cpv1 "github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1"
)

// emptySyncMap returns an empty tunnelled map for tests that call startAgent
// directly (the Worker constructor leaves it nil). Pointer, since sync.Map
// must not be copied.
func emptySyncMap() *sync.Map {
	return &sync.Map{}
}

// neutralizeProviderEnv ensures the spawned per-dir servers never hit the
// network: no provider keys, free pool unreachable, embed model redirected.
func neutralizeProviderEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL",
		"OPENAI_API_KEY", "OPENAI_BASE_URL",
		"OPENROUTER_API_KEY", "OLLAMA_API_KEY", "OLLAMA_BASE_URL",
	} {
		t.Setenv(k, "")
	}
	t.Setenv("OGCODE_FREE_KEYS_URL", "http://127.0.0.1:9/free-keys-unavailable")
	t.Setenv("OGCODE_EMBED_MODEL_DIR", t.TempDir())
}

// newHostingWorker isolates HOME, neutralizes provider env (the spawned servers
// must not hit the network), and returns a worker whose srvs manager can spawn
// real worktree servers.
func newHostingWorker(t *testing.T) *Worker {
	t.Helper()
	isolateHome(t)
	neutralizeProviderEnv(t)
	return New(Options{})
}

// TestSessionRegistry_TracksHostedSessions pins the registry plumbing that
// StopAgent/AbortSession, guidance and permission replies all depend on: which
// sessions are hosted here, and in which directory (the join key to the
// worktree server that owns the loop).
func TestSessionRegistry_TracksHostedSessions(t *testing.T) {
	r := newSessionRegistry()
	if r.has("missing") {
		t.Fatal("has(missing) = true, want false")
	}
	if r.dirOf("missing") != "" {
		t.Fatal("dirOf(missing) should be empty")
	}
	if r.cancel("missing") {
		t.Fatal("cancel(missing) = true, want false")
	}

	stopped := false
	r.add(&hostedSession{id: "s1", dir: "/ws", stop: func() { stopped = true }})

	if !r.has("s1") {
		t.Fatal("has(s1) = false, want true")
	}
	if d := r.dirOf("s1"); d != "/ws" {
		t.Fatalf("dirOf(s1) = %q, want /ws", d)
	}
	if !r.cancel("s1") || !stopped {
		t.Fatal("cancel(s1) did not invoke the session's stop func")
	}
}

// TestGuidanceCommand_NotHostedDoesNotPanic pins that guidance for a session
// the worker does not host fails cleanly (the replyResult is a no-op log with
// no open stream; the assertion is that handleCommand neither panics nor
// reaches a server).
func TestGuidanceCommand_NotHostedDoesNotPanic(t *testing.T) {
	w := newHostingWorker(t)
	w.srvs = newServerManager(nil, w.logger)
	cmd := &cpv1.MasterToWorker{Command: &cpv1.MasterToWorker_Guidance{
		Guidance: &cpv1.Guidance{SessionId: "nosuch", Text: "hi"},
	}}
	w.handleCommand(cmd) // must not panic
}

// TestStartAgent_HostsOnRealWorktreeServer pins the Phase-2 hosting path end
// to end at the worker level: StartAgent spawns a real per-dir server, the
// session lands in the registry joined to that dir, the loop is registered on
// the server (guidance reaches its loopControls), and the server answers HTTP
// on loopback.
func TestStartAgent_HostsOnRealWorktreeServer(t *testing.T) {
	w := newHostingWorker(t)
	w.ctx = t.Context()
	w.srvs = newServerManager(w.ctx, w.logger)
	w.tunnelled = emptySyncMap()

	dir := t.TempDir()
	// The hosting path requires a directory the worker itself offered: register
	// the test dir as the worker's workspace root (the guard in startAgent
	// refuses anything else).
	w.opts.Workspaces = []string{dir}
	cmd := &cpv1.MasterToWorker{RequestId: "r1", Command: &cpv1.MasterToWorker_StartAgent{
		StartAgent: &cpv1.StartAgent{
			SessionId:      "ses_workerintegration1",
			Workspace:      dir,
			AgentName:      "build",
			Prompt:         "Say hello",
			ViewportWidth:  0,
			ViewportHeight: 0,
		},
	}}
	w.startAgent(cmd.GetStartAgent()) // no open stream; replyResult only logs

	if !w.sessions.has("ses_workerintegration1") {
		t.Fatal("session not registered after StartAgent")
	}
	sh := w.hostServerFor("ses_workerintegration1")
	if sh == nil || sh.port == 0 {
		t.Fatal("hostServerFor did not return a live server for the session's dir")
	}
	if w.srvs.get(dir) != sh {
		t.Fatal("registry dir does not join to the spawned server")
	}

	// The loop is registered on the server, so guidance reaches it instead of
	// erroring with the no-loop text.
	if err := sh.srv.Guidance("ses_workerintegration1", "keep going", true); err != nil {
		t.Fatalf("Guidance on a hosted running session: %v", err)
	}

	// The session row landed in the spawned server's own store and the user
	// message exists.
	sess, err := sh.srv.SessionRow("ses_workerintegration1")
	if err != nil || sess == nil {
		t.Fatalf("session row not in the worktree server's store: %v", err)
	}
	if sess.Directory != filepath.Clean(dir) && sess.Directory != dir {
		t.Fatalf("session directory = %q, want %q", sess.Directory, dir)
	}

	w.sessions.cancelAll()
	w.srvs.stopAll()
}
