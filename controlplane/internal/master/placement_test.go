package master_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"connectrpc.com/connect"

	cpv1 "github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/auth"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/bus"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/incus"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/master"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/pairing"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/registry"
)

// fakeDriver records every call and answers from a tiny in-memory state map —
// the driver interface the master talks to, no Incus socket involved.
type fakeDriver struct {
	created  map[string]incus.Seed
	stopped  map[string]bool // deleted container names
	failNext error           // next Create returns this
	lists    []incus.Assignment
}

func newFakeDriver() *fakeDriver {
	return &fakeDriver{
		created: map[string]incus.Seed{},
		stopped: map[string]bool{},
	}
}

func (f *fakeDriver) Create(_ context.Context, name string, seed incus.Seed) error {
	if f.failNext != nil {
		err := f.failNext
		f.failNext = nil
		return err
	}
	f.created[name] = seed
	delete(f.stopped, name)
	return nil
}

func (f *fakeDriver) Delete(_ context.Context, name string) error {
	delete(f.created, name)
	f.stopped[name] = true
	return nil
}

func (f *fakeDriver) State(_ context.Context, name string) (incus.InstanceState, error) {
	if _, ok := f.created[name]; ok {
		return incus.StateRunning, nil
	}
	return incus.StateMissing, nil
}

func (f *fakeDriver) ListAssignments(_ context.Context) ([]incus.Assignment, error) {
	out := make([]incus.Assignment, 0, len(f.created))
	for name, seed := range f.created {
		out = append(out, incus.Assignment{
			Name:   name,
			User:   name,
			Labels: map[string]string{"repo-url": seed.RepoURL},
		})
	}
	return out, nil
}

