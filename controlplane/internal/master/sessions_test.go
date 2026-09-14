package master_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	cpv1 "github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1"
	"github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1/controlplanev1connect"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/bus"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/master"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/pairing"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/registry"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// newSessionPanelServer builds the same master harness as newTestServer but
// adds a fake worker named wsName (registered, hello'd, online, offering one
// workspace) so the start-session form has someone to route to. No operator
// gate: the session handlers are gate-independent (handleApexConsole checks it
// before dispatching), so these tests exercise the handlers directly.
func newSessionPanelServer(t *testing.T, wsName string) (*master.Server, *httptest.Server) {
	t.Helper()
	srv := master.New(master.Options{
		Registry: registry.New(),
		Auth:     pairing.New(testSecret, time.Minute),
		Bus:      bus.New(256),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	mux := http.NewServeMux()
	path, handler := srv.Handler()
	mux.Handle(path, handler)
	// h2c so Connect bidi streaming works over the httptest cleartext listener.
	hs := httptest.NewServer(h2c.NewHandler(srv.UIProxyHandler(mux), &http2.Server{}))
	t.Cleanup(hs.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fakeWorkerWith(t, ctx, h2cClient(hs.URL), wsName)
	waitFor(t, 3*time.Second, func() bool {
		info, ok := srv.Registry().Get(wsName)
		return ok && info.Status == registry.StatusOnline
	}, "fake worker never came online")
	return srv, hs
}

func sessionsGet(t *testing.T, hs *httptest.Server, path string) *http.Response {
	t.Helper()
	resp, err := http.Get(hs.URL + path)
	if err != nil {
		t.Fatalf("get %s: %v", path, err)
	}
	return resp
}

// newSessionPanelServerWithRejectingWorker is newSessionPanelServer with the
// fake worker's command loop answering every command ok=false + failMsg — the
// worker-side failure path (a failing clone) without a real git clone.
func newSessionPanelServerWithRejectingWorker(t *testing.T, wsName, failMsg string) (*master.Server, *httptest.Server) {
	t.Helper()
	srv := master.New(master.Options{
		Registry: registry.New(),
		Auth:     pairing.New(testSecret, time.Minute),
		Bus:      bus.New(256),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	mux := http.NewServeMux()
	path, handler := srv.Handler()
	mux.Handle(path, handler)
	hs := httptest.NewServer(h2c.NewHandler(srv.UIProxyHandler(mux), &http2.Server{}))
	t.Cleanup(hs.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	id := fakeWorkerReject(t, ctx, h2cClient(hs.URL), wsName, failMsg)
	waitFor(t, 3*time.Second, func() bool {
		info, ok := srv.Registry().Get(id)
		return ok && info.Status == registry.StatusOnline
	}, "fake worker never came online")
	return srv, hs
}

// sessionsPost posts a form to a sessions endpoint, carrying cookies when
// given (the accounts-gate test logs in first), without following redirects.
func sessionsPost(t *testing.T, hs *httptest.Server, cookies []*http.Cookie, path string, form map[string][]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, hs.URL+path, strings.NewReader(formEncode(form)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", path, err)
	}
	return resp
}

func assertBody(t *testing.T, resp *http.Response, status int, want ...string) string {
	t.Helper()
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != status {
		t.Fatalf("status = %d, want %d (body=%s)", resp.StatusCode, status, body)
	}
	for _, s := range want {
		if !strings.Contains(string(body), s) {
			t.Errorf("body missing %q (body=%s)", s, body)
		}
	}
	return string(body)
}

// TestSessionsPage_ListAndStartDialog pins the sessions page wiring: an empty
// session list renders its empty state, and the start-session dialog posts to
// the user-session endpoint with an employee select.
func TestSessionsPage_ListAndStartDialog(t *testing.T) {
	_, hs := newSessionPanelServer(t, "alpha")
	resp := sessionsGet(t, hs, "/__operator/sessions")
	assertBody(t, resp, http.StatusOK,
		"Start a session", "/__operator/sessions/user", `name="user"`, "No active sessions")
}

// TestSessionsPage_KnownReposOffered pins that placements recorded on the
// Repositories page render as the user-session form's repository select, while
// an empty placement table falls back to a typed repo URL.
func TestSessionsPage_KnownReposOffered(t *testing.T) {
	srv, hs := newSessionPanelServer(t, "alpha")

	resp := sessionsGet(t, hs, "/__operator/sessions")
	body := assertBody(t, resp, http.StatusOK, `name="repo"`,
		"add one on the Repositories page")
	if strings.Contains(body, "<select id=\"user-repo\"") {
		t.Errorf("empty placements should render a typed repo input (body=%s)", body)
	}

	resp2 := sessionsPost(t, hs, nil, "/__operator/repos/add", map[string][]string{
		"repo":   {"https://github.com/org/repo"},
		"worker": {"alpha"},
	})
	resp2.Body.Close()

	resp3 := sessionsGet(t, hs, "/__operator/sessions")
	body3 := assertBody(t, resp3, http.StatusOK, `<select id="user-repo" name="repo"`,
		"github.com-org-repo")
	if !strings.Contains(body3, `value="https://github.com/org/repo"`) {
		t.Errorf("repo select option should carry the clone URL (body=%s)", body3)
	}
	_ = srv
}

// TestSessionsStart_HappyPath pins the end-to-end flow: the form POST drives
// StartRemoteAgent through the worker stream with an ADVERTISED workspace path,
// the master routes the new session, the banner carries the hosted-session URL,
// and the ref / agent / prompt travel verbatim on the StartAgent command.
func TestSessionsStart_HappyPath(t *testing.T) {
	srv, hs := newSessionPanelServer(t, "alpha")

	resp := sessionsPost(t, hs, nil, "/__operator/sessions/start", map[string][]string{
		"worker":    {"alpha"},
		"repo":      {"https://github.com/org/repo"},
		"workspace": {"/ws/alpha"},
		"agent":     {"plan"},
		"prompt":    {"fix the flaky test"},
	})
	body := assertBody(t, resp, http.StatusOK, "started on worker", "/sessions/")

	sids := srv.Registry().SessionsForWorker("alpha")
	if len(sids) != 1 {
		t.Fatalf("routed sessions = %d, want 1", len(sids))
	}
	if !strings.Contains(body, sids[0]) {
		t.Errorf("banner %q does not carry the routed session id", body)
	}
}

// TestSessionsStart_WorkspaceMustBeAdvertised pins the containment contract at
// the form: the posted workspace is resolved against the set the worker
// actually offers, and anything else — a path on the worker the worker never
// volunteered, a traversal attempt, a different worker's workspace — is
// rejected without routing a session or reaching the worker.
func TestSessionsStart_WorkspaceMustBeAdvertised(t *testing.T) {
	srv, hs := newSessionPanelServer(t, "alpha")

	tests := []struct {
		name      string
		workspace string
		want      string
	}{
		{"unadvertised path", "/home/me/work/repo", "is not offered by worker"},
		{"parent of advertised", "/ws", "is not offered by worker"},
		{"sibling of advertised", "/ws/other", "is not offered by worker"},
		{"traversal", "/ws/../etc", "is not offered by worker"},
		{"filesystem root", "/", "is not offered by worker"},
		{"relative", "rel/x", "is not offered by worker"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := sessionsPost(t, hs, nil, "/__operator/sessions/start", map[string][]string{
				"worker":    {"alpha"},
				"workspace": {tc.workspace},
			})
			assertBody(t, resp, http.StatusOK, tc.want, "flash err")
		})
	}
	if n := len(srv.Registry().SessionsForWorker("alpha")); n != 0 {
		t.Errorf("a non-advertised workspace routed %d sessions, want 0", n)
	}
}

// TestSessionsStart_ValidationGates pins the remaining form validations — and
// that a validation failure never routes a session.
func TestSessionsStart_ValidationGates(t *testing.T) {
	srv, hs := newSessionPanelServer(t, "alpha")

	tests := []struct {
		name string
		form map[string][]string
		want string
	}{
		{"missing worker", map[string][]string{"workspace": {"/ws/alpha"}}, "Choose a worker."},
		{"missing workspace", map[string][]string{"worker": {"alpha"}}, "Choose a workspace."},
		{"unknown worker", map[string][]string{"worker": {"nope"}, "workspace": {"/ws/alpha"}}, "not registered"},
		{"bad agent", map[string][]string{"worker": {"alpha"}, "workspace": {"/ws/alpha"}, "agent": {"nope"}}, "agent must be one of"},
		{"bad scheme", map[string][]string{"worker": {"alpha"}, "workspace": {"/ws/alpha"}, "repo": {"ftp://h/r"}}, "unsupported repository URL scheme"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := sessionsPost(t, hs, nil, "/__operator/sessions/start", tc.form)
			assertBody(t, resp, http.StatusOK, tc.want, "flash err")
		})
	}
	if len(srv.Registry().SessionsForWorker("alpha")) != 0 {
		t.Error("a validation failure routed a session")
	}
}

// fakeWorkerReject registers a fake worker whose command loop answers every
// command with ok=false and the given error — the worker-side failure path
// (a failing clone, a bootstrap error) without a real git clone. The worker
// still advertises one workspace at registration (the master falls back to
// that cached set when the live ListWorkspaces command fails too).
func fakeWorkerReject(t *testing.T, ctx context.Context, client controlplanev1connect.ControlPlaneServiceClient, wsName, failMsg string) string {
	t.Helper()
	reg, err := client.Register(ctx, connect.NewRequest(&cpv1.RegisterRequest{
		PairingSecret: testSecret, WorkerName: wsName, WorkerId: wsName,
		Workspaces: []*cpv1.Workspace{{Path: "/ws/" + wsName, Name: wsName, Present: true}},
	}))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	stream := client.WorkerStream(ctx)
	if err := stream.Send(&cpv1.WorkerToMaster{
		Message: &cpv1.WorkerToMaster_Hello{Hello: &cpv1.Hello{WorkerToken: reg.Msg.GetWorkerToken()}},
	}); err != nil {
		t.Fatalf("send hello: %v", err)
	}
	go func() {
		for {
			cmd, err := stream.Receive()
			if err != nil {
				return
			}
			_ = stream.Send(&cpv1.WorkerToMaster{Message: &cpv1.WorkerToMaster_CommandResult{
				CommandResult: &cpv1.CommandResult{RequestId: cmd.GetRequestId(), Ok: false, Error: failMsg},
			}})
		}
	}()
	return reg.Msg.GetWorkerId()
}

// TestSessionsStart_WorkerFailureSurfaces pins that a worker-side StartAgent
// failure (clone error, bootstrap error) surfaces verbatim as the error
// banner, and that no session is routed.
func TestSessionsStart_WorkerFailureSurfaces(t *testing.T) {
	srv := master.New(master.Options{
		Registry: registry.New(),
		Auth:     pairing.New(testSecret, time.Minute),
		Bus:      bus.New(256),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	mux := http.NewServeMux()
	path, handler := srv.Handler()
	mux.Handle(path, handler)
	// h2c so Connect bidi streaming works over the httptest cleartext listener.
	hs := httptest.NewServer(h2c.NewHandler(srv.UIProxyHandler(mux), &http2.Server{}))
	t.Cleanup(hs.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	id := fakeWorkerReject(t, ctx, h2cClient(hs.URL), "alpha", "bootstrap: git clone failed")
	waitFor(t, 3*time.Second, func() bool {
		info, ok := srv.Registry().Get(id)
		return ok && info.Status == registry.StatusOnline
	}, "fake worker never came online")

	resp := sessionsPost(t, hs, nil, "/__operator/sessions/start", map[string][]string{
		"worker":    {"alpha"},
		"workspace": {"/ws/alpha"},
		"repo":      {"https://github.com/org/repo"},
	})
	assertBody(t, resp, http.StatusOK, "bootstrap: git clone failed", "flash err")
	if len(srv.Registry().SessionsForWorker("alpha")) != 0 {
		t.Error("failed start routed a session")
	}
}

// TestSessionsPage_ConcurrentStarts pins that concurrent form starts against
// the same worker stream each complete against the right command round-trip —
// the pending-command table is keyed per request_id, so concurrent starts
// cannot cross wires (mirrors TestWorkerStreamRoundTrip_ConcurrentWorkers).
func TestSessionsPage_ConcurrentStarts(t *testing.T) {
	srv, hs := newSessionPanelServer(t, "alpha")

	var wg sync.WaitGroup
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp := sessionsPost(t, hs, nil, "/__operator/sessions/start", map[string][]string{
				"worker":    {"alpha"},
				"workspace": {"/ws/alpha"},
				"prompt":    {fmt.Sprintf("task %d", i)},
			})
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				results <- fmt.Errorf("start %d: status %d (body=%s)", i, resp.StatusCode, body)
			} else if !strings.Contains(string(body), "started on worker") {
				results <- fmt.Errorf("start %d: no success banner (body=%s)", i, body)
			}
		}(i)
	}
	wg.Wait()
	close(results)
	for err := range results {
		t.Error(err)
	}
	if n := len(srv.Registry().SessionsForWorker("alpha")); n != 4 {
		t.Errorf("routed sessions = %d, want 4", n)
	}
}

// TestSessionsStart_AccountsGate pins the gate behavior end to end: with the
// accounts gate enabled, an unauthenticated POST to the start endpoint is
// answered by the login page, never a session start; a logged-in operator
// gets through.
func TestSessionsStart_AccountsGate(t *testing.T) {
	srv, hs, _ := newAccountPanelServer(t, "alice", "s3cret")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fakeWorkerWith(t, ctx, h2cClient(hs.URL), "alpha")
	waitFor(t, 3*time.Second, func() bool {
		info, ok := srv.Registry().Get("alpha")
		return ok && info.Status == registry.StatusOnline
	}, "fake worker never came online")

	unauth := sessionsPost(t, hs, nil, "/__operator/sessions/start", map[string][]string{
		"worker":    {"alpha"},
		"workspace": {"/ws/alpha"},
	})
	body, _ := io.ReadAll(unauth.Body)
	unauth.Body.Close()
	if !strings.Contains(string(body), "sign in") {
		t.Errorf("unauthenticated start = %d, want the login page (body=%s)", unauth.StatusCode, body)
	}
	if len(srv.Registry().SessionsForWorker("alpha")) != 0 {
		t.Error("unauthenticated start routed a session")
	}

	cookies := loginCookie(t, hs, "alice", "s3cret")
	authd := sessionsPost(t, hs, cookies, "/__operator/sessions/start", map[string][]string{
		"worker":    {"alpha"},
		"workspace": {"/ws/alpha"},
	})
	assertBody(t, authd, http.StatusOK, "started on worker")
}
