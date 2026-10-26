package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/agent"
	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/timer"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// reminderTestServer is a Server with a live timer store, a session store and a
// runner's report tracker on temp DBs — the same rig the board tests build,
// with the timer store opened where Serve opens it.
func reminderTestServer(t *testing.T) *Server {
	t.Helper()
	srv := newTestServer(t)
	srv.bus = bus.New(64)
	srv.timers = timer.Open(srv.db)
	srv.store = session.NewStore(srv.db)
	srv.loopRunner = &agent.LoopRunner{}
	return srv
}

// createReminder stages one deadline that has already passed AND has been
// claimed by the claim scan, exactly as an idle session's row sits in the
// store between a deadline and its delivery — and as deliverReminder and
// catchUpReminders meet it in production. Undelivered hands over claimed rows
// only, which is why the claim is part of the staging. For a row the poller
// itself must claim, createPendingReminder.
func createReminder(t *testing.T, srv *Server, sessionID, note string, fireAt time.Time) timer.Timer {
	t.Helper()
	t1 := createPendingReminder(t, srv, sessionID, note, fireAt)
	ctx, cancel := context.WithTimeout(context.Background(), boardOpTimeout)
	defer cancel()
	if _, err := srv.timers.ClaimDue(ctx); err != nil {
		t.Fatalf("claim staged reminder: %v", err)
	}
	return t1
}

// createPendingReminder stores one deadline and leaves it pending — not yet
// claimed by a claim scan, so a running ticker's own ClaimDue is the claimant,
// as it is in production.
func createPendingReminder(t *testing.T, srv *Server, sessionID, note string, fireAt time.Time) timer.Timer {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), boardOpTimeout)
	defer cancel()
	t1, err := srv.timers.Create(ctx, sessionID, note, fireAt)
	if err != nil {
		t.Fatalf("create reminder: %v", err)
	}
	return t1
}

// undeliveredCount reads back how many of the session's reminders are still
// owed to it.
func undeliveredCount(t *testing.T, srv *Server, sessionID string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), boardOpTimeout)
	defer cancel()
	due, err := srv.timers.Undelivered(ctx, sessionID)
	if err != nil {
		t.Fatalf("undelivered: %v", err)
	}
	return len(due)
}

// TestDeliverReminderPushesIntoARunningTurnAndMarksAtDispatch pins the live
// path: the reminder is pushed as a "reminder"-caused notice, the push carries
// the note and the fire time, and the row stays due until the step that drains
// the notice dispatches a request carrying its text — whereupon the tracked
// mark stamps the store.
func TestDeliverReminderPushesIntoARunningTurnAndMarksAtDispatch(t *testing.T) {
	srv := reminderTestServer(t)
	id := session.SessionID("s1")
	createBoardSession(t, srv, id)
	lc := registerBoardLoop(t, srv, id)

	t1 := createReminder(t, srv, "s1", "check the failing parser test", time.Now().Add(-time.Minute))
	srv.deliverReminder(context.Background(), t1)

	notice := waitNotice(t, lc)
	if notice.Cause != agent.ReminderCause {
		t.Errorf("notice cause = %q, want %q", notice.Cause, agent.ReminderCause)
	}
	if !strings.Contains(notice.Text, "check the failing parser test") {
		t.Errorf("pushed notice does not carry the note: %q", notice.Text)
	}
	if !strings.Contains(notice.Text, "Scheduled reminder came due") {
		t.Errorf("pushed notice does not say it is a scheduled reminder: %q", notice.Text)
	}

	// The push is tracked, not stamped: the mark belongs to a dispatch that
	// carried the text, played by hand here.
	if got := undeliveredCount(t, srv, "s1"); got != 1 {
		t.Fatalf("the push marked the row delivered before any request carried it: %d undelivered, want 1", got)
	}
	srv.loopRunner.BoardNoticesDelivered(lc, id, []agent.Notice{{Text: notice.Text}})
	if got := undeliveredCount(t, srv, "s1"); got != 0 {
		t.Errorf("a dispatched notice left its row due: %d undelivered, want 0", got)
	}
}