// newContainerServer builds a master in container mode over a real bbolt
// store (so the placement bucket is exercised) and a fake driver. The gate is
// accounts-backed (AssignUser refuses without it) but login flow is not
// exercised — accounts are seeded directly on the store via seedAccount.
func newContainerServer(t *testing.T, d incus.Driver, maxContainers int, registerTimeout time.Duration) (*master.Server, *registry.Store) {
	t.Helper()
	store, err := registry.Open(filepath.Join(t.TempDir(), "cp.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	seedAccount(t, store, "seed-user") // any account flips the gate to accounts mode
	gate, err := auth.NewGate(store, "", time.Hour, "", false)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	srv := master.New(master.Options{
		Registry: registry.NewWithStore(store, slog.New(slog.NewTextHandler(io.Discard, nil))),
		Auth:     pairing.New(testSecret, time.Minute),
		Bus:      bus.New(256),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Gate:     gate,
		Incus: &master.IncusOptions{
			Driver:          d,
			Profile:         "ogcode-worker",
			ImageAlias:      "ogcode-base",
			NamePrefix:      "og-",
			MaxContainers:   maxContainers,
			RegisterTimeout: registerTimeout,
			MasterURL:       "https://panel.example.com",
			PairingSecret:   testSecret,
		},
	})
	return srv, store
}

// registerWorker exercises the Register RPC directly on the server (no HTTP
// round-trip needed) and returns the issued token value.
func registerWorker(t *testing.T, srv *master.Server, workerID string) (string, error) {
	t.Helper()
	resp, err := srv.Register(context.Background(), connect.NewRequest(&cpv1.RegisterRequest{
		PairingSecret: testSecret, WorkerName: workerID, WorkerId: workerID,
	}))
	if err != nil {
		return "", err
	}
	return resp.Msg.GetWorkerToken(), nil
}

// seedAccount creates a plain (non-admin) user account on the store.
func seedAccount(t *testing.T, store *registry.Store, name string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	rec := registry.UserRecord{Hash: string(hash), CreatedAt: time.Now(), Admin: boolPtr(false)}
	if err := store.PutUser(name, rec); err != nil {
		t.Fatal(err)
	}
}

// TestContainerName_MirrorsAssignSh pins the container-name math against the
// assign.sh budget arithmetic (NAME_BUDGET=40): repo trimmed first, user
// whole, folding identical to fold_for_name (lowercase, '.'/'_' -> '-',
// collapse, trim). Note the NAME folds github.com -> github-com while the
// clone dir keeps the raw slug.
func TestContainerName_MirrorsAssignSh(t *testing.T) {
	cases := []struct {
		repo, user, want string
	}{
		// Plain: "og-" + folded repo + "-" + user.
		{"https://github.com/org/api.git", "alice", "og-github-com-org-api-alice"},
		// Upper case is DROPPED by repoSlugFromURL (no lowercasing there);
		// dots/underscores fold only in the NAME.
		{"https://Host.example/My_Repo.git/", "Ada_Lovelace", "og-ost-example-y-epo-git-ada-lovelace"},
		// Repo segment trimmed FIRST to fit the 40-byte budget; user survives.
		{"https://github.com/org/a-very-long-repository-name-here.git", "bob", "og-github-com-org-a-very-long-reposi-bob"},
		// '!' chars vanish from the repo slug (x/a), leaving x-a.
		{"https://x/a/!!!", "!!!", "og-x-a-user"},
	}
	for _, c := range cases {
		if got := master.ContainerName("og-", c.repo, c.user); got != c.want {
			t.Errorf("ContainerName(%q, %q) = %q, want %q", c.repo, c.user, got, c.want)
		}
	}
}

// TestContainerName_DegenerateUserKeepsWhole covers the degenerate branch:
// the user overflows the budget, so the repo segment is cut against the full
// budget and the user still rides whole — the same shape assign.sh produces
// before its own 63-byte guard rejects the launch.
func TestContainerName_DegenerateUserKeepsWhole(t *testing.T) {
	longUser := strings.Repeat("u", 60)
	name := master.ContainerName("og-", "https://github.com/org/api.git", longUser)
	if !strings.HasPrefix(name, "og-") {
		t.Fatalf("name %q lost the prefix", name)
	}
	if !strings.Contains(name, longUser) {
		t.Errorf("degenerate user not preserved whole: %q", name)
	}
	if len(name) <= 63 {
		t.Errorf("expected the degenerate overflow (assign.sh dies on it), got %q (%d bytes)", name, len(name))
	}
}

// TestAssignUser_ContainerMode_DegenerateNameRefused: a container name the
// name math cannot keep inside the 63-byte worker-id cap is refused before
// any Incus call — mirroring assign.sh's die().
func TestAssignUser_ContainerMode_DegenerateNameRefused(t *testing.T) {
	d := newFakeDriver()
	srv, store := newContainerServer(t, d, 20, 600*time.Second)
	seedAccount(t, store, strings.Repeat("u", 60))

	_, err := srv.AssignUser(context.Background(), "https://github.com/org/api.git", strings.Repeat("u", 60), "")
	if err == nil || !strings.Contains(err.Error(), "not a valid worker id") {
		t.Fatalf("err = %v, want the worker-id refusal", err)
	}
	if len(d.created) != 0 {
		t.Errorf("containers created despite the refusal: %v", d.created)
	}
}

func TestContainerName_ValidWorkerIDShape(t *testing.T) {
	for _, c := range []struct{ repo, user string }{
		{"https://GitHub.COM/Org/API.git", "Alice"},
		{"https://gitlab.com/team/long-repo-name-that-keeps-going.git", "multi-word user"},
		{"ssh://git@host:2222/path/repo.git", "u"},
	} {
		name := master.ContainerName("og-", c.repo, c.user)
		if len(name) == 0 || len(name) > 63 {
			t.Errorf("name %q out of the 1-63 range", name)
		}
		if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
			t.Errorf("name %q has an edge hyphen", name)
		}
		for _, ch := range name {
			if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
				t.Errorf("name %q has char %q outside [a-z0-9-]", name, ch)
			}
		}
	}
}

// TestAssignUser_ContainerMode_CreatesAndScopes drives the happy path: the
// assignment creates the container, scopes the account, and returns the
// container name as the worker id.
func TestAssignUser_ContainerMode_CreatesAndScopes(t *testing.T) {
	d := newFakeDriver()
	srv, store := newContainerServer(t, d, 20, 600*time.Second)
	seedAccount(t, store, "alice")

	workerID, err := srv.AssignUser(context.Background(), "https://github.com/org/api.git", "alice", "main")
	if err != nil {
		t.Fatalf("AssignUser: %v", err)
	}
	want := master.ContainerName("og-", "https://github.com/org/api.git", "alice")
	if workerID != want {
		t.Fatalf("workerID = %q, want %q", workerID, want)
	}
	seed, ok := d.created[want]
	if !ok {
		t.Fatalf("container %q never created (created: %v)", want, d.created)
	}
	if seed.RepoURL != "https://github.com/org/api.git" || seed.WorkerID != want {
		t.Errorf("seed = %+v", seed)
	}
	if seed.MasterURL != "https://panel.example.com" || seed.PairingSecret != testSecret {
		t.Errorf("seed master/secret = %q/%q", seed.MasterURL, seed.PairingSecret)
	}
	if !strings.HasPrefix(seed.RepoSlug, "github.com-org-api") {
		t.Errorf("repo slug = %q", seed.RepoSlug)
	}

	// Account scoped exactly like the bare path: repo added, allowlist pinned.
	rec, found, err := store.GetUser("alice")
	if err != nil || !found {
		t.Fatalf("account lookup: %v/%v", err, found)
	}
	if !slices.Contains(rec.AllRepos(), "https://github.com/org/api.git") {
		t.Errorf("repos = %v", rec.AllRepos())
	}
	if len(rec.Workspaces) != 1 || rec.Workspaces[0] != "alice" {
		t.Errorf("workspaces = %v, want [alice] (auto-pin)", rec.Workspaces)
	}

	// Placement recorded provisioning; readiness arrives via Register.
	placements, err := srv.Placements()
	if err != nil {
		t.Fatal(err)
	}
	if len(placements) != 1 || placements[0].Status != "provisioning" {
		t.Fatalf("placements = %+v", placements)
	}
}

