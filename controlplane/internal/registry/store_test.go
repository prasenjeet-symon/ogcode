package registry

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "cp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// TestPersistReloadRoundTrip seeds a store-backed registry, closes it, reopens
// the SAME bbolt file, restores into a fresh registry, and asserts workers,
// the token index, and the session routing table all survived.
func TestPersistReloadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cp.db")

	seed := func() *Registry {
		st, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		r := NewWithStore(st, nil)
		now := time.Unix(1000, 0)
		r.Add("w1", "laptop", []string{"git"}, []Workspace{{Path: "/a", Name: "a", Branch: "main", Present: true}}, tok("t1"), now)
		r.Add("w2", "desktop", nil, nil, tok("t2"), now)
		r.UpdateToken("w1", tok("t1r")) // rotate w1 -> t1r, t1 must be gone from index
		r.RouteSession("s1", "w1")
		r.RouteSession("s2", "w2")
		r.RouteSession("s3", "w1")
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
		return r
	}
	seed()

	st2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	snap, err := st2.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Workers) != 2 {
		t.Fatalf("workers: got %d want 2", len(snap.Workers))
	}

	r2 := NewWithStore(st2, nil)
	boot := time.Now()
	r2.Restore(snap, boot)

	if id, ok := r2.WorkerIDForToken("t1r"); !ok || id != "w1" {
		t.Fatalf("rotated token t1r: %q,%v", id, ok)
	}
	if _, ok := r2.WorkerIDForToken("t1"); ok {
		t.Fatal("rotated-away token t1 must not resolve after reload")
	}
	if id, ok := r2.WorkerForSession("s1"); !ok || id != "w1" {
		t.Fatalf("session s1 route: %q,%v", id, ok)
	}
	if id, ok := r2.WorkerForSession("s2"); !ok || id != "w2" {
		t.Fatalf("session s2 route: %q,%v", id, ok)
	}
	if got := len(r2.SessionsForWorker("w1")); got != 2 {
		t.Fatalf("SessionsForWorker(w1): got %d want 2", got)
	}
	info, ok := r2.Get("w1")
	if !ok || info.Name != "laptop" {
		t.Fatalf("Get(w1): %+v ok=%v", info, ok)
	}
	if len(info.Workspaces) != 1 || info.Workspaces[0].Path != "/a" {
		t.Fatalf("restored workspaces: %+v", info.Workspaces)
	}
	// The restored worker's token carries its expiry, so a master restart re-
	// recognizes the worker presenting its pre-restart token within the TTL.
	tok, ok := r2.Token("w1")
	if !ok || tok.Value != "t1r" {
		t.Fatalf("restored token: %+v ok=%v", tok, ok)
	}
	if tok.ExpiresAt.IsZero() {
		t.Fatal("restored token must carry its expiry")
	}
}

// TestRestoreResetsLiveState pins the ReapStale-on-restore hazard: a restored
// worker comes back Offline with LastSeen = boot time, so running ReapStale at
// the same boot instant immediately afterwards reaps nothing and fails no
// sessions. Without the LastSeen reset the persisted (past) timestamp would
// make every restore a mass session failure.
func TestRestoreResetsLiveState(t *testing.T) {
	st := newTestStore(t)
	r := NewWithStore(st, nil)
	past := time.Now().Add(-time.Hour)
	r.Add("w1", "n", nil, nil, tok("t1"), past)
	r.RouteSession("s1", "w1")
	// The persisted record carries a past LastSeen.
	rec := workerRecord{Info: r.workers["w1"].info, Token: r.workers["w1"].token}
	_ = st.ReplaceWorker("w1", rec, "")
	// Now simulate a restart into a fresh registry from the same store.
	snap, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	r2 := NewWithStore(st, nil)
	boot := time.Now()
	r2.Restore(snap, boot)

	// Live state reset: not dead, not offline-with-past-LastSeen.
	if info, _ := r2.Get("w1"); info.Status != StatusOffline {
		t.Fatalf("restored status: %s want offline", info.Status)
	}
	dead, orphans := r2.ReapStale(time.Minute, boot.Add(time.Second))
	if len(dead) != 0 || len(orphans) != 0 {
		t.Fatalf("restore caused immediate reap: dead=%v orphans=%v", dead, orphans)
	}
	// A worker still missing well past boot IS reaped, though.
	dead, orphans = r2.ReapStale(time.Minute, boot.Add(2*time.Minute))
	if len(dead) != 1 || len(orphans) != 1 {
		t.Fatalf("late reap after restore: dead=%v orphans=%v", dead, orphans)
	}
}