// TestIdleReminderWaitsForCatchUp pins the idle path: a deadline passing while
// the session has no loop leaves the row due, and the session's next turn start
// hands it over — pushed once, stamped only at the dispatch, quiet afterwards.
func TestIdleReminderWaitsForCatchUp(t *testing.T) {
	srv := reminderTestServer(t)
	createBoardSession(t, srv, session.SessionID("s1"))

	t1 := createReminder(t, srv, "s1", "report the build status", time.Now().Add(-time.Minute))
	// No loop is registered, which is the point: the deadline passes between
	// turns and nothing wakes the session.
	srv.deliverReminder(context.Background(), t1)
	if got := undeliveredCount(t, srv, "s1"); got != 1 {
		t.Fatalf("an idle session's reminder was marked delivered: %d undelivered, want 1", got)
	}

	lc := registerBoardLoop(t, srv, session.SessionID("s1"))
	if err := srv.catchUpReminders(context.Background(), session.SessionID("s1"), srv.dir); err != nil {
		t.Fatalf("catch up: %v", err)
	}
	notice := waitNotice(t, lc)
	if notice.Cause != agent.ReminderCause {
		t.Errorf("catch-up notice cause = %q, want %q", notice.Cause, agent.ReminderCause)
	}
	if !strings.Contains(notice.Text, "report the build status") {
		t.Errorf("catch-up notice does not carry the note: %q", notice.Text)
	}
	if got := undeliveredCount(t, srv, "s1"); got != 1 {
		t.Fatalf("the catch-up push stamped the row before any request carried it: %d undelivered, want 1", got)
	}

	// The turn that received it dispatches it — played by hand, as RunLoop does.
	srv.loopRunner.BoardNoticesDelivered(lc, session.SessionID("s1"), []agent.Notice{{Text: notice.Text}})
	if got := undeliveredCount(t, srv, "s1"); got != 0 {
		t.Errorf("a dispatched reminder was left due: %d undelivered, want 0", got)
	}

	// Once delivered, it is once: the next turn start is quiet.
	if err := srv.catchUpReminders(context.Background(), session.SessionID("s1"), srv.dir); err != nil {
		t.Fatalf("catch up again: %v", err)
	}
	if lc.HasPendingNotice() {
		t.Error("a delivered reminder was handed to another turn")
	}
}

// TestCatchUpRemindersGuards pins the guards: a directory that is not this
// project's is a no-op even with a live loop (the catch-up belongs to the
// project's own sessions), a missing loop is a no-op, and — the fallback pin —
// a runner with no tracker marks the rows itself rather than leaving them due
// forever.
func TestCatchUpRemindersGuards(t *testing.T) {
	srv := reminderTestServer(t)
	createBoardSession(t, srv, session.SessionID("s1"))
	// Left due by a deadline that passed while nothing watched: the guards
	// and the fallback below act on it.
	createReminder(t, srv, "s1", "tidy the branch", time.Now().Add(-time.Minute))

	// Directory mismatch: nothing is delivered and the row stays due.
	lc := registerBoardLoop(t, srv, session.SessionID("s1"))
	if err := srv.catchUpReminders(context.Background(), session.SessionID("s1"), srv.dir+"-elsewhere"); err != nil {
		t.Fatalf("catch up with a foreign dir: %v", err)
	}
	if lc.HasPendingNotice() {
		t.Error("a foreign directory's catch-up pushed a notice")
	}

	// No loop registered: the handover has nowhere to go.
	if err := srv.catchUpReminders(context.Background(), session.SessionID("s2"), srv.dir); err != nil {
		t.Fatalf("catch up without a loop: %v", err)
	}
	if got := undeliveredCount(t, srv, "s1"); got != 1 {
		t.Errorf("a guarded catch-up moved the row: %d undelivered, want 1", got)
	}

	// Fallback: no loop runner keeps a tracker, so the rows would sit due
	// forever — a direct mark is the only stamp available.
	srv.loopRunner = nil
	if err := srv.catchUpReminders(context.Background(), session.SessionID("s1"), srv.dir); err != nil {
		t.Fatalf("catch up without a runner: %v", err)
	}
	if got := undeliveredCount(t, srv, "s1"); got != 0 {
		t.Errorf("the nil-runner fallback left rows due: %d undelivered, want 0", got)
	}
	if !lc.HasPendingNotice() {
		t.Error("the nil-runner fallback skipped the push")
	}
}