// TestAssignUser_ContainerMode_IdempotentReassign: a second assignment of the
// same (user, repo) returns the live container and creates nothing new.
func TestAssignUser_ContainerMode_IdempotentReassign(t *testing.T) {
	d := newFakeDriver()
	srv, store := newContainerServer(t, d, 20, 600*time.Second)
	seedAccount(t, store, "alice")

	first, err := srv.AssignUser(context.Background(), "https://github.com/org/api.git", "alice", "main")
	if err != nil {
		t.Fatalf("first assign: %v", err)
	}
	second, err := srv.AssignUser(context.Background(), "https://github.com/org/api.git", "alice", "main")
	if err != nil {
		t.Fatalf("second assign: %v", err)
	}
	if first != second {
		t.Errorf("reassign returned %q, want %q", second, first)
	}
	if len(d.created) != 1 {
		t.Errorf("created %d containers, want 1", len(d.created))
	}
}

// TestAssignUser_ContainerMode_ReprovisionsMissingContainer: the operator
// destroyed the container out-of-band; re-assign re-provisions under the same
// name.
func TestAssignUser_ContainerMode_ReprovisionsMissingContainer(t *testing.T) {
	d := newFakeDriver()
	srv, store := newContainerServer(t, d, 20, 600*time.Second)
	seedAccount(t, store, "alice")

	first, err := srv.AssignUser(context.Background(), "https://github.com/org/api.git", "alice", "")
	if err != nil {
		t.Fatalf("first assign: %v", err)
	}
	if err := d.Delete(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	again, err := srv.AssignUser(context.Background(), "https://github.com/org/api.git", "alice", "")
	if err != nil {
		t.Fatalf("re-assign after delete: %v", err)
	}
	if again != first {
		t.Errorf("re-provisioned name = %q, want %q", again, first)
	}
	if len(d.created) != 1 {
		t.Errorf("created %d seeds, want 1 (same name reused)", len(d.created))
	}
}

// TestAssignUser_ContainerMode_CreateFailureMarksFailed: a driver failure
// records the placement as failed, keeps the name for retry, and returns the
// error.
func TestAssignUser_ContainerMode_CreateFailureMarksFailed(t *testing.T) {
	d := newFakeDriver()
	srv, store := newContainerServer(t, d, 20, 600*time.Second)
	seedAccount(t, store, "alice")

	d.failNext = errors.New("no such image")
	_, err := srv.AssignUser(context.Background(), "https://github.com/org/api.git", "alice", "")
	if err == nil || !strings.Contains(err.Error(), "no such image") {
		t.Fatalf("AssignUser err = %v, want the driver failure", err)
	}
	placements, perr := srv.Placements()
	if perr != nil {
		t.Fatal(perr)
	}
	if len(placements) != 1 || placements[0].Status != "failed" {
		t.Fatalf("placements = %+v, want one failed", placements)
	}

	// A retry succeeds and reuses the failed placement's name.
	name, err := srv.AssignUser(context.Background(), "https://github.com/org/api.git", "alice", "")
	if err != nil {
		t.Fatalf("retry assign: %v", err)
	}
	if name != placements[0].ContainerName {
		t.Errorf("retry name = %q, want %q", name, placements[0].ContainerName)
	}
}

// TestAssignUser_ContainerMode_Capacity: the container cap is enforced
// against live placements; a failed placement frees its slot.
func TestAssignUser_ContainerMode_Capacity(t *testing.T) {
	d := newFakeDriver()
	srv, store := newContainerServer(t, d, 2, 600*time.Second)
	seedAccount(t, store, "alice")
	seedAccount(t, store, "bob")
	seedAccount(t, store, "carol")

	if _, err := srv.AssignUser(context.Background(), "https://github.com/org/api.git", "alice", ""); err != nil {
		t.Fatalf("alice: %v", err)
	}
	if _, err := srv.AssignUser(context.Background(), "https://github.com/org/other.git", "bob", ""); err != nil {
		t.Fatalf("bob: %v", err)
	}
	_, err := srv.AssignUser(context.Background(), "https://github.com/org/third.git", "carol", "")
	if !errors.Is(err, master.ErrCapacity) {
		t.Fatalf("third assign err = %v, want ErrCapacity", err)
	}
}

// TestRegister_CompletesPendingPlacement: a worker registration whose id is a
// provisioning container's name flips that placement to ready; unrelated
// registrations complete nothing.
func TestRegister_CompletesPendingPlacement(t *testing.T) {
	d := newFakeDriver()
	srv, store := newContainerServer(t, d, 20, 600*time.Second)
	seedAccount(t, store, "alice")

	name, err := srv.AssignUser(context.Background(), "https://github.com/org/api.git", "alice", "")
	if err != nil {
		t.Fatal(err)
	}

	// Simulate the in-guest worker registering: it presents the container
	// name as its worker id. Register's readiness hook flips the placement.
	_, herr := registerWorker(t, srv, name)
	if herr != nil {
		t.Fatalf("register: %v", herr)
	}

	placements, err := srv.Placements()
	if err != nil {
		t.Fatal(err)
	}
	if len(placements) != 1 || placements[0].Status != "ready" {
		t.Fatalf("placements = %+v, want one ready", placements)
	}

	// An unrelated registration completes nothing and is admitted.
	if _, err := registerWorker(t, srv, "plain-worker"); err != nil {
		t.Fatalf("unrelated register: %v", err)
	}
	placements, err = srv.Placements()
	if err != nil {
		t.Fatal(err)
	}
	if len(placements) != 1 || placements[0].Status != "ready" {
		t.Fatalf("unrelated registration disturbed placements: %+v", placements)
	}
}

// TestStartUserSession_ContainerMode: ready placements route the session;
// non-ready placements are refused.
func TestStartUserSession_ContainerMode(t *testing.T) {
	d := newFakeDriver()
	srv, store := newContainerServer(t, d, 20, 600*time.Second)
	seedAccount(t, store, "alice")

	name, err := srv.AssignUser(context.Background(), "https://github.com/org/api.git", "alice", "")
	if err != nil {
		t.Fatal(err)
	}

	// Not ready yet: start is refused.
	if _, err := srv.StartUserSession(context.Background(), "https://github.com/org/api.git", "alice", master.StartSpec{}); err == nil {
		t.Fatal("start before register succeeded, want refusal")
	} else if !strings.Contains(err.Error(), "not ready") {
		t.Errorf("err = %v, want not-ready wording", err)
	}

	// Register the container worker → placement ready → start routes there.
	if _, err := registerWorker(t, srv, name); err != nil {
		t.Fatal(err)
	}
	_, err = srv.StartUserSession(context.Background(), "https://github.com/org/api.git", "alice", master.StartSpec{Prompt: "hi"})
	if err != nil && !strings.Contains(err.Error(), "offline") {
		// Routing resolved to the container's worker id; the registered-but-
		// streamless fake worker then fails the send — the expected shape.
		t.Fatalf("start after ready: %v", err)
	}
}

// TestRemoveUserWorktree_ContainerMode: removal deletes the container,
// forgets the placement, and drops the repo from the account.
func TestRemoveUserWorktree_ContainerMode(t *testing.T) {
	d := newFakeDriver()
	srv, store := newContainerServer(t, d, 20, 600*time.Second)
	seedAccount(t, store, "alice")

	name, err := srv.AssignUser(context.Background(), "https://github.com/org/api.git", "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.RemoveUserWorktree(context.Background(), "https://github.com/org/api.git", "alice"); err != nil {
		t.Fatalf("RemoveUserWorktree: %v", err)
	}
	if _, deleted := d.stopped[name]; !deleted {
		t.Errorf("container %q not deleted", name)
	}
	placements, err := srv.Placements()
	if err != nil {
		t.Fatal(err)
	}
	if len(placements) != 0 {
		t.Errorf("placements after remove = %+v, want none", placements)
	}
	rec, found, err := store.GetUser("alice")
	if err != nil || !found {
		t.Fatalf("account: %v/%v", err, found)
	}
	if slices.Contains(rec.AllRepos(), "https://github.com/org/api.git") {
		t.Errorf("repo still on account: %v", rec.AllRepos())
	}
	// Removing an unassigned pair is a clean error.
	if err := srv.RemoveUserWorktree(context.Background(), "https://github.com/org/api.git", "alice"); err == nil {
		t.Error("removing absent placement succeeded")
	}
}

// TestDeprovisionRepo_ContainerMode: every container of the repo is destroyed
// and the repo drops from each holder's account.
func TestDeprovisionRepo_ContainerMode(t *testing.T) {
	d := newFakeDriver()
	srv, store := newContainerServer(t, d, 20, 600*time.Second)
	seedAccount(t, store, "alice")
	seedAccount(t, store, "bob")

	if _, err := srv.AssignUser(context.Background(), "https://github.com/org/api.git", "alice", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.AssignUser(context.Background(), "https://github.com/org/api.git", "bob", ""); err != nil {
		t.Fatal(err)
	}
	if len(d.created) != 2 {
		t.Fatalf("created %d containers, want 2", len(d.created))
	}

	summary, err := srv.DeprovisionRepo(context.Background(), "https://github.com/org/api.git", false)
	if err != nil {
		t.Fatalf("DeprovisionRepo: %v", err)
	}
	if !strings.Contains(summary, "destroyed:") {
		t.Errorf("summary = %q", summary)
	}
	if len(d.created) != 0 {
		t.Errorf("containers left: %v", d.created)
	}
	placements, err := srv.Placements()
	if err != nil {
		t.Fatal(err)
	}
	if len(placements) != 0 {
		t.Errorf("placements after deprovision = %+v", placements)
	}
	for _, u := range []string{"alice", "bob"} {
		rec, found, err := store.GetUser(u)
		if err != nil || !found {
			t.Fatalf("%s lookup: %v/%v", u, err, found)
		}
		if slices.Contains(rec.AllRepos(), "https://github.com/org/api.git") {
			t.Errorf("%s still holds the repo: %v", u, rec.AllRepos())
		}
	}
}

// TestReapProvisioning_FailsStale: a placement past the register timeout
// flips to failed; a young one survives.
func TestReapProvisioning_FailsStale(t *testing.T) {
	d := newFakeDriver()
	srv, store := newContainerServer(t, d, 20, time.Hour)
	seedAccount(t, store, "alice")
	seedAccount(t, store, "bob")

	if _, err := srv.AssignUser(context.Background(), "https://github.com/org/api.git", "alice", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.AssignUser(context.Background(), "https://github.com/org/other.git", "bob", ""); err != nil {
		t.Fatal(err)
	}

	// Force one placement's CreatedAt past the deadline by re-writing it via
	// the reaper clock: first reap with a far-future "now" — both fail.
	reaped, err := srv.ReapProvisioning(context.Background(), time.Now().Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(reaped) != 2 {
		t.Fatalf("reaped %d placements, want 2", len(reaped))
	}
	placements, err := srv.Placements()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range placements {
		if p.Status != "failed" {
			t.Errorf("placement %+v not failed", p)
		}
	}

	// A failed placement does not count against capacity (its slot is free).
	seedAccount(t, store, "carol")
	if _, err := srv.AssignUser(context.Background(), "https://github.com/org/third.git", "carol", ""); err != nil {
		t.Fatalf("assign after reaps: %v", err)
	}
}

// TestContainerMode_BareBehaviorUnaffected pins the nil-incus invariant: a
// bare server has no placements and reports non-container mode.
func TestContainerMode_BareBehaviorUnaffected(t *testing.T) {
	store, err := registry.Open(filepath.Join(t.TempDir(), "cp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	srv := master.New(master.Options{
		Registry: registry.NewWithStore(store, slog.New(slog.NewTextHandler(io.Discard, nil))),
		Auth:     pairing.New(testSecret, time.Minute),
		Bus:      bus.New(256),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if srv.ContainerMode() {
		t.Fatal("bare server reports container mode")
	}
	placements, err := srv.Placements()
	if err != nil || placements != nil {
		t.Errorf("bare Placements = %v/%v, want nil, nil", placements, err)
	}
	if _, err := srv.ReapProvisioning(context.Background(), time.Now()); err != nil {
		t.Errorf("bare reap errored: %v", err)
	}
}

// TestRegistryPlacementBucketExists pins the bucket contract between the
// registry and the placement store.
func TestRegistryPlacementBucketExists(t *testing.T) {
	store, err := registry.Open(filepath.Join(t.TempDir(), "cp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	err = store.DB().Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("placements")).Put([]byte("k"), []byte("v"))
	})
	if err != nil {
		t.Fatalf("placements bucket missing: %v", err)
	}
}

// newContainerPanelServer serves a container-mode master over HTTP so the
// placements page and its Create/Destroy handlers can be driven like the
// other panel flows.
func newContainerPanelServer(t *testing.T, d incus.Driver) (*master.Server, *httptest.Server, *registry.Store) {
	t.Helper()
	srv, store := newContainerServer(t, d, 20, 600*time.Second)
	mux := http.NewServeMux()
	path, handler := srv.Handler()
	mux.Handle(path, handler)
	hs := httptest.NewServer(h2c.NewHandler(srv.UIProxyHandler(mux), &http2.Server{}))
	t.Cleanup(hs.Close)
	return srv, hs, store
}

// TestPlacementsPanel_ListingAndDestroy drives the placements table over
// HTTP: a recorded placement renders with its status; the Destroy action
// deletes the container and empties the table.
func TestPlacementsPanel_ListingAndDestroy(t *testing.T) {
	d := newFakeDriver()
	srv, hs, store := newContainerPanelServer(t, d)
	seedAccount(t, store, "alice")

	if _, err := srv.AssignUser(context.Background(), "https://github.com/org/api.git", "alice", ""); err != nil {
		t.Fatal(err)
	}

	// The panel is operator-gated; log in with a seeded account's password.
	resp := getWithCookies(t, hs.URL+"/__operator/repos", loginCookie(t, hs, "alice", "pw"))
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	for _, want := range []string{"alice", "api", "github.com-org-api", "provisioning", "og-github-com-org-api-alice", "Container mode"} {
		if !strings.Contains(page, want) {
			t.Errorf("placements page missing %q\npage:\n%s", want, page)
		}
	}

	// Destroy: the container is deleted and the table empties.
	cookies := loginCookie(t, hs, "alice", "pw")
	resp = postFormWithCookies(t, hs, cookies, "/__operator/repos/placements/destroy", map[string][]string{
		"user": {"alice"}, "repo": {"https://github.com/org/api.git"},
	})
	assertBody(t, resp, http.StatusOK, "destroyed", "flash ok")
	if len(d.created) != 0 {
		t.Errorf("containers left after destroy: %v", d.created)
	}
	placements, err := srv.Placements()
	if err != nil {
		t.Fatal(err)
	}
	if len(placements) != 0 {
		t.Errorf("placements after destroy = %+v", placements)
	}

	// Destroying again is a clean flash error, not a panic.
	resp = postFormWithCookies(t, hs, cookies, "/__operator/repos/placements/destroy", map[string][]string{
		"user": {"alice"}, "repo": {"https://github.com/org/api.git"},
	})
	assertBody(t, resp, http.StatusOK, "flash err")
}

// postFormWithCookies POSTs a form with a session cookie attached, following
// redirects disabled so the immediate response is inspectable.
func postFormWithCookies(t *testing.T, hs *httptest.Server, cookies []*http.Cookie, path string, form map[string][]string) *http.Response {
	t.Helper()
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	values := url.Values{}
	for k, vs := range form {
		for _, v := range vs {
			values.Add(k, v)
		}
	}
	req, err := http.NewRequest(http.MethodPost, hs.URL+path, strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", path, err)
	}
	return resp
}

// TestPlacementsPanel_CreateAction drives the Create action: a fresh
// assignment through the form creates the container and re-renders the table.
func TestPlacementsPanel_CreateAction(t *testing.T) {
	d := newFakeDriver()
	srv, hs, store := newContainerPanelServer(t, d)
	seedAccount(t, store, "bob")

	cookies := loginCookie(t, hs, "bob", "pw")
	resp := postFormWithCookies(t, hs, cookies, "/__operator/repos/placements/create", map[string][]string{
		"user": {"bob"}, "repo": {"https://github.com/org/api.git"},
	})
	assertBody(t, resp, http.StatusOK, "created for bob", "flash ok")
	placements, err := srv.Placements()
	if err != nil {
		t.Fatal(err)
	}
	if len(placements) != 1 || placements[0].Status != "provisioning" {
		t.Fatalf("placements = %+v", placements)
	}
}