// TestReAddAfterRestorePreservesRoutes pins the Add-leaves-sessions-alone
// contract across a restart + re-pair: a worker that re-registers (re-Add) after
// restore keeps its session routes and rotates its token index entry.
func TestReAddAfterRestorePreservesRoutes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cp.db")

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r := NewWithStore(st, nil)
	now := time.Now()
	r.Add("w1", "n", nil, nil, tok("t-old"), now)
	r.RouteSession("s1", "w1")
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// Restart.
	st2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	snap, err := st2.Load()
	if err != nil {
		t.Fatal(err)
	}
	r2 := NewWithStore(st2, nil)
	r2.Restore(snap, time.Now())
	// Re-pair: the worker presents the pairing secret again, master issues a new
	// token and re-Adds.
	r2.Add("w1", "n", nil, nil, tok("t-new"), time.Now())

	if id, ok := r2.WorkerForSession("s1"); !ok || id != "w1" {
		t.Fatalf("route preserved across re-pair: %q,%v", id, ok)
	}
	// The old token no longer resolves; the new one does.
	if _, ok := r2.WorkerIDForToken("t-old"); ok {
		t.Fatal("old token must be removed from the index on re-pair")
	}
	if id, ok := r2.WorkerIDForToken("t-new"); !ok || id != "w1" {
		t.Fatalf("new token index after re-pair: %q,%v", id, ok)
	}
	// Re-pair effects persisted: reload again and check.
	snap2, err := st2.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap2.Workers["w1"]; !ok {
		t.Fatal("re-paired worker missing from persisted store")
	}
	if _, ok := snap2.Tokens["t-new"]; !ok {
		t.Fatal("re-paired token missing from persisted token index")
	}
	if _, ok := snap2.Tokens["t-old"]; ok {
		t.Fatal("re-paired old token still in persisted token index")
	}
	if wid, ok := snap2.Sessions["s1"]; !ok || wid != "w1" {
		t.Fatalf("re-paired session route in store: %q,%v", wid, ok)
	}
}

// TestWriteThroughOnMutators confirms the store is written for each mutator and
// that an in-memory-only registry (New) never touches the store.
func TestWriteThroughOnMutators(t *testing.T) {
	st := newTestStore(t)
	r := NewWithStore(st, nil)
	r.Add("w1", "n", nil, nil, tok("t1"), time.Now())
	r.RouteSession("s1", "w1")
	r.UpdateToken("w1", tok("t2"))
	r.UnrouteSession("s1")

	snap, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.Tokens["t1"]; ok {
		t.Fatal("t1 should be removed by UpdateToken write-through")
	}
	if _, ok := snap.Tokens["t2"]; !ok {
		t.Fatal("t2 should be present after UpdateToken write-through")
	}
	if _, ok := snap.Sessions["s1"]; ok {
		t.Fatal("s1 should be unrouted in the store")
	}

	// In-memory-only registry does not require a store.
	mem := New()
	mem.Add("w2", "n", nil, nil, tok("m1"), time.Now())
	if id, ok := mem.WorkerIDForToken("m1"); !ok || id != "w2" {
		t.Fatalf("in-memory index: %q,%v", id, ok)
	}
}

// TestBackupTo produces a readable copy of a live DB (graceful-shutdown path).
func TestBackupTo(t *testing.T) {
	st := newTestStore(t)
	r := NewWithStore(st, nil)
	r.Add("w1", "n", nil, nil, tok("t1"), time.Now())
	r.RouteSession("s1", "w1")

	bak := filepath.Join(t.TempDir(), "cp.db.bak")
	if err := st.BackupTo(bak); err != nil {
		t.Fatal(err)
	}
	b, err := Open(bak)
	if err != nil {
		t.Fatalf("reopen backup: %v", err)
	}
	defer b.Close()
	snap, err := b.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Workers) != 1 || len(snap.Sessions) != 1 {
		t.Fatalf("backup contents: workers=%d sessions=%d", len(snap.Workers), len(snap.Sessions))
	}
}

func TestStorePath(t *testing.T) {
	st := newTestStore(t)
	if st.Path() == "" {
		t.Fatal("empty store path")
	}
}

// TestOpen_CreatesFilePrivate pins the Phase G hardening: the control-plane DB
// (workers, tokens, session routes, operator accounts) is created 0600 — the
// token index inside it is a bearer credential, so a group/world-readable file
// would leak it to every local user.
func TestOpen_CreatesFilePrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cp.db")

	if _, err := Open(path); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("control-plane DB perms = %o, want 600", got)
	}
}