// TestPollRemindersDeliversDueTimers pins the composition ClaimDue and
// deliverReminder ride: a running server's ticker picks the deadline up and
// pushes it into the session's live loop without a turn having started.
func TestPollRemindersDeliversDueTimers(t *testing.T) {
	srv := reminderTestServer(t)
	id := session.SessionID("s1")
	createBoardSession(t, srv, id)
	lc := registerBoardLoop(t, srv, id)

	// Shrink the interval so the first tick lands within the wait window, and
	// restore no matter how the test finishes.
	oldInterval := reminderPollInterval
	reminderPollInterval = 20 * time.Millisecond
	t.Cleanup(func() { reminderPollInterval = oldInterval })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.pollReminders(ctx)

	// The poller is up; a deadline passing now is claimed — by the poller — on
	// the next tick, and delivered then.
	createPendingReminder(t, srv, "s1", "summarize the diff", time.Now().Add(-time.Minute))
	notice := waitNotice(t, lc)
	if !strings.Contains(notice.Text, "summarize the diff") {
		t.Errorf("ticker-delivered notice does not carry the note: %q", notice.Text)
	}
	if got := undeliveredCount(t, srv, "s1"); got != 1 {
		t.Errorf("the ticker delivered without tracking: %d undelivered, want 1", got)
	}

	// The dispatch — played by RunLoop by hand — clears the row.
	srv.loopRunner.BoardNoticesDelivered(lc, id, []agent.Notice{{Text: notice.Text}})
	if got := undeliveredCount(t, srv, "s1"); got != 0 {
		t.Errorf("a dispatched reminder stayed due: %d undelivered, want 0", got)
	}
}

// TestNilTimersIsASilentNoOp pins the nil handling Serve tolerates: a server
// without a timer store refuses through the tool and ignores the delivery
// paths, exactly like the board refusals do. The refusal rides the error —
// ScheduleResult carries data, not prose.
func TestNilTimersIsASilentNoOp(t *testing.T) {
	srv := newTestServer(t)
	srv.store = session.NewStore(srv.db)
	srv.loopRunner = &agent.LoopRunner{}

	_, err := srv.scheduleTool(context.Background(), "s1", tool.ScheduleRequest{Op: "create", Note: "n", DelaySeconds: 5})
	if err == nil {
		t.Fatal("schedule with a nil store did not refuse")
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Errorf("nil-store refusal does not say why: %v", err)
	}

	// The delivery paths neither panic nor touch anything.
	srv.deliverReminder(context.Background(), timer.Timer{ID: "t1", SessionID: "s1", Note: "n", FireAt: time.Now()})
	if err := srv.catchUpReminders(context.Background(), session.SessionID("s1"), srv.dir); err != nil {
		t.Fatalf("catch up with a nil store: %v", err)
	}
}

// TestReminderNoticeCarriesTheFireDate pins the notice date: reminders are
// scheduled days out, so the pushed text renders a date with the time — a
// dateless "17:05" reads wrong when the reminder was set for a year hence.
func TestReminderNoticeCarriesTheFireDate(t *testing.T) {
	text := reminderNotice(timer.Timer{
		FireAt: time.Date(2026, time.March, 9, 17, 5, 0, 0, time.UTC),
		Note:   "check CI",
	})
	want := "Scheduled reminder came due (set for Mar 9, 17:05): check CI"
	if text != want {
		t.Errorf("reminderNotice = %q, want %q", text, want)
	}
}

