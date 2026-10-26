package timer

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

// testStore opens a throwaway project database and a store over it, with the
// database path kept in scope so the restart test can reopen it.
func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ogcode.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return Open(database), path
}

func create(t *testing.T, s *Store, sessionID, note string, fireAt time.Time) Timer {
	t.Helper()
	timer, err := s.Create(context.Background(), sessionID, note, fireAt)
	if err != nil {
		t.Fatalf("create timer in %s: %v", sessionID, err)
	}
	return timer
}

func makeTimer(t *testing.T, s *Store, sessionID, status string, due time.Duration) Timer {
	t.Helper()
	timer := create(t, s, sessionID, noteOf(status), time.Now().Add(due))
	switch status {
	case StatusDelivered, StatusCancelled:
		t.Fatalf("makeTimer only mints pending or due timers, not %s", status)
	case StatusDue:
		claimed, err := s.ClaimDue(context.Background())
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		if len(claimed) != 1 || claimed[0].ID != timer.ID {
			t.Fatalf("claim returned %v, wanted just %s", claimed, timer.ID)
		}
	}
	return timer
}

func noteOf(status string) string { return "note for " + status }

// TestClaimDueClaimsEachTimerExactlyOnce pins the claim scan: only the timers
// whose deadline has passed move pending → due and come back, and a second
// scan returns nothing — an already-due timer is never claimed again, so a
// note cannot fire twice.
func TestClaimDueClaimsEachTimerExactlyOnce(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()

	past := create(t, s, "s1", "past note", time.Now().Add(-time.Minute))
	create(t, s, "s1", "future note", time.Now().Add(time.Hour))

	claimed, err := s.ClaimDue(ctx)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != past.ID || claimed[0].Status != StatusDue {
		t.Fatalf("first claim returned %+v, wanted only the past-due %s as due", claimed, past.ID)
	}

	again, err := s.ClaimDue(ctx)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second claim returned %+v, wanted nothing", again)
	}
}

// TestTimersSurviveARestart pins durability: reopening the same database
// leaves every timer in place with the same fields, and a reminder whose
// deadline passed while the server was down is still claimable afterwards —
// that is the whole reason the store is the database and not memory.
func TestTimersSurviveARestart(t *testing.T) {
	s, path := testStore(t)
	ctx := context.Background()

	deadline := time.Now().Add(-time.Minute).Round(time.Second)
	past := create(t, s, "s1", "survives", deadline)
	future := create(t, s, "s1", "waits", time.Now().Add(time.Hour))
	if err := s.database.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	reopened, err := db.Open(path)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	t.Cleanup(func() { reopened.Close() })
	after := Open(reopened)

	claimed, err := after.ClaimDue(ctx)
	if err != nil {
		t.Fatalf("claim after restart: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != past.ID {
		t.Fatalf("claim after restart returned %+v, wanted only %s", claimed, past.ID)
	}
	if !claimed[0].FireAt.Equal(deadline) {
		t.Fatalf("fire_at after restart is %v, wanted the stored %v", claimed[0].FireAt, deadline)
	}

	list, err := after.List(ctx, "s1")
	if err != nil {
		t.Fatalf("list after restart: %v", err)
	}
	// The claimed timer is due, which is still active — it is only the
	// future one that has not fired yet.
	if len(list) != 2 || list[0].Note != past.Note || list[0].Status != StatusDue ||
		list[1].Note != future.Note || list[1].Status != StatusPending {
		t.Fatalf("list after restart returned %+v, wanted the due then the pending note", list)
	}
}

// TestCancelOnlyItsOwnSession pins the scoping of Cancel.
func TestCancelOnlyItsOwnSession(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()

	timer := create(t, s, "s1", "mine", time.Now().Add(time.Hour))
	other := create(t, s, "s2", "theirs", time.Now().Add(time.Hour))

	// A wrong session id must refuse and must not touch the row.
	if _, err := s.Cancel(ctx, "s2", timer.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancel with a wrong session returned %v, wanted ErrNotFound", err)
	}
	list, err := s.List(ctx, "s1")
	if err != nil || len(list) != 1 || list[0].Status != StatusPending {
		t.Fatalf("a refused cancel changed the row: %+v, %v", list, err)
	}

	// The owning session cancels it.
	cancelled, err := s.Cancel(ctx, "s1", timer.ID)
	if err != nil {
		t.Fatalf("cancel by the owner: %v", err)
	}
	if cancelled.Status != StatusCancelled {
		t.Fatalf("cancel left status %q, wanted cancelled", cancelled.Status)
	}

	// Cancelling again — it is terminal now — refuses, and the row stays cancelled.
	if _, err := s.Cancel(ctx, "s1", timer.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("canceling a cancelled timer returned %v, wanted ErrNotFound", err)
	}

	// An absent id refuses too.
	if _, err := s.Cancel(ctx, "s1", "tNoSuchTimer"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("canceling an absent timer returned %v, wanted ErrNotFound", err)
	}

	// A delivered timer is terminal and cannot be cancelled.
	due := makeTimer(t, s, "s1", StatusDue, -time.Minute)
	if err := s.MarkDelivered(ctx, []string{due.ID}); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}
	if _, err := s.Cancel(ctx, "s1", due.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("canceling a delivered timer returned %v, wanted ErrNotFound", err)
	}
	if _, err := s.get(ctx, due.ID); err != nil {
		t.Fatalf("delivered row vanished: %v", err)
	}
	if other.Note != "theirs" {
		t.Fatal("the other session's row was disturbed")
	}
}

