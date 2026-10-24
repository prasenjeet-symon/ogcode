package board

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return Open(database, t.TempDir())
}

func join(t *testing.T, s *Store, handle, sessionID string) Handle {
	t.Helper()
	h, err := s.Register(context.Background(), Handle{Handle: handle, SessionID: sessionID, Role: "build", Status: StatusRunning})
	if err != nil {
		t.Fatalf("register %s: %v", handle, err)
	}
	return h
}

func spawn(t *testing.T, s *Store, handle, sessionID, parent string) Handle {
	t.Helper()
	h, err := s.Register(context.Background(), Handle{
		Handle:    handle,
		SessionID: sessionID,
		Role:      "build",
		Kind:      KindTeammate,
		SpawnedBy: parent,
		Status:    StatusRunning,
	})
	if err != nil {
		t.Fatalf("spawn %s: %v", handle, err)
	}
	return h
}

// backdate pushes a row's stamps into the past, so a test can produce a status
// change the spawner has not been told about yet without waiting out the
// one-second resolution of the stamps.
func backdate(t *testing.T, s *Store, handle string, back time.Duration) {
	t.Helper()
	secs := int64(back.Seconds())
	if _, err := s.database.ExecContext(context.Background(),
		`UPDATE board_agent SET updated_at = updated_at - ?, notified_at = max(0, notified_at - ?)
		 WHERE directory = ? AND handle = ?`, secs, secs, s.dir, handle); err != nil {
		t.Fatalf("backdate %s: %v", handle, err)
	}
}

func TestRegisterRefusesReservedHandles(t *testing.T) {
	s := testStore(t)
	for _, h := range []string{"user", "system", "board"} {
		if _, err := s.Register(context.Background(), Handle{Handle: h}); err == nil {
			t.Errorf("register %q: want error, got nil", h)
		}
	}
	if _, err := s.Register(context.Background(), Handle{Handle: "Not A Handle"}); err == nil {
		t.Error("register with uppercase and spaces: want error, got nil")
	}
}

func TestRegisterKeepsIdentityOnRejoin(t *testing.T) {
	s := testStore(t)
	before := join(t, s, "build-1", "sess-1")
	join(t, s, "dev-2", "sess-2")
	backdate(t, s, "build-1", 10*time.Second)

	after := join(t, s, "build-1", "sess-1")
	if after.ID != before.ID {
		t.Errorf("rejoin id = %q, want %q (the row must survive a rejoin, not be recreated)", after.ID, before.ID)
	}
	if !after.CreatedAt.Equal(before.CreatedAt) {
		t.Errorf("rejoin createdAt = %s, want %s", after.CreatedAt, before.CreatedAt)
	}
	if after.Status != StatusRunning {
		t.Errorf("rejoin status = %q, want running (a rejoin refreshes what the agent is doing)", after.Status)
	}
}

func TestRegisterKeepsTheReportStamp(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	join(t, s, "build-1", "sess-1")
	spawn(t, s, "dev-2", "sess-2", "build-1")

	if err := s.SetStatus(ctx, "dev-2", StatusDone); err != nil {
		t.Fatalf("set status: %v", err)
	}
	if err := s.MarkReported(ctx, []string{"dev-2"}); err != nil {
		t.Fatalf("mark reported: %v", err)
	}
	spawn(t, s, "dev-2", "sess-2", "build-1")

	h, err := s.Get(ctx, "dev-2")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if after := join(t, s, "build-1", "sess-1"); after.Status != StatusRunning {
		t.Errorf("parent status = %q, want running", after.Status)
	}
	if h.SpawnedBy != "build-1" {
		t.Errorf("spawnedBy = %q, want build-1 (the tree edge survives a rejoin)", h.SpawnedBy)
	}
	if err := s.SetStatus(ctx, "dev-2", StatusDone); err != nil {
		t.Fatalf("set status again: %v", err)
	}
	again, err := s.Get(ctx, "dev-2")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if again.Unreported() {
		t.Error("a rejoin forgot that the completion was already reported, so the spawner would hear it twice")
	}
}

func TestBySessionFindsTheHandle(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	join(t, s, "build-1", "sess-1")

	h, err := s.BySession(ctx, "sess-1")
	if err != nil {
		t.Fatalf("by session: %v", err)
	}
	if h.Handle != "build-1" {
		t.Errorf("handle = %q, want build-1", h.Handle)
	}
	missing, err := s.BySession(ctx, "sess-nope")
	if err != nil {
		t.Fatalf("by session: %v", err)
	}
	if missing.Handle != "" {
		t.Errorf("unknown session resolved to %q, want the zero handle", missing.Handle)
	}
}