// TestScheduleToolValidation pins the create contract: a note is required and
// exactly one of delaySeconds or a future fireAt carries the deadline — a
// refusal rides the error, since ScheduleResult carries data, not prose. The
// happy path stores a row the session can list and cancel, echoing its id and
// fire time.
func TestScheduleToolValidation(t *testing.T) {
	srv := reminderTestServer(t)

	_, err := srv.scheduleTool(context.Background(), "s1", tool.ScheduleRequest{Op: "create", Note: "n"})
	if err == nil {
		t.Error("create with no deadline did not refuse")
	}
	_, err = srv.scheduleTool(context.Background(), "s1", tool.ScheduleRequest{
		Op: "create", Note: "n", DelaySeconds: 5, FireAt: time.Now().Add(time.Minute).Format(time.RFC3339),
	})
	if err == nil {
		t.Error("create with two deadlines did not refuse")
	}
	_, err = srv.scheduleTool(context.Background(), "s1", tool.ScheduleRequest{
		Op: "create", Note: "n", FireAt: time.Now().Add(-time.Minute).Format(time.RFC3339),
	})
	if err == nil {
		t.Error("create in the past did not refuse")
	}
	_, err = srv.scheduleTool(context.Background(), "s1", tool.ScheduleRequest{Op: "poke"})
	if err == nil {
		t.Error("an unknown op did not refuse")
	}
	_, err = srv.scheduleTool(context.Background(), "s1", tool.ScheduleRequest{Op: "create", DelaySeconds: 120})
	if err == nil {
		t.Error("create without a note did not refuse")
	}
	_, err = srv.scheduleTool(context.Background(), "s1", tool.ScheduleRequest{Op: "create", Note: "n", DelaySeconds: 9_999_999_999})
	if err == nil {
		t.Error("create with an over-cap delaySeconds did not refuse")
	} else if !strings.Contains(err.Error(), "too long") {
		t.Errorf("the over-cap refusal does not say why: %v", err)
	}
	_, err = srv.scheduleTool(context.Background(), "s1", tool.ScheduleRequest{Op: "create", Note: "n", DelaySeconds: -5})
	if err == nil {
		t.Error("create with a negative delaySeconds did not refuse")
	} else if err.Error() != "delaySeconds must be a positive number of seconds" {
		t.Errorf("negative delaySeconds refusal = %q, want the exact wording", err.Error())
	}

	// The happy path, both deadline shapes, each echoing what it stored:
	delayed, err := srv.scheduleTool(context.Background(), "s1", tool.ScheduleRequest{Op: "create", Note: "check CI", DelaySeconds: 120})
	if err != nil {
		t.Fatalf("create by delay: %v", err)
	}
	if delayed.ID == "" || delayed.FireAt == "" {
		t.Errorf("a delay-form create echoed nothing: id %q fireAt %q", delayed.ID, delayed.FireAt)
	}
	if _, err := time.Parse(time.RFC3339, delayed.FireAt); err != nil {
		t.Errorf("the echoed fireAt is not RFC3339: %q", delayed.FireAt)
	}
	absolute, err := srv.scheduleTool(context.Background(), "s1", tool.ScheduleRequest{
		Op: "create", Note: "re-run the flaky test", FireAt: time.Now().Add(time.Minute).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("create by fireAt: %v", err)
	}
	if absolute.ID == "" || absolute.FireAt == "" {
		t.Errorf("a fireAt create echoed nothing: id %q fireAt %q", absolute.ID, absolute.FireAt)
	}
	ctx, cancel := context.WithTimeout(context.Background(), boardOpTimeout)
	defer cancel()
	timers, err := srv.timers.List(ctx, "s1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(timers) != 2 {
		t.Fatalf("list found %d reminders, want 2", len(timers))
	}
	// The session lists its own, then cancels one before it fires.
	listed, err := srv.scheduleTool(context.Background(), "s1", tool.ScheduleRequest{Op: "list"})
	if err != nil {
		t.Fatalf("list op: %v", err)
	}
	if len(listed.Timers) != 2 {
		t.Errorf("the list op reported %d reminders, want 2", len(listed.Timers))
	}
	cancelled, err := srv.scheduleTool(context.Background(), "s1", tool.ScheduleRequest{Op: "cancel", ID: timers[0].ID})
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.ID != timers[0].ID {
		t.Errorf("cancel echoed id %q, want %q", cancelled.ID, timers[0].ID)
	}
	_, err = srv.scheduleTool(context.Background(), "s1", tool.ScheduleRequest{Op: "cancel", ID: timers[0].ID})
	if err == nil {
		t.Error("cancelling a cancelled reminder did not refuse")
	}
	if _, err := srv.timers.Cancel(ctx, "s1", timers[1].ID); err != nil {
		t.Fatalf("cancel second reminder: %v", err)
	}
	if timers, err = srv.timers.List(ctx, "s1"); err != nil || len(timers) != 0 {
		t.Fatalf("cancelled reminders stayed listed: %d rows, err %v", len(timers), err)
	}
}

// timerStatus reads one row's status from the project database directly; ok is
// false once the row is gone, which is how prune assertions tell deletion from
// any surviving status.
func timerStatus(t *testing.T, srv *Server, id string) (string, bool) {
	t.Helper()
	row := srv.db.QueryRowContext(context.Background(),
		`SELECT status FROM agent_timer WHERE id = ?`, id)
	var status string
	if err := row.Scan(&status); err != nil {
		return "", false
	}
	return status, true
}

// backdateTimer ages one row's updated_at past any cutoff the poller would
// prune at, the way time would.
func backdateTimer(t *testing.T, srv *Server, id string) {
	t.Helper()
	stale := time.Now().Add(-8 * 24 * time.Hour).Unix()
	if _, err := srv.db.ExecContext(context.Background(),
		`UPDATE agent_timer SET updated_at = ? WHERE id = ?`, stale, id); err != nil {
		t.Fatalf("backdate %s: %v", id, err)
	}
}

// TestPollRemindersPrunesOldTerminalRows pins the housekeeping wiring: the
// running poller deletes delivered and cancelled rows past the cutoff, and
// keeps a recent terminal row and every live row — pending or due, however
// stale — whose session may still collect it.
func TestPollRemindersPrunesOldTerminalRows(t *testing.T) {
	srv := reminderTestServer(t)
	oldInterval := reminderPollInterval
	oldCadence := timerPruneEveryTicks
	reminderPollInterval = 10 * time.Millisecond
	timerPruneEveryTicks = 1 // a prune on every tick, so the wait window is one tick
	t.Cleanup(func() { reminderPollInterval = oldInterval; timerPruneEveryTicks = oldCadence })

	// Staged before the poller starts, with the due rows claimed by hand — the
	// same hands the claim scan would use — so no assertion races a tick. The
	// one live pending row sits in the future, so the poller claims nothing:
	// only its prune is under test.
	oldDue := createReminder(t, srv, "s1", "old due", time.Now().Add(-time.Minute))
	pending := createPendingReminder(t, srv, "s1", "fresh pending", time.Now().Add(time.Hour))
	delivered := createReminder(t, srv, "s1", "old delivered", time.Now().Add(-time.Minute))
	if err := srv.timers.MarkDelivered(context.Background(), []string{delivered.ID}); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}
	recent := createReminder(t, srv, "s1", "recent delivered", time.Now().Add(-time.Minute))
	if err := srv.timers.MarkDelivered(context.Background(), []string{recent.ID}); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}
	cancelled := createPendingReminder(t, srv, "s1", "old cancelled", time.Now().Add(time.Hour))
	if _, err := srv.timers.Cancel(context.Background(), "s1", cancelled.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	for _, id := range []string{oldDue.ID, pending.ID, delivered.ID, cancelled.ID} {
		backdateTimer(t, srv, id)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.pollReminders(ctx)

	// The old terminal pair goes, and nothing else moves.
	gone := time.Now().Add(2 * time.Second)
	for {
		_, hadDelivered := timerStatus(t, srv, delivered.ID)
		_, hadCancelled := timerStatus(t, srv, cancelled.ID)
		if !hadDelivered && !hadCancelled {
			break
		}
		if time.Now().After(gone) {
			t.Fatalf("the poller never pruned the old terminal rows: delivered %v, cancelled %v still present", hadDelivered, hadCancelled)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if status, ok := timerStatus(t, srv, recent.ID); !ok || status != timer.StatusDelivered {
		t.Errorf("a recent delivered row was disturbed: %q present=%v", status, ok)
	}
	if status, ok := timerStatus(t, srv, oldDue.ID); !ok || status != timer.StatusDue {
		t.Errorf("a stale due row was pruned: %q present=%v", status, ok)
	}
	if status, ok := timerStatus(t, srv, pending.ID); !ok || status != timer.StatusPending {
		t.Errorf("a stale pending row was pruned: %q present=%v", status, ok)
	}
}

// TestPollRemindersStopsWhenServerStops pins the shutdown fix: the poller runs
// on a ctx Stop() never cancels, so Stop's own stopCh close must end it — it
// claims deadlines up to the stop, and never touches the store again after it,
// though the ctx lives on for the rest of the test.
func TestPollRemindersStopsWhenServerStops(t *testing.T) {
	srv := reminderTestServer(t)
	// serve() allocates stopCh before it selects on it; a bare rig wires the
	// same channel by hand, and Stop() closes exactly what the loop watches.
	srv.stopCh = make(chan struct{})
	oldInterval := reminderPollInterval
	reminderPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { reminderPollInterval = oldInterval })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		srv.pollReminders(ctx)
		close(done)
	}()

	// Before the stop the loop is live: a deadline that passed is claimed on
	// the next pass. It rides session s0 so the ledger it leaves behind — no
	// loop runs to mark it delivered — can never be read by the after-stop
	// probe, which uses its own session s1.
	createReminder(t, srv, "s0", "before the stop", time.Now().Add(-time.Minute))
	wait := time.Now().Add(2 * time.Second)
	for undeliveredCount(t, srv, "s0") != 1 {
		if time.Now().After(wait) {
			t.Fatal("the poller never claimed a due reminder before the stop")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Stop() closes stopCh — the poller's only stop signal, since its ctx is
	// never cancelled — and the goroutine returns promptly.
	srv.Stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pollReminders was still running after Stop()")
	}

	// And it stays stopped: a reminder that comes due afterwards is never
	// claimed — the next server's startup pass hands it over, not a dead
	// poller. s1 has no other rows, so a nonzero count is a fresh claim and
	// nothing else.
	createPendingReminder(t, srv, "s1", "after the stop", time.Now().Add(-time.Minute))
	time.Sleep(20 * reminderPollInterval)
	if got := undeliveredCount(t, srv, "s1"); got != 0 {
		t.Errorf("a reminder was claimed after Stop(): %d undelivered, want 0", got)
	}
}
