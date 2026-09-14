package master_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/crypto/bcrypt"

	cpv1 "github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1"
	"github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1/controlplanev1connect"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/bus"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/master"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/pairing"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/registry"
)

// The slug functions are deliberate byte-for-byte mirrors of the worker's
// safeRepoName / safeUserName (ogcode module, internal/worker) — the master
// keys repo placement and derives allowlist identifiers from them. These goldens
// pin the mirror: change one side and both must change together.
func TestRepoSlugFromURL_MatchesWorkerSafeRepoName(t *testing.T) {
	cases := []struct{ url, want string }{
		{"https://github.com/org/repo.git", "github.com-org-repo"},
		{"https://github.com/org/repo", "github.com-org-repo"},
		{"git@github.com:org/repo.git", "github.com-org-repo"},
		{"ssh://user:pass@host.tld:2222/path/repo.git", "host.tld-2222-path-repo"},
		{"file:///srv/git/repo.git", "srv-git-repo"},
		{"   ", "repo"},
		{"", "repo"},
	}
	for _, c := range cases {
		if got := master.RepoSlugFromURL(c.url); got != c.want {
			t.Errorf("repoSlugFromURL(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

func TestUserWorktreeSlug_MatchesWorkerSafeUserName(t *testing.T) {
	cases := []struct{ name, want string }{
		{"alice", "alice"},
		{"Ada Lovelace", "ada-lovelace"},
		{"  dev_1  ", "dev_1"},
		{"bob--builder", "bob-builder"},
		{"!!!", "user"},
		{"", "user"},
	}
	for _, c := range cases {
		if got := master.UserWorktreeSlug(c.name); got != c.want {
			t.Errorf("userWorktreeSlug(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

// startStatsWorker registers a fake worker whose command loop answers Stats
// with the given free bytes (or never answers when stats=false, exercising the
// skip-silent-worker path) and acks everything else Ok.
func startStatsWorker(t *testing.T, ctx context.Context, client controlplanev1connect.ControlPlaneServiceClient, workerID string, freeBytes int64, stats bool) {
	t.Helper()
	reg, err := client.Register(ctx, connect.NewRequest(&cpv1.RegisterRequest{
		PairingSecret: testSecret, WorkerName: workerID, WorkerId: workerID,
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
			switch cmd.GetCommand().(type) {
			case *cpv1.MasterToWorker_Stats:
				if !stats {
					continue // never answer: the master must skip this worker
				}
				_ = stream.Send(&cpv1.WorkerToMaster{Message: &cpv1.WorkerToMaster_CommandResult{
					CommandResult: &cpv1.CommandResult{RequestId: cmd.GetRequestId(), Ok: true, FreeBytes: freeBytes},
				}})
			default:
				_ = stream.Send(&cpv1.WorkerToMaster{Message: &cpv1.WorkerToMaster_CommandResult{
					CommandResult: &cpv1.CommandResult{RequestId: cmd.GetRequestId(), Ok: true},
				}})
			}
		}
	}()
}

// AssignUser picks the online worker reporting the most free disk (skipping a
// silent one), provisions, records the placement, and scopes the account;
// a second assignment reuses the recorded holder.
func TestAssignUser_PlacementAndScoping(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	srv, hs, store := newAccountPanelServer(t, "root", "pw-root")
	defer hs.Close()
	client := h2cClient(hs.URL)

	// The seeded account is the admin (nil flag). Create the plain user the
	// assignment targets — assigning an admin is refused.
	hash, err := bcrypt.GenerateFromPassword([]byte("pw-alice"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := store.PutUser("alice", registry.UserRecord{Hash: string(hash), CreatedAt: time.Now(), Admin: boolPtr(false)}); err != nil {
		t.Fatalf("put alice: %v", err)
	}

	startStatsWorker(t, ctx, client, "stats-a", 1<<30, true)
	startStatsWorker(t, ctx, client, "stats-b", 900<<20, true)
	startStatsWorker(t, ctx, client, "stats-silent", 1<<40, false)
	for _, id := range []string{"stats-a", "stats-b", "stats-silent"} {
		waitFor(t, 5*time.Second, func() bool {
			info, ok := srv.Registry().Get(id)
			return ok && info.Status == registry.StatusOnline
		}, "worker "+id+" never came online")
	}

	// The admin account itself is refused: assignment is the scoping step for
	// plain users, and an admin has nothing to scope.
	if _, err := srv.AssignUser(ctx, "https://github.com/o/r.git", "root", ""); err == nil || !strings.Contains(err.Error(), "administrator") {
		t.Errorf("assign admin: err = %v, want administrator refusal", err)
	}

	workerID, err := srv.AssignUser(ctx, "https://github.com/org/repo.git", "alice", "develop")
	if err != nil {
		t.Fatalf("AssignUser: %v", err)
	}
	if workerID != "stats-a" {
		t.Fatalf("AssignUser placed on %q, want %q (most free disk)", workerID, "stats-a")
	}

	// A second assignment of the same repo must reuse the recorded holder
	// even though the other workers are also online.
	again, err := srv.AssignUser(ctx, "https://github.com/org/repo.git", "alice", "develop")
	if err != nil {
		t.Fatalf("second AssignUser: %v", err)
	}
	if again != "stats-a" {
		t.Fatalf("second AssignUser placed on %q, want the recorded holder %q", again, "stats-a")
	}

	// The account got scoped: repo recorded, no explicit allowlist ->
	// auto-pinned to the worktree route label, admin flag untouched.
	rec, ok, err := store.GetUser("alice")
	if err != nil || !ok {
		t.Fatalf("get user: ok=%v err=%v", ok, err)
	}
	if rec.IsAdmin() {
		t.Error("assignment must not flip the account to admin")
	}
	if repos := rec.AllRepos(); len(repos) != 1 || repos[0] != "https://github.com/org/repo.git" {
		t.Errorf("rec.AllRepos() = %v, want [the assigned URL]", repos)
	}
	if rec.BaseBranch != "develop" {
		t.Errorf("rec.BaseBranch = %q, want the assigned base branch %q", rec.BaseBranch, "develop")
	}
	if len(rec.Workspaces) != 1 || rec.Workspaces[0] != "alice" {
		t.Errorf("rec.Workspaces = %v, want [alice] (the worktree route label)", rec.Workspaces)
	}
}

// Assigning an administrator is refused outright: assignment is the scoping
// step for plain users, and an administrator has nothing to scope.
func TestAssignUser_AdminRefused(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv, hs, _ := newAccountPanelServer(t, "root", "pw-root")
	defer hs.Close()
	client := h2cClient(hs.URL)

	startStatsWorker(t, ctx, client, "w", 1<<30, true)
	waitFor(t, 5*time.Second, func() bool {
		info, ok := srv.Registry().Get("w")
		return ok && info.Status == registry.StatusOnline
	}, "worker never came online")

	if _, err := srv.AssignUser(ctx, "https://github.com/o/r.git", "root", ""); err == nil || !strings.Contains(err.Error(), "administrator") {
		t.Fatalf("err = %v, want administrator refusal", err)
	}
}

func TestAssignUser_Errors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv, hs, _ := newAccountPanelServer(t, "root", "pw-root")
	defer hs.Close()

	if _, err := srv.AssignUser(ctx, "", "alice", ""); err == nil || !strings.Contains(err.Error(), "repo url") {
		t.Errorf("empty repo: err = %v, want repo url required", err)
	}
	if _, err := srv.AssignUser(ctx, "https://github.com/o/r", "", ""); err == nil || !strings.Contains(err.Error(), "user name") {
		t.Errorf("empty user: err = %v, want user name required", err)
	}
	if _, err := srv.AssignUser(ctx, "https://github.com/o/r", "ghost", ""); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("unknown user: err = %v, want does-not-exist error", err)
	}
}

func TestAssignUser_RequiresAccountsMode(t *testing.T) {
	// A server with no gate (open proxy) cannot scope accounts — refuse.
	srv := master.New(master.Options{
		Registry: registry.New(),
		Auth:     pairing.New(testSecret, time.Minute),
		Bus:      bus.New(256),
	})
	if _, err := srv.AssignUser(context.Background(), "https://github.com/o/r", "alice", ""); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("err = %v, want accounts-disabled error", err)
	}
}

func TestStartUserSession_RequiresPlacement(t *testing.T) {
	srv, hs, _ := newAccountPanelServer(t, "root", "pw-root")
	defer hs.Close()
	if _, err := srv.StartUserSession(context.Background(), "https://github.com/o/r", "root", master.StartSpec{}); err == nil || !strings.Contains(err.Error(), "assign the user first") {
		t.Fatalf("err = %v, want unassigned-repo error", err)
	}
}

// boolPtr is a tiny helper for building UserRecords in tests.
func boolPtr(v bool) *bool { return &v }
