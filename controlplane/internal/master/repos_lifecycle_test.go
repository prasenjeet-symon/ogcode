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
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/master"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/registry"
)

// startMergeWorker registers a fake worker that acks everything Ok but
// answers MergeUserBranch with a canned summary — the server-side mirror of
// startStatsWorker, scoped to the lifecycle tests.
func startMergeWorker(t *testing.T, ctx context.Context, client controlplanev1connect.ControlPlaneServiceClient, workerID string, summary string) {
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
				_ = stream.Send(&cpv1.WorkerToMaster{Message: &cpv1.WorkerToMaster_CommandResult{
					CommandResult: &cpv1.CommandResult{RequestId: cmd.GetRequestId(), Ok: true, FreeBytes: 1 << 30},
				}})
			case *cpv1.MasterToWorker_MergeUserBranch:
				_ = stream.Send(&cpv1.WorkerToMaster{Message: &cpv1.WorkerToMaster_CommandResult{
					CommandResult: &cpv1.CommandResult{RequestId: cmd.GetRequestId(), Ok: true, Summary: summary},
				}})
			default:
				_ = stream.Send(&cpv1.WorkerToMaster{Message: &cpv1.WorkerToMaster_CommandResult{
					CommandResult: &cpv1.CommandResult{RequestId: cmd.GetRequestId(), Ok: true},
				}})
			}
		}
	}()
}

// lifecycleServer is the shared precondition of the lifecycle tests: an
// account-mode server, one online worker, and one plain user assigned to a
// repo. The assignment provisions through the fake worker, so the placement
// record exists before any lifecycle call. The fake worker's stream is bound
// to context.Background — it must outlive this helper so the test's own
// commands reach it (a cancelled ctx kills the stream); the httptest server's
// cleanup tears it down.
func lifecycleServer(t *testing.T) (*master.Server, *string) {
	t.Helper()
	srv, hs, _ := newAccountPanelServer(t, "root", "pw-root")
	t.Cleanup(hs.Close)
	client := h2cClient(hs.URL)
	startStatsWorker(t, context.Background(), client, "lifecycle-w", 1<<30, true)
	waitFor(t, 5*time.Second, func() bool {
		info, ok := srv.Registry().Get("lifecycle-w")
		return ok && info.Status == registry.StatusOnline
	}, "worker never came online")

	hash, err := bcrypt.GenerateFromPassword([]byte("pw-alice"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	store := srv.Registry().Store()
	if err := store.PutUser("alice", registry.UserRecord{Hash: string(hash), CreatedAt: time.Now(), Admin: boolPtr(false)}); err != nil {
		t.Fatalf("put alice: %v", err)
	}
	repoURL := "https://github.com/org/repo.git"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := srv.AssignUser(ctx, repoURL, "alice", ""); err != nil {
		t.Fatalf("AssignUser: %v", err)
	}
	return srv, &repoURL
}

func TestRemoveUserWorktree_PlacementAndCommand(t *testing.T) {
	srv, repoURL := lifecycleServer(t)
	url := *repoURL
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.RemoveUserWorktree(ctx, url, "alice"); err != nil {
		t.Fatalf("RemoveUserWorktree: %v", err)
	}
	if err := srv.RemoveUserWorktree(ctx, url, ""); err == nil || !strings.Contains(err.Error(), "user name") {
		t.Errorf("empty user: err = %v, want user name required", err)
	}
	if err := srv.RemoveUserWorktree(ctx, "https://github.com/org/unplaced.git", "alice"); err == nil || !strings.Contains(err.Error(), "no placement") {
		t.Errorf("unplaced repo: err = %v, want no-placement error", err)
	}
	if err := srv.RemoveUserWorktree(ctx, "", "alice"); err == nil || !strings.Contains(err.Error(), "repo url") {
		t.Errorf("empty repo: err = %v, want repo url required", err)
	}
}

func TestMergeUserBranch_PlacementAndSummary(t *testing.T) {
	srv, repoURL := lifecycleServer(t)
	url := *repoURL
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// The startStatsWorker fake acks everything Ok with no summary; the
	// master surfaces whatever the worker put in CommandResult.summary.
	summary, err := srv.MergeUserBranch(ctx, url, "alice", "", false)
	if err != nil {
		t.Fatalf("MergeUserBranch: %v", err)
	}
	if summary != "" {
		t.Errorf("summary = %q, want the fake worker's (empty) summary", summary)
	}
	if _, err := srv.MergeUserBranch(ctx, url, "", "", false); err == nil || !strings.Contains(err.Error(), "user name") {
		t.Errorf("empty user: err = %v, want user name required", err)
	}
	if _, err := srv.MergeUserBranch(ctx, "https://github.com/org/unplaced.git", "alice", "", false); err == nil || !strings.Contains(err.Error(), "no placement") {
		t.Errorf("unplaced repo: err = %v, want no-placement error", err)
	}
}

// TestMergeUserBranch_SummaryFromWorker pins the summary pipe: a worker that
// answers MergeUserBranch with a canned summary has it returned verbatim.
func TestMergeUserBranch_SummaryFromWorker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	srv, hs, _ := newAccountPanelServer(t, "root", "pw-root")
	defer hs.Close()
	client := h2cClient(hs.URL)
	startMergeWorker(t, ctx, client, "merge-summary-w", "merged")
	waitFor(t, 5*time.Second, func() bool {
		info, ok := srv.Registry().Get("merge-summary-w")
		return ok && info.Status == registry.StatusOnline
	}, "worker never came online")

	hash, err := bcrypt.GenerateFromPassword([]byte("pw-alice"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	store := srv.Registry().Store()
	if err := store.PutUser("alice", registry.UserRecord{Hash: string(hash), CreatedAt: time.Now(), Admin: boolPtr(false)}); err != nil {
		t.Fatalf("put alice: %v", err)
	}
	repoURL := "https://github.com/org/repo.git"
	if _, err := srv.AssignUser(ctx, repoURL, "alice", ""); err != nil {
		t.Fatalf("AssignUser: %v", err)
	}

	summary, err := srv.MergeUserBranch(ctx, repoURL, "alice", "", false)
	if err != nil {
		t.Fatalf("MergeUserBranch: %v", err)
	}
	if summary != "merged" {
		t.Errorf("summary = %q, want %q", summary, "merged")
	}
}

func TestDeprovisionRepo_ForgetsAndUnroutes(t *testing.T) {
	srv, repoURL := lifecycleServer(t)
	url := *repoURL
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	summary, err := srv.DeprovisionRepo(ctx, url, true)
	if err != nil {
		t.Fatalf("DeprovisionRepo: %v", err)
	}
	// The fake worker acks Ok with no summary; the master surfaces whatever
	// the worker sent, empty included.
	if summary != "" {
		t.Errorf("summary = %q, want the fake worker's (empty) summary", summary)
	}
	// The placement is forgotten.
	if placements := srv.RepoPlacements(); len(placements) != 0 {
		t.Errorf("RepoPlacements after deprovision = %d entries, want 0", len(placements))
	}
	// The repo is gone: a second deprovision reports the missing placement.
	if _, err := srv.DeprovisionRepo(ctx, url, true); err == nil || !strings.Contains(err.Error(), "no placement") {
		t.Errorf("second deprovision: err = %v, want no-placement error", err)
	}
	if err := srv.RemoveUserWorktree(ctx, url, "alice"); err == nil || !strings.Contains(err.Error(), "no placement") {
		t.Errorf("post-deprovision RemoveUserWorktree: err = %v, want no-placement error", err)
	}
}