func TestGetOfAnUnknownHandleIsZero(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	join(t, s, "build-1", "sess-1")

	h, err := s.Get(ctx, "nobody")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if h.Handle != "" {
		t.Errorf("unknown handle = %q, want the zero Handle", h.Handle)
	}
	if h.Terminal() || h.Unreported() {
		t.Error("the zero Handle must be neither terminal nor unreported")
	}
}

func TestRosterIsJoinOrder(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	join(t, s, "build-1", "sess-1")
	spawn(t, s, "dev-2", "sess-2", "build-1")
	spawn(t, s, "dev-3", "sess-3", "build-1")

	roster, err := s.Roster(ctx)
	if err != nil {
		t.Fatalf("roster: %v", err)
	}
	if len(roster) != 3 {
		t.Fatalf("roster length = %d, want 3", len(roster))
	}
	want := []string{"build-1", "dev-2", "dev-3"}
	for i, h := range roster {
		if h.Handle != want[i] {
			t.Errorf("roster[%d] = %q, want %q", i, h.Handle, want[i])
		}
	}
}

func TestChildrenAreTheTreeEdge(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	join(t, s, "build-1", "sess-1")
	spawn(t, s, "dev-2", "sess-2", "build-1")
	spawn(t, s, "dev-3", "sess-3", "build-1")
	spawn(t, s, "dev-4", "sess-4", "dev-2")

	children, err := s.Children(ctx, "build-1")
	if err != nil {
		t.Fatalf("children: %v", err)
	}
	if len(children) != 2 || children[0].Handle != "dev-2" || children[1].Handle != "dev-3" {
		t.Fatalf("children of build-1 = %v, want dev-2 and dev-3", handlesOf(children))
	}
	grandchildren, err := s.Children(ctx, "dev-2")
	if err != nil {
		t.Fatalf("children: %v", err)
	}
	if len(grandchildren) != 1 || grandchildren[0].Handle != "dev-4" {
		t.Fatalf("children of dev-2 = %v, want dev-4", handlesOf(grandchildren))
	}
	none, err := s.Children(ctx, "")
	if err != nil {
		t.Fatalf("children of nobody: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("children of an empty parent = %v, want none", handlesOf(none))
	}
}

func TestUnreportedUntilMarkedReported(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	join(t, s, "build-1", "sess-1")
	spawn(t, s, "dev-2", "sess-2", "build-1")

	running, err := s.Get(ctx, "dev-2")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if running.Unreported() {
		t.Error("a running teammate is not news to its spawner")
	}

	if err := s.SetStatus(ctx, "dev-2", StatusDone); err != nil {
		t.Fatalf("set status: %v", err)
	}
	backdate(t, s, "dev-2", 5*time.Second)
	if err := s.SetStatus(ctx, "dev-2", StatusDone); err != nil {
		t.Fatalf("set status: %v", err)
	}
	done, err := s.Get(ctx, "dev-2")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !done.Unreported() {
		t.Fatal("a completion the spawner has not heard is not marked unreported")
	}

	if err := s.MarkReported(ctx, []string{"dev-2"}); err != nil {
		t.Fatalf("mark reported: %v", err)
	}
	reported, err := s.Get(ctx, "dev-2")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if reported.Unreported() {
		t.Error("a reported completion is still marked unreported, so it would be pushed twice")
	}
}

func TestWaitStatusReturnsWhenATeammateFinishes(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	join(t, s, "build-1", "sess-1")
	spawn(t, s, "dev-2", "sess-2", "build-1")

	go func() {
		time.Sleep(100 * time.Millisecond)
		if err := s.SetStatus(context.Background(), "dev-2", StatusDone); err != nil {
			t.Errorf("set status: %v", err)
		}
	}()

	found, err := s.WaitStatus(ctx, []string{"dev-2"}, 5*time.Second)
	if err != nil {
		t.Fatalf("wait status: %v", err)
	}
	if len(found) != 1 || found[0].Status != StatusDone {
		t.Fatalf("wait returned %v, want dev-2 done", handlesOf(found))
	}
}

func TestWaitStatusTimesOutWithoutError(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	join(t, s, "build-1", "sess-1")
	spawn(t, s, "dev-2", "sess-2", "build-1")

	found, err := s.WaitStatus(ctx, []string{"dev-2"}, 150*time.Millisecond)
	if err != nil {
		t.Fatalf("wait status: %v", err)
	}
	if len(found) != 1 || found[0].Status != StatusRunning {
		t.Fatalf("wait returned %v, want dev-2 still running", handlesOf(found))
	}
}

func TestWaitStatusIgnoresAHandleThatLeft(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	join(t, s, "build-1", "sess-1")

	found, err := s.WaitStatus(ctx, []string{"ghost"}, 5*time.Second)
	if err != nil {
		t.Fatalf("wait status: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("wait returned %v for a handle nobody holds, want none", handlesOf(found))
	}
}

func TestReconcileMarksOrphansStale(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	join(t, s, "build-1", "sess-1")
	join(t, s, "dev-2", "sess-2")
	if err := s.SetStatus(ctx, "dev-2", StatusDone); err != nil {
		t.Fatalf("set status: %v", err)
	}

	if err := s.Reconcile(ctx, map[string]bool{"sess-1": true}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	alive, _ := s.Get(ctx, "build-1")
	if alive.Status != StatusRunning {
		t.Errorf("live handle status = %q, want running", alive.Status)
	}
	gone, _ := s.Get(ctx, "dev-2")
	if gone.Status != StatusDone {
		t.Errorf("finished handle status = %q, want done", gone.Status)
	}

	if err := s.Reconcile(ctx, map[string]bool{}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	orphan, _ := s.Get(ctx, "build-1")
	if orphan.Status != StatusStale {
		t.Errorf("orphan status = %q, want stale", orphan.Status)
	}
}

// TestRegisterFreshClaimsAnUnusedHandle pins the insert-once registration: a
// name nobody holds joins the board, exactly as Register would write it.
func TestRegisterFreshClaimsAnUnusedHandle(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	h, err := s.RegisterFresh(ctx, Handle{
		Handle: "dev-1", SessionID: "sess-1", Role: "build",
		Kind: KindTeammate, SpawnedBy: "build-0", Status: StatusRunning,
	})
	if err != nil {
		t.Fatalf("register fresh: %v", err)
	}
	if h.Handle != "dev-1" || h.SessionID != "sess-1" || h.Role != "build" || h.Status != StatusRunning || h.SpawnedBy != "build-0" {
		t.Fatalf("fresh row = %+v, want the row that was registered", h)
	}
}

// TestRegisterFreshRefusesATakenHandle pins the guard Register has never had:
// a fresh registration of a name somebody holds is refused, and the row the
// other session is running under is left exactly as it was — its eventual
// report must not land on a newcomer's row.
func TestRegisterFreshRefusesATakenHandle(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	first := spawn(t, s, "dev-2", "sess-2", "build-1")

	_, err := s.RegisterFresh(ctx, Handle{
		Handle: "dev-2", SessionID: "sess-9", Role: "build",
		Kind: KindTeammate, SpawnedBy: "build-1", Status: StatusRunning,
	})
	if !errors.Is(err, ErrHandleTaken) {
		t.Fatalf("register fresh over a taken handle: %v, want ErrHandleTaken", err)
	}
	cur, err := s.Get(ctx, "dev-2")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cur.ID != first.ID || cur.SessionID != first.SessionID || cur.Status != first.Status {
		t.Fatalf("the taken row moved: %+v, want it untouched at session %q", cur, first.SessionID)
	}
}

// TestRegisterFreshRefusesReservedHandles pins that the fresh registration
// validates the same way Register does.
func TestRegisterFreshRefusesReservedHandles(t *testing.T) {
	s := testStore(t)
	if _, err := s.RegisterFresh(context.Background(), Handle{Handle: "user"}); err == nil {
		t.Error("register fresh of a reserved handle: want error, got nil")
	}
}

// TestRegisterStillUpsertsARejoin pins that Register's semantics are
// unchanged: it keeps the existing upsert a rejoin relies on — same row, and
// the row now names the new session.
func TestRegisterStillUpsertsARejoin(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	before := join(t, s, "build-1", "sess-1")
	after := join(t, s, "build-1", "sess-9")
	if after.ID != before.ID {
		t.Errorf("rejoin id = %q, want %q (an upsert keeps the row, it does not replace it)", after.ID, before.ID)
	}
	got, err := s.Get(ctx, "build-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SessionID != "sess-9" {
		t.Errorf("rejoined session id = %q, want sess-9 (Register still overwrites the row on conflict)", got.SessionID)
	}
}

func handlesOf(handles []Handle) []string {
	out := make([]string, 0, len(handles))
	for _, h := range handles {
		out = append(out, h.Handle+":"+h.Status)
	}
	return out
}
