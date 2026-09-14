package master_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	cpv1 "github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/registry"
)

// postForm posts form values with the given cookies (no redirect following).
func postForm(t *testing.T, rawURL string, cookies []*http.Cookie, form url.Values) *http.Response {
	t.Helper()
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	req, err := http.NewRequest(http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	return resp
}

func ctxBackground() context.Context { return context.Background() }

// The console's assign → user-session flow end-to-end over HTTP: the assign
// form provisions via the fake worker's answers and scopes the account; the
// user-session form then starts a session carrying (repo_url, user_name) down
// the stream, routed to the placement's worker, recorded under alice's
// account, and alice's cookie opens its monitor while a foreign scoped user's
// does not.
func TestConsoleAssignThenUserSession_EndToEnd(t *testing.T) {
	srv, hs, store, _, aliceCookie, bobCookie := scopedHarness(t)
	client := h2cClient(hs.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// A worker answering every command Ok — the only online worker, so
	// pickWorker takes it even though its Stats answer carries no free bytes.
	reg, err := client.Register(ctx, connect.NewRequest(&cpv1.RegisterRequest{
		PairingSecret: testSecret, WorkerName: "w1", WorkerId: "w1",
	}))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	stream := client.WorkerStream(ctx)
	if err := stream.Send(&cpv1.WorkerToMaster{
		Message: &cpv1.WorkerToMaster_Hello{Hello: &cpv1.Hello{WorkerToken: reg.Msg.GetWorkerToken()}},
	}); err != nil {
		t.Fatalf("hello: %v", err)
	}
	var startAgent *cpv1.StartAgent
	gotStats := make(chan struct{}, 1)
	go func() {
		for {
			cmd, err := stream.Receive()
			if err != nil {
				return
			}
			switch c := cmd.GetCommand().(type) {
			case *cpv1.MasterToWorker_StartAgent:
				startAgent = c.StartAgent
				_ = stream.Send(&cpv1.WorkerToMaster{Message: &cpv1.WorkerToMaster_CommandResult{
					CommandResult: &cpv1.CommandResult{RequestId: cmd.GetRequestId(), Ok: true},
				}})
			case *cpv1.MasterToWorker_Stats:
				select {
				case gotStats <- struct{}{}:
				default:
				}
				_ = stream.Send(&cpv1.WorkerToMaster{Message: &cpv1.WorkerToMaster_CommandResult{
					CommandResult: &cpv1.CommandResult{RequestId: cmd.GetRequestId(), Ok: true},
				}})
			default:
				_ = stream.Send(&cpv1.WorkerToMaster{Message: &cpv1.WorkerToMaster_CommandResult{
					CommandResult: &cpv1.CommandResult{RequestId: cmd.GetRequestId(), Ok: true},
				}})
			}
		}
	}()
	waitFor(t, 5*time.Second, func() bool {
		info, ok := srv.Registry().Get("w1")
		return ok && info.Status == registry.StatusOnline
	}, "worker never came online")

	// 1. Assign alice via the console form.
	resp := postForm(t, hs.URL+"/__operator/users/assign", aliceCookie, url.Values{
		"repo": {"https://github.com/org/repo.git"},
		"user": {"alice"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("assign status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}
	rec, ok, err := store.GetUser("alice")
	if err != nil || !ok {
		t.Fatalf("get alice: ok=%v err=%v", ok, err)
	}
	// Additive assignment: alice keeps her seeded repo and gains the newly
	// assigned one (this is the multi-repo behavior).
	assigned := false
	for _, u := range rec.AllRepos() {
		if u == "https://github.com/org/repo.git" {
			assigned = true
		}
	}
	if !assigned {
		t.Errorf("alice.AllRepos() = %v, want it to include the assigned URL", rec.AllRepos())
	}
	if len(rec.Workspaces) != 1 || rec.Workspaces[0] != "alice" {
		t.Errorf("alice.Workspaces = %v, want [alice]", rec.Workspaces)
	}

	// 2. Start alice's session via the console's user-session form.
	resp2 := postForm(t, hs.URL+"/__operator/sessions/user", aliceCookie, url.Values{
		"repo":   {"https://github.com/org/repo.git"},
		"user":   {"alice"},
		"agent":  {"build"},
		"prompt": {"fix the build"},
	})
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("user session status = %d, want 200 (body=%s)", resp2.StatusCode, body)
	}

	// The worker received logical targeting, not a workspace path.
	if startAgent == nil {
		t.Fatal("worker never received StartAgent")
	}
	if startAgent.GetRepoUrl() != "https://github.com/org/repo.git" || startAgent.GetUserName() != "alice" {
		t.Errorf("StartAgent carried repo=%q user=%q, want the (repo, user) pair",
			startAgent.GetRepoUrl(), startAgent.GetUserName())
	}
	if startAgent.GetWorkspace() != "" {
		t.Errorf("StartAgent.Workspace = %q, want empty (logical targeting)", startAgent.GetWorkspace())
	}
	sid := startAgent.GetSessionId()
	if got, routed := srv.Registry().WorkerForSession(sid); !routed || got != "w1" {
		t.Errorf("session routed to %q,%v, want w1", got, routed)
	}
	if user, ok := srv.SessionUser(sid); !ok || user != "alice" {
		t.Errorf("SessionUser = %q,%v, want alice", user, ok)
	}

	// 3. Monitor scoping: alice (the starter) renders it; bob gets 404.
	if resp3 := getWithCookies(t, hs.URL+"/sessions/"+sid, aliceCookie); resp3.StatusCode != http.StatusOK {
		resp3.Body.Close()
		t.Fatalf("starter monitor status = %d, want 200", resp3.StatusCode)
	}
	if resp4 := getWithCookies(t, hs.URL+"/sessions/"+sid, bobCookie); resp4.StatusCode != http.StatusNotFound {
		resp4.Body.Close()
		t.Fatalf("foreign monitor status = %d, want 404", resp4.StatusCode)
	}
}