// TestDeliveredIsNotReclaimed pins the end of the pipeline: a timer that is
// marked delivered drops out of every later claim scan and out of its
// session's undelivered handover, so delivery happens exactly once.
func TestDeliveredIsNotReclaimed(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()

	due := makeTimer(t, s, "s1", StatusDue, -time.Minute)
	if err := s.MarkDelivered(ctx, []string{due.ID}); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}

	claimed, err := s.ClaimDue(ctx)
	if err != nil {
		t.Fatalf("claim after delivery: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claim after delivery returned %+v, wanted nothing", claimed)
	}

	undelivered, err := s.Undelivered(ctx, "s1")
	if err != nil {
		t.Fatalf("undelivered after delivery: %v", err)
	}
	if len(undelivered) != 0 {
		t.Fatalf("undelivered after delivery returned %+v, wanted nothing", undelivered)
	}
}

// TestListIsFireAtAscendingAndActiveOnly pins List's contract: the session's
// pending and due timers in fire_at order, terminal ones left out.
func TestListIsFireAtAscendingAndActiveOnly(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()

	middle := create(t, s, "s1", "middle", time.Now().Add(time.Hour))
	create(t, s, "s1", "late", time.Now().Add(2*time.Hour))
	dropped := create(t, s, "s1", "dropped", time.Now().Add(15*time.Minute)) // will be cancelled
	early := create(t, s, "s1", "early", time.Now().Add(-time.Minute))       // will be due
	create(t, s, "s1", "delivered filler", time.Now().Add(30*time.Minute))

	due, err := s.ClaimDue(ctx)
	if err != nil || len(due) != 1 || due[0].ID != early.ID {
		t.Fatalf("claim returned %+v (%v), wanted only the early timer", due, err)
	}
	if _, err := s.Cancel(ctx, "s1", dropped.ID); err != nil {
		t.Fatalf("cancel the dropped timer: %v", err)
	}
	if _, err := s.Cancel(ctx, "s1", middle.ID); err != nil {
		t.Fatalf("cancel the middle timer: %v", err)
	}

	list, err := s.List(ctx, "s1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := []string{"early", "delivered filler", "late"}
	if len(list) != len(want) {
		t.Fatalf("list returned %+v, wanted %v", list, want)
	}
	for i, g := range list {
		if g.Note != want[i] {
			t.Fatalf("list position %d is %q, wanted %q", i, g.Note, want[i])
		}
		if g.Status != StatusDue && g.Status != StatusPending {
			t.Fatalf("list position %d is terminal (%s)", i, g.Status)
		}
	}

	// Another session's timers never leak into this one.
	others, err := s.List(ctx, "s2")
	if err != nil || len(others) != 0 {
		t.Fatalf("list of empty session returned %+v (%v), wanted nothing", others, err)
	}
}

// TestMarkDeliveredOfNothingIsANoOp pins that MarkDelivered with no ids is a
// silent no-op — a delivery pass with nothing claimed must not error.
func TestMarkDeliveredOfNothingIsANoOp(t *testing.T) {
	s, _ := testStore(t)
	if err := s.MarkDelivered(context.Background(), nil); err != nil {
		t.Fatalf("mark delivered of nothing: %v", err)
	}
	if err := s.MarkDelivered(context.Background(), []string{}); err != nil {
		t.Fatalf("mark delivered of an empty list: %v", err)
	}
}

// TestPruneDeletesOnlyOldTerminalRows pins the housekeeping contract: a
// delivered or cancelled row whose updated_at predates the cutoff is deleted,
// a recently terminal one is kept — delivery is once, so the recent row is
// still readable history — and every pending or due row is kept no matter how
// old, because a stuck due row is still owed to its session's next turn.
func TestPruneDeletesOnlyOldTerminalRows(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()

	// Stage the fates, then backdate everything but the fresh delivery past
	// any cutoff the poller would use.
	oldDelivered := makeTimer(t, s, "s1", StatusDue, -time.Minute)
	if err := s.MarkDelivered(ctx, []string{oldDelivered.ID}); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}
	newDelivered := makeTimer(t, s, "s1", StatusDue, -time.Minute)
	if err := s.MarkDelivered(ctx, []string{newDelivered.ID}); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}
	oldCancelled := create(t, s, "s1", "old cancelled", time.Now().Add(time.Hour))
	if _, err := s.Cancel(ctx, "s1", oldCancelled.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	oldDue := makeTimer(t, s, "s1", StatusDue, -time.Minute)
	oldPending := create(t, s, "s1", "old pending", time.Now().Add(time.Hour))

	// Backdate well past the 7-day cutoff the Prune call below uses: both
	// values truncate to whole Unix seconds, so an exact 7-day backdate lands
	// on the cutoff second and `updated_at < cutoff` misses it.
	stale := time.Now().Add(-8 * 24 * time.Hour).Unix()
	for _, id := range []string{oldDelivered.ID, oldCancelled.ID, oldDue.ID, oldPending.ID} {
		if _, err := s.database.ExecContext(ctx,
			`UPDATE agent_timer SET updated_at = ? WHERE id = ?`, stale, id); err != nil {
			t.Fatalf("backdate %s: %v", id, err)
		}
	}

	n, err := s.Prune(ctx, time.Now().Add(-7*24*time.Hour))
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 2 {
		t.Fatalf("prune deleted %d rows, want exactly the old delivered and cancelled pair", n)
	}
	for _, id := range []string{oldDelivered.ID, oldCancelled.ID} {
		if _, err := s.get(ctx, id); err == nil {
			t.Fatalf("pruned %s is still there", id)
		}
	}
	for _, id := range []string{newDelivered.ID, oldDue.ID, oldPending.ID} {
		if _, err := s.get(ctx, id); err != nil {
			t.Fatalf("prune touched the kept row %s: %v", id, err)
		}
	}
}
