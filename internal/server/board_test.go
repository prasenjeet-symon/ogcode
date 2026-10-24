package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/agent"
	"github.com/prasenjeet-symon/ogcode/internal/board"
	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// boardTestServer is a Server with a live board on temp DBs.
func boardTestServer(t *testing.T) *Server {
	t.Helper()
	srv := newTestServer(t)
	srv.bus = bus.New(64)
	srv.board = board.Open(srv.db, srv.dir)
	return srv
}

// waitNotice returns the next notice pushed into lc, waiting for the push to
// land.
func waitNotice(t *testing.T, lc *agent.LoopControl) agent.Notice {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if batch := lc.DrainNotices(); len(batch) > 0 {
			return batch[0]
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no notice was pushed")
	return agent.Notice{}
}

// registerBoardLoop puts a LoopControl in place for a session, the way
// startSessionLoop does for a running turn.
func registerBoardLoop(t *testing.T, srv *Server, id session.SessionID) *agent.LoopControl {
	t.Helper()
	lc := agent.NewLoopControl()
	srv.mu.Lock()
	if srv.loopControls == nil {
		srv.loopControls = map[session.SessionID]*agent.LoopControl{}
	}
	srv.loopControls[id] = lc
	srv.mu.Unlock()
	return lc
}

// registerBoardAgent puts one handle on the roster.
func registerBoardAgent(t *testing.T, srv *Server, h board.Handle) board.Handle {
	t.Helper()
	got, err := srv.board.Register(context.Background(), h)
	if err != nil {
		t.Fatalf("register %s: %v", h.Handle, err)
	}
	return got
}

// createBoardSession writes the session row a teammate's loop would own, so the
// report can read its last message.
func createBoardSession(t *testing.T, srv *Server, id session.SessionID) {
	t.Helper()
	if err := srv.store.Create(&session.Session{
		ID: id, ProjectID: srv.dir, Directory: srv.dir, Title: "t",
		SessionType: "build", CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
}

// createAssistantMessage records the message a teammate ends on: the result its
// spawner is handed.
func createAssistantMessage(t *testing.T, srv *Server, id session.SessionID, text string) {
	t.Helper()
	msgID := session.NewMessageID()
	if err := srv.store.CreateMessage(&session.MessageInfo{
		ID: msgID, SessionID: id, Role: session.RoleAssistant, CreatedAt: session.Now(),
	}); err != nil {
		t.Fatalf("create message: %v", err)
	}
	data, err := json.Marshal(session.TextPartData{Text: text})
	if err != nil {
		t.Fatalf("marshal part: %v", err)
	}
	if err := srv.store.CreatePart(&session.Part{
		ID: session.NewPartID(), MessageID: msgID, SessionID: id,
		Type: session.PartText, Data: data,
		CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}); err != nil {
		t.Fatalf("create part: %v", err)
	}
}

// TestStartSessionLoopJoinsAnEmptyAgentAsBuild pins the normalisation the web
// client depends on: a prompt carries no agent, so startSessionLoop is handed a
// blank name and must still join the roster as the build agent. joinBoard
// refuses every name but "build", and losing this would leave every interactive
// session running with no board at all — silently, since the refusal is logged.
func TestStartSessionLoopJoinsAnEmptyAgentAsBuild(t *testing.T) {
	srv := boardTestServer(t)
	srv.store = session.NewStore(srv.db)
	srv.mu.Lock()
	srv.running = map[session.SessionID]context.CancelFunc{}
	srv.runningToken = map[session.SessionID]uint64{}
	srv.runningDone = map[session.SessionID]chan struct{}{}
	srv.loopControls = map[session.SessionID]*agent.LoopControl{}
	srv.mu.Unlock()
	// The loop is not what this test watches: no session row exists, so RunLoop
	// exits at once. The join happens before it is started.
	srv.loopRunner = &agent.LoopRunner{Store: srv.store, Bus: srv.bus}

	sid := session.SessionID("ses_blank_agent")
	srv.startSessionLoop(sid, "", 0, 0, "")

	ctx := context.Background()
	h, err := srv.board.BySession(ctx, string(sid))
	if err != nil {
		t.Fatalf("by session: %v", err)
	}
	if h.Handle == "" {
		t.Fatal("a blank agent name did not join the board as build")
	}
	if h.Role != "build" || h.Kind != board.KindSession {
		t.Fatalf("joined as role %q kind %q, want build/%s", h.Role, h.Kind, board.KindSession)
	}

	// Let the loop goroutine finish before the temp DBs are closed under it.
	deadline := time.Now().Add(5 * time.Second)
	for srv.sessionRunning(sid) {
		if time.Now().After(deadline) {
			t.Fatal("the loop started by startSessionLoop did not exit")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestSpawnTeammateAllowsNestingAndCapsNothing pins the two rails this feature
// deliberately dropped: a teammate may spawn teammates of its own, and no
// ceiling is put on how many are live in one directory.
func TestSpawnTeammateAllowsNestingAndCapsNothing(t *testing.T) {
	srv := boardTestServer(t)
	srv.store = session.NewStore(srv.db)
	srv.mu.Lock()
	srv.running = map[session.SessionID]context.CancelFunc{}
	srv.runningToken = map[session.SessionID]uint64{}
	srv.runningDone = map[session.SessionID]chan struct{}{}
	srv.loopControls = map[session.SessionID]*agent.LoopControl{}
	srv.mu.Unlock()
	// The loops are not what this test watches: the registry is empty, so each
	// child exits on its first resolve instead of reaching a model. The spawn
	// result is written before that.
	srv.loopRunner = &agent.LoopRunner{Store: srv.store, Bus: srv.bus, Registry: srv.registry}

	ctx := context.Background()
	mk := func() *session.Session {
		s := &session.Session{
			ID:          session.NewSessionID(),
			ProjectID:   srv.dir,
			Directory:   srv.dir,
			SessionType: "build",
			CreatedAt:   session.Now(),
			UpdatedAt:   session.Now(),
		}
		if err := srv.store.Create(s); err != nil {
			t.Fatalf("create session: %v", err)
		}
		return s
	}
	parent := mk()
	if _, err := srv.joinBoard(ctx, parent.ID, "build"); err != nil {
		t.Fatalf("join parent: %v", err)
	}
	// A teammate handle is what a nested spawn is handed as its parent.
	child := mk()
	teammate, err := srv.board.Register(ctx, board.Handle{
		Handle: "tm-parent", SessionID: string(child.ID), Role: "build",
		Kind: board.KindTeammate, SpawnedBy: "build-x", Status: board.StatusRunning,
	})
	if err != nil {
		t.Fatalf("register teammate: %v", err)
	}

	// Six is past the four that used to be the ceiling, so a cap that survived
	// would refuse the last two.
	var spawned []session.SessionID
	for i := 0; i < 6; i++ {
		res, err := srv.spawnTeammate(ctx, child, teammate, tool.TeammateRequest{
			Op: "spawn", Name: fmt.Sprintf("nested-%d", i), Prompt: "check one file",
		})
		if err != nil {
			t.Fatalf("spawn %d: %v", i, err)
		}
		if res.Handle == "" {
			t.Fatalf("spawn %d refused: %s", i, res.Notice)
		}
		spawned = append(spawned, session.SessionID(res.SessionID))
	}
	// The rows, not the live count: a child whose loop ends flips to done, so
	// liveTeammates is a race once the loops exit. Every spawn leaves a row.
	roster, err := srv.board.Roster(ctx)
	if err != nil {
		t.Fatalf("roster: %v", err)
	}
	held := map[string]bool{}
	for _, h := range roster {
		held[h.Handle] = true
	}
	for i := 0; i < 6; i++ {
		name := fmt.Sprintf("nested-%d", i)
		if !held[name] {
			t.Fatalf("%s is not on the roster after its spawn — %d handles held", name, len(roster))
		}
	}
	// The tree edge is what the drawer nests them by.
	children, err := srv.board.Children(ctx, "tm-parent")
	if err != nil {
		t.Fatalf("children: %v", err)
	}
	if len(children) != 6 {
		t.Fatalf("tm-parent has %d children on the board, want 6", len(children))
	}

	// Let the loop goroutines finish before the temp DBs are closed under them.
	deadline := time.Now().Add(10 * time.Second)
	for {
		running := false
		for _, id := range spawned {
			if srv.sessionRunning(id) {
				running = true
				break
			}
		}
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a loop started by spawnTeammate did not exit")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestBoardTurnStartKeepsAnExistingHandle pins the second-turn path: a session
// that is already on the roster keeps its handle, and the turn is recorded as
// running. Registering again would fail UNIQUE(board_agent.session_id), and
// because Register is the write that sets StatusRunning, a failed join left a
// running session reporting idle for the whole turn.
func TestBoardTurnStartKeepsAnExistingHandle(t *testing.T) {
	srv := boardTestServer(t)
	ctx := context.Background()
	sid := session.SessionID("ses_second_turn")
	h, err := srv.board.Register(ctx, board.Handle{
		Handle:    "build-first",
		SessionID: string(sid),
		Role:      "build",
		Kind:      board.KindSession,
		Status:    board.StatusRunning,
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	// The previous turn ended, so the row is idle when the next one starts.
	if err := srv.board.SetStatus(ctx, h.Handle, board.StatusIdle); err != nil {
		t.Fatalf("set idle: %v", err)
	}

	if err := srv.boardTurnStart(ctx, sid, "build"); err != nil {
		t.Fatalf("turn start on a session already on the board: %v", err)
	}
	got, err := srv.board.BySession(ctx, string(sid))
	if err != nil {
		t.Fatalf("by session: %v", err)
	}
	if got.Handle != h.Handle {
		t.Fatalf("handle %q after a second turn, want the original %q", got.Handle, h.Handle)
	}
	if got.Status != board.StatusRunning {
		t.Fatalf("status %q at turn start, want %q", got.Status, board.StatusRunning)
	}

	// Starting again while the turn is live is a no-op, not a second register.
	if err := srv.boardTurnStart(ctx, sid, "build"); err != nil {
		t.Fatalf("turn start on a running session: %v", err)
	}
	roster, err := srv.board.Roster(ctx)
	if err != nil {
		t.Fatalf("roster: %v", err)
	}
	n := 0
	for _, row := range roster {
		if row.SessionID == string(sid) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d roster rows for the session, want 1", n)
	}
}

// TestLeaveBoardReportsCompletionToARunningSpawner pins the guaranteed report:
// a teammate's loop ends, its row becomes done, and its spawner is told in the
// turn it is already in — carrying the text of the teammate's last message, so
// the parent has the result without opening the child's session. The row is
// stamped reported only by the step that drains the notice, not by the push:
// a push-time stamp would let a turn that ends first swallow the report.
func TestLeaveBoardReportsCompletionToARunningSpawner(t *testing.T) {
	srv := boardTestServer(t)
	srv.store = session.NewStore(srv.db)
	// The report tracker hangs off the loop runner, the way the server holds it.
	srv.loopRunner = &agent.LoopRunner{}
	registerBoardAgent(t, srv, board.Handle{
		Handle: "build-x", SessionID: "s1", Role: "build",
		Kind: board.KindSession, Status: board.StatusRunning,
	})
	worker := registerBoardAgent(t, srv, board.Handle{
		Handle: "worker", SessionID: "s2", Role: "build",
		Kind: board.KindTeammate, SpawnedBy: "build-x", Status: board.StatusRunning,
	})
	parentLC := registerBoardLoop(t, srv, "s1")
	id := session.SessionID(worker.SessionID)
	createBoardSession(t, srv, id)
	createAssistantMessage(t, srv, id, "the parser is done")

	srv.leaveBoard(id)

	cur, err := srv.board.Get(context.Background(), "worker")
	if err != nil {
		t.Fatalf("get worker: %v", err)
	}
	if cur.Status != board.StatusDone {
		t.Errorf("teammate status = %q, want %q", cur.Status, board.StatusDone)
	}
	if !cur.Unreported() {
		t.Error("a completion was stamped reported before the spawner's loop drained its notice")
	}

	notice := waitNotice(t, parentLC)
	if !strings.Contains(notice.Text, "the parser is done") {
		t.Errorf("pushed notice does not carry the teammate's last message: %q", notice.Text)
	}
	if !strings.Contains(notice.Text, "worker") || !strings.Contains(notice.Text, "/session/s2") {
		t.Errorf("pushed notice does not name the teammate or its session: %q", notice.Text)
	}
	// The step that drained the notice concludes the report — RunLoop runs this
	// callback straight after its drain, and only then is CatchUpTeammates kept
	// from handing the same report over again.
	srv.loopRunner.BoardNoticesDelivered(parentLC, "s1", []agent.Notice{notice})
	cur, err = srv.board.Get(context.Background(), "worker")
	if err != nil {
		t.Fatalf("re-get worker: %v", err)
	}
	if cur.Unreported() {
		t.Error("a report whose notice drained into its spawner's step was left unreported")
	}

	// Ending the loop again is not a second piece of news.
	srv.leaveBoard(id)
	if parentLC.HasPendingNotice() {
		t.Error("the same completion was pushed twice")
	}
}

// TestLeaveBoardOfASessionGoesIdle pins the other half: a session between turns
// is idle, and nobody is told anything — a session has no spawner.
func TestLeaveBoardOfASessionGoesIdle(t *testing.T) {
	srv := boardTestServer(t)
	srv.store = session.NewStore(srv.db)
	registerBoardAgent(t, srv, board.Handle{
		Handle: "build-x", SessionID: "s1", Role: "build",
		Kind: board.KindSession, Status: board.StatusRunning,
	})
	child := registerBoardAgent(t, srv, board.Handle{
		Handle: "worker", SessionID: "s2", Role: "build",
		Kind: board.KindTeammate, SpawnedBy: "build-x", Status: board.StatusRunning,
	})
	parentLC := registerBoardLoop(t, srv, "s1")
	createBoardSession(t, srv, "s1")

	srv.leaveBoard("s1")

	got, err := srv.board.Get(context.Background(), "build-x")
	if err != nil {
		t.Fatalf("get build-x: %v", err)
	}
	if got.Status != board.StatusIdle {
		t.Errorf("session status after its turn = %q, want idle", got.Status)
	}
	if child.Status != board.StatusRunning {
		t.Errorf("the teammate's row moved to %q when its spawner's turn ended, want running", child.Status)
	}
	if parentLC.HasPendingNotice() {
		t.Error("a session ending its turn reported something to itself")
	}
}

// TestCatchUpTeammatesReportsAnIdleSpawnerOnce pins the handover at turn
// start: a completion that arrived while the spawner was between turns waits
// on its row and is pushed when the next turn starts. The push is tracked, not
// stamped: the row is stamped only when that turn's step dispatches the
// notice, so a turn that dies first leaves the row unreported and a later
// catch-up re-pushes the same report — delivery is at-least-once, and once a
// dispatch has carried it no catch-up repeats it.
func TestCatchUpTeammatesReportsAnIdleSpawnerOnce(t *testing.T) {
	srv := boardTestServer(t)
	srv.store = session.NewStore(srv.db)
	// The tracker lives on the runner, as it does in the server.
	srv.loopRunner = &agent.LoopRunner{}
	registerBoardAgent(t, srv, board.Handle{
		Handle: "build-x", SessionID: "s1", Role: "build",
		Kind: board.KindSession, Status: board.StatusRunning,
	})
	worker := registerBoardAgent(t, srv, board.Handle{
		Handle: "worker", SessionID: "s2", Role: "build",
		Kind: board.KindTeammate, SpawnedBy: "build-x", Status: board.StatusRunning,
	})
	id := session.SessionID(worker.SessionID)
	createBoardSession(t, srv, id)
	createAssistantMessage(t, srv, id, "the parser is done")

	// No loop is registered for the spawner, which is the point: the completion
	// arrives between its turns and nothing wakes it.
	srv.leaveBoard(id)
	cur, err := srv.board.Get(context.Background(), "worker")
	if err != nil {
		t.Fatalf("get worker: %v", err)
	}
	if !cur.Unreported() {
		t.Fatal("a completion that arrived while the spawner was idle was marked reported")
	}

	lc := registerBoardLoop(t, srv, "s1")
	if err := srv.catchUpTeammates(context.Background(), "s1", srv.dir); err != nil {
		t.Fatalf("catch up: %v", err)
	}
	notice := waitNotice(t, lc)
	if !strings.Contains(notice.Text, "the parser is done") {
		t.Errorf("caught-up notice does not carry the teammate's last message: %q", notice.Text)
	}
	cur, err = srv.board.Get(context.Background(), "worker")
	if err != nil {
		t.Fatalf("re-get worker: %v", err)
	}
	if !cur.Unreported() {
		t.Fatal("a catch-up push was stamped before any request carried it — the stamp belongs to the drain")
	}

	// The turn never dispatched the notice and dies; its LoopControl dies with
	// it and the next turn registers a fresh one, the way startSessionLoop does.
	// The board's tracker drops the dead turn's report, the row is still
	// unreported, so this turn's catch-up re-pushes the same report there.
	lc = registerBoardLoop(t, srv, "s1")
	if err := srv.catchUpTeammates(context.Background(), "s1", srv.dir); err != nil {
		t.Fatalf("catch up again: %v", err)
	}
	if !lc.HasPendingNotice() {
		t.Fatal("an undelivered completion was never re-pushed; its row was stamped and the report lost")
	}
	// The step that drained it concludes the report — RunLoop's callback,
	// played by hand, exactly as the running-spawner pins do.
	notice = waitNotice(t, lc)
	srv.loopRunner.BoardNoticesDelivered(lc, "s1", []agent.Notice{notice})
	cur, err = srv.board.Get(context.Background(), "worker")
	if err != nil {
		t.Fatalf("re-get worker: %v", err)
	}
	if cur.Unreported() {
		t.Error("a report whose notice was dispatched stayed unreported")
	}

	// Once a dispatch has carried it, it is once: a further turn start is quiet.
	if err := srv.catchUpTeammates(context.Background(), "s1", srv.dir); err != nil {
		t.Fatalf("catch up a third time: %v", err)
	}
	if lc.HasPendingNotice() {
		t.Error("a dispatched completion was handed to another turn")
	}
}

// TestTeammateReportUndrainedAtTurnEndStillDelivers pins the report-loss fix:
// a completion pushed into a spawner's running turn is stamped reported by the
// step that drains it, not by the push — so a turn that ends without draining
// (its LoopControl is replaced) leaves the row unreported, and the idle
// spawner's next turn hands the report over via catchUpTeammates. The old
// push-time stamp let the turn-scoped notice die with the turn and took the
// report down with it.
func TestTeammateReportUndrainedAtTurnEndStillDelivers(t *testing.T) {
	srv := boardTestServer(t)
	srv.store = session.NewStore(srv.db)
	srv.loopRunner = &agent.LoopRunner{}
	registerBoardAgent(t, srv, board.Handle{
		Handle: "build-x", SessionID: "s1", Role: "build",
		Kind: board.KindSession, Status: board.StatusRunning,
	})
	worker := registerBoardAgent(t, srv, board.Handle{
		Handle: "worker", SessionID: "s2", Role: "build",
		Kind: board.KindTeammate, SpawnedBy: "build-x", Status: board.StatusRunning,
	})
	// The spawner's turn is running when the child finishes; nothing ever
	// drains the notice this push queues.
	registerBoardLoop(t, srv, "s1")
	id := session.SessionID(worker.SessionID)
	createBoardSession(t, srv, id)
	createAssistantMessage(t, srv, id, "the parser is done")

	srv.leaveBoard(id)

	cur, err := srv.board.Get(context.Background(), "worker")
	if err != nil {
		t.Fatalf("get worker: %v", err)
	}
	if !cur.Unreported() {
		t.Fatal("the push stamped the report before any drain; the turn ended without delivering it")
	}

	// The spawner's turn ends without draining its notice. Its LoopControl is
	// dropped and replaced, which is what the next turn's registration does.
	nextLC := registerBoardLoop(t, srv, "s1")
	if err := srv.catchUpTeammates(context.Background(), "s1", srv.dir); err != nil {
		t.Fatalf("catch up: %v", err)
	}
	notice := waitNotice(t, nextLC)
	if !strings.Contains(notice.Text, "the parser is done") {
		t.Errorf("catch-up notice does not carry the teammate's last message: %q", notice.Text)
	}
	// The catch-up push is tracked the same way, not stamped: its mark lands
	// when this turn's step dispatches the notice — played by hand here.
	srv.loopRunner.BoardNoticesDelivered(nextLC, "s1", []agent.Notice{notice})
	cur, err = srv.board.Get(context.Background(), "worker")
	if err != nil {
		t.Fatalf("re-get worker: %v", err)
	}
	if cur.Unreported() {
		t.Error("a report whose notice was dispatched was left unreported, so it would deliver twice")
	}

	// Once is once: the turn start after that is quiet again.
	if err := srv.catchUpTeammates(context.Background(), "s1", srv.dir); err != nil {
		t.Fatalf("catch up again: %v", err)
	}
	if nextLC.HasPendingNotice() {
		t.Error("the same completion was handed to two turns")
	}
}

// TestWaitTeammateReportsLastMessage pins the blocking path: a wait returns the
// child's status and last message rather than only that it finished.
func TestWaitTeammateReportsLastMessage(t *testing.T) {
	srv := boardTestServer(t)
	srv.store = session.NewStore(srv.db)
	parent := registerBoardAgent(t, srv, board.Handle{
		Handle: "build-x", SessionID: "s1", Role: "build",
		Kind: board.KindSession, Status: board.StatusRunning,
	})
	worker := registerBoardAgent(t, srv, board.Handle{
		Handle: "worker", SessionID: "s2", Role: "build",
		Kind: board.KindTeammate, SpawnedBy: "build-x", Status: board.StatusRunning,
	})
	id := session.SessionID(worker.SessionID)
	createBoardSession(t, srv, id)
	createAssistantMessage(t, srv, id, "the parser is done")

	go func() {
		time.Sleep(100 * time.Millisecond)
		if err := srv.board.SetStatus(context.Background(), "worker", board.StatusDone); err != nil {
			t.Errorf("set status: %v", err)
		}
	}()

	res, err := srv.waitTeammate(context.Background(), parent, tool.TeammateRequest{
		Op: "wait", Handles: []string{"worker"}, Timeout: 5,
	})
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if len(res.Reports) != 1 {
		t.Fatalf("wait reported %d teammates, want 1: %+v", len(res.Reports), res)
	}
	if got := res.Reports[0]; got.Handle != "worker" || got.Status != board.StatusDone || !strings.Contains(got.Text, "the parser is done") {
		t.Fatalf("wait report = %+v, want worker done with its last message", got)
	}
	if res.Notice != "They are done." {
		t.Errorf("wait notice = %q, want %q", res.Notice, "They are done.")
	}
}

// TestStepBudgetLeavesTeammatesUncapped pins that a teammate is never cut off at
// a step count: the runner is told to run one uncapped, while every other session
// leaves the runner's own budget in place.
func TestStepBudgetLeavesTeammatesUncapped(t *testing.T) {
	srv := boardTestServer(t)
	registerBoardAgent(t, srv, board.Handle{
		Handle: "build-x", SessionID: "s1", Role: "build",
		Kind: board.KindSession, Status: board.StatusRunning,
	})
	registerBoardAgent(t, srv, board.Handle{
		Handle: "worker", SessionID: "s2", Role: "build",
		Kind: board.KindTeammate, SpawnedBy: "build-x", Status: board.StatusRunning,
	})

	steps, resumes := srv.stepBudgetFor("s2")
	if steps != agent.MaxStepsUncapped || resumes != 0 {
		t.Fatalf("teammate budget = (%d, %d), want (%d, 0)", steps, resumes, agent.MaxStepsUncapped)
	}
	if steps, resumes := srv.stepBudgetFor("s1"); steps != 0 || resumes != 0 {
		t.Fatalf("session budget override = (%d, %d), want (0, 0) so the runner's own budget applies", steps, resumes)
	}
}

// TestTeammateReportKeepsTheWholeMessage pins that a report is not clipped: the
// spawner is handed the child's last message exactly as the child wrote it.
func TestTeammateReportKeepsTheWholeMessage(t *testing.T) {
	body := strings.Repeat("a line of the result\n", 2000) // ~40 KB, past the old 4 KB cut
	got := teammateReportNotice(board.Handle{
		Handle: "worker", SessionID: "s2", Status: board.StatusDone,
	}, body)
	if !strings.Contains(got, strings.TrimSpace(body)) {
		t.Fatalf("report dropped part of the message: %d bytes in, %d out", len(body), len(got))
	}
}

// TestStopTeammateIsNotRewrittenDone pins the race cancelLoop leaves open: it
// does not wait for the loop to exit, so a stopped teammate's own leaveBoard
// arrives afterwards. The stop claims the row first, so the late leaveBoard must
// not overwrite it with done and tell the spawner the child finished.
func TestStopTeammateIsNotRewrittenDone(t *testing.T) {
	srv := boardTestServer(t)
	srv.store = session.NewStore(srv.db)
	registerBoardAgent(t, srv, board.Handle{
		Handle: "build-x", SessionID: "s1", Role: "build",
		Kind: board.KindSession, Status: board.StatusRunning,
	})
	worker := registerBoardAgent(t, srv, board.Handle{
		Handle: "worker", SessionID: "s2", Role: "build",
		Kind: board.KindTeammate, SpawnedBy: "build-x", Status: board.StatusRunning,
	})
	parentLC := registerBoardLoop(t, srv, "s1")
	id := session.SessionID(worker.SessionID)
	createBoardSession(t, srv, id)
	createAssistantMessage(t, srv, id, "partway through")

	res, err := srv.stopTeammate(context.Background(), "worker")
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !strings.Contains(res.Notice, "Stopped worker") {
		t.Fatalf("notice = %q, want a stop confirmation", res.Notice)
	}

	// The stop is itself the report: the spawner is told the child was
	// stopped, with the message it had when it was cancelled — not a
	// completion it never reached.
	notice := waitNotice(t, parentLC)
	if !strings.Contains(notice.Text, "was stopped") || !strings.Contains(notice.Text, "partway through") {
		t.Errorf("stop report = %q, want the stopped child and its last message", notice.Text)
	}

	// The cancelled loop now winds up and records its own end.
	srv.leaveBoard(id)

	cur, err := srv.board.Get(context.Background(), "worker")
	if err != nil {
		t.Fatalf("get worker: %v", err)
	}
	if cur.Status != board.StatusStopped {
		t.Errorf("teammate status = %q, want %q — the loop's leaveBoard rewrote the stop", cur.Status, board.StatusStopped)
	}
	if parentLC.HasPendingNotice() {
		t.Error("the stopped child's own leaveBoard pushed a second piece of news about it")
	}
}

// TestStopTeammateOfAFinishedTeammateChangesNothing pins the other order: a
// completion that landed first is final, so a later stop must leave the row and
// its report alone rather than re-announcing the same completion.
func TestStopTeammateOfAFinishedTeammateChangesNothing(t *testing.T) {
	srv := boardTestServer(t)
	srv.store = session.NewStore(srv.db)
	// The report is stamped when its drain concludes it, so the stop's
	// assertions below have a reported row to be checked against.
	srv.loopRunner = &agent.LoopRunner{}
	registerBoardAgent(t, srv, board.Handle{
		Handle: "build-x", SessionID: "s1", Role: "build",
		Kind: board.KindSession, Status: board.StatusRunning,
	})
	worker := registerBoardAgent(t, srv, board.Handle{
		Handle: "worker", SessionID: "s2", Role: "build",
		Kind: board.KindTeammate, SpawnedBy: "build-x", Status: board.StatusRunning,
	})
	parentLC := registerBoardLoop(t, srv, "s1")
	id := session.SessionID(worker.SessionID)
	createBoardSession(t, srv, id)
	createAssistantMessage(t, srv, id, "the parser is done")

	srv.leaveBoard(id)
	notice := waitNotice(t, parentLC)
	// RunLoop's drain site, played by hand: the notice this step carries is
	// what stamps the row reported.
	srv.loopRunner.BoardNoticesDelivered(parentLC, "s1", []agent.Notice{notice})

	res, err := srv.stopTeammate(context.Background(), "worker")
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !strings.Contains(res.Notice, "not stopped") {
		t.Fatalf("notice = %q, want a refusal naming the finished status", res.Notice)
	}

	cur, err := srv.board.Get(context.Background(), "worker")
	if err != nil {
		t.Fatalf("get worker: %v", err)
	}
	if cur.Status != board.StatusDone {
		t.Errorf("teammate status = %q, want %q to stay — a finished teammate was rewritten", cur.Status, board.StatusDone)
	}
	if cur.Unreported() {
		t.Error("stopping a finished teammate marked its completion unreported again")
	}
	if parentLC.HasPendingNotice() {
		t.Error("stopping a finished teammate re-reported its completion")
	}
}

// TestSpawnBackgroundTaskRegistersATaskChild pins the spawn path: the task
// tool's background mode starts a subagent session on the board as a task child
// of the calling build session, with the prompt as its first message, and
// returns where it will run — the caller keeps working instead of waiting.
func TestSpawnBackgroundTaskRegistersATaskChild(t *testing.T) {
	srv := boardTestServer(t)
	srv.store = session.NewStore(srv.db)
	srv.mu.Lock()
	srv.running = map[session.SessionID]context.CancelFunc{}
	srv.runningToken = map[session.SessionID]uint64{}
	srv.runningDone = map[session.SessionID]chan struct{}{}
	srv.loopControls = map[session.SessionID]*agent.LoopControl{}
	srv.mu.Unlock()
	// The loop is not what this test watches: the registry is empty, so the
	// child exits on its first model resolve as its teammate sibling does.
	srv.loopRunner = &agent.LoopRunner{Store: srv.store, Bus: srv.bus, Registry: srv.registry}

	ctx := context.Background()
	parent := &session.Session{
		ID: session.NewSessionID(), ProjectID: srv.dir, Directory: srv.dir,
		SessionType: "build", CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}
	if err := srv.store.Create(parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}

	res, err := srv.spawnBackgroundTask(ctx, string(parent.ID), "trace auth middleware", "find every middleware")
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if res.Handle == "" {
		t.Fatalf("spawn refused: %s", res.Notice)
	}
	if !strings.HasPrefix(res.Handle, "task-") {
		t.Errorf("handle = %q, want a task-prefixed name", res.Handle)
	}
	if res.URL != "/session/"+res.SessionID {
		t.Errorf("URL = %q, want the child's session page", res.URL)
	}
	if res.Live < 1 {
		t.Errorf("Live = %d, want the freshly started child counted", res.Live)
	}

	parentHandle, err := srv.board.BySession(ctx, string(parent.ID))
	if err != nil || parentHandle.Handle == "" {
		t.Fatalf("the calling session did not join the board: %v", err)
	}
	h, err := srv.board.Get(ctx, res.Handle)
	if err != nil {
		t.Fatalf("get %s: %v", res.Handle, err)
	}
	if h.Kind != board.KindTask {
		t.Errorf("kind = %q, want %q", h.Kind, board.KindTask)
	}
	if h.Role != "subagent" {
		t.Errorf("role = %q, want subagent", h.Role)
	}
	if h.SpawnedBy != parentHandle.Handle {
		t.Errorf("spawned by %q, want the caller's handle %q", h.SpawnedBy, parentHandle.Handle)
	}
	child, err := srv.store.Get(session.SessionID(res.SessionID))
	if err != nil || child == nil {
		t.Fatalf("child session missing: %v", err)
	}
	if child.SessionType != "subagent" {
		t.Errorf("child session type = %q, want subagent", child.SessionType)
	}
	if !strings.HasPrefix(child.Title, "Task: ") {
		t.Errorf("child title = %q, want it to carry the task label", child.Title)
	}
	msgs, err := srv.store.GetMessages(session.SessionID(res.SessionID), session.MessageID(""), 10)
	if err != nil || len(msgs) == 0 {
		t.Fatalf("child started with no messages: %v", err)
	}
	promptSeen := false
	for _, m := range msgs {
		if m.Info.Role != session.RoleUser {
			continue
		}
		for _, p := range m.Parts {
			if p.Type != session.PartText {
				continue
			}
			var td session.TextPartData
			if err := json.Unmarshal(p.Data, &td); err == nil && strings.Contains(td.Text, "find every middleware") {
				promptSeen = true
			}
		}
	}
	if !promptSeen {
		t.Error("the task prompt did not ride the child's first message")
	}

	// Let the child loop's goroutine exit before the temp DBs close under it.
	deadline := time.Now().Add(10 * time.Second)
	for srv.sessionRunning(session.SessionID(res.SessionID)) {
		if time.Now().After(deadline) {
			t.Fatal("the child's loop did not exit")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestLeaveBoardReportsABackgroundTaskToARunningSpawner pins that a task
// child's completion rides the same guaranteed report as a teammate's: the
// spawner running a turn is told in-turn, with the child's last message, and
// the notice names it a background task rather than a teammate. The mark-on-
// drain ordering is the teammate report's: unreported until the step drains.
func TestLeaveBoardReportsABackgroundTaskToARunningSpawner(t *testing.T) {
	srv := boardTestServer(t)
	srv.store = session.NewStore(srv.db)
	srv.loopRunner = &agent.LoopRunner{}
	registerBoardAgent(t, srv, board.Handle{
		Handle: "build-x", SessionID: "s1", Role: "build",
		Kind: board.KindSession, Status: board.StatusRunning,
	})
	worker := registerBoardAgent(t, srv, board.Handle{
		Handle: "task-1a2b", SessionID: "s2", Role: "subagent",
		Kind: board.KindTask, SpawnedBy: "build-x", Status: board.StatusRunning,
	})
	parentLC := registerBoardLoop(t, srv, "s1")
	id := session.SessionID(worker.SessionID)
	createBoardSession(t, srv, id)
	createAssistantMessage(t, srv, id, "the middleware is at line 40")

	srv.leaveBoard(id)

	cur, err := srv.board.Get(context.Background(), "task-1a2b")
	if err != nil {
		t.Fatalf("get task child: %v", err)
	}
	if cur.Status != board.StatusDone {
		t.Errorf("task child status = %q, want %q", cur.Status, board.StatusDone)
	}
	if !cur.Unreported() {
		t.Error("a completion was stamped reported before the spawner's loop drained its notice")
	}

	notice := waitNotice(t, parentLC)
	if !strings.Contains(notice.Text, "Background task") {
		t.Errorf("notice does not name the child a background task: %q", notice.Text)
	}
	if !strings.Contains(notice.Text, "the middleware is at line 40") ||
		!strings.Contains(notice.Text, "/session/s2") {
		t.Errorf("notice does not carry the child's answer and session: %q", notice.Text)
	}
	srv.loopRunner.BoardNoticesDelivered(parentLC, "s1", []agent.Notice{notice})
	cur, err = srv.board.Get(context.Background(), "task-1a2b")
	if err != nil {
		t.Fatalf("re-get task child: %v", err)
	}
	if cur.Unreported() {
		t.Error("a report whose notice drained into its spawner's step was left unreported")
	}

	// Ending the loop again is not a second piece of news.
	srv.leaveBoard(id)
	if parentLC.HasPendingNotice() {
		t.Error("the same completion was pushed twice")
	}
}

// TestStepBudgetForATaskChildMatchesTheSyncSubAgent pins the budget: a
// background task child runs exactly what its synchronous sibling runs —
// DefaultMaxSteps with auto-resume off — not the runner's turn budget and not
// a teammate's uncapped one.
func TestStepBudgetForATaskChildMatchesTheSyncSubAgent(t *testing.T) {
	srv := boardTestServer(t)
	registerBoardAgent(t, srv, board.Handle{
		Handle: "build-x", SessionID: "s1", Role: "build",
		Kind: board.KindSession, Status: board.StatusRunning,
	})
	registerBoardAgent(t, srv, board.Handle{
		Handle: "task-1a2b", SessionID: "s2", Role: "subagent",
		Kind: board.KindTask, SpawnedBy: "build-x", Status: board.StatusRunning,
	})

	steps, resumes := srv.stepBudgetFor("s2")
	if steps != agent.DefaultMaxSteps || resumes != 0 {
		t.Fatalf("task budget = (%d, %d), want (%d, 0) like the synchronous sub-agent", steps, resumes, agent.DefaultMaxSteps)
	}
}

// TestStartSessionLoopSupersedeKeepsTeammates pins what a plain second start
// is — a prompt arriving while the session already runs — a supersede of the
// old loop and nothing more: the old loop's cancel fires, and a teammate the
// session spawned keeps its row and its loop. The cascade to teammates belongs
// to the explicit abort, delete and hosted-worker cancel paths only.
func TestStartSessionLoopSupersedeKeepsTeammates(t *testing.T) {
	srv := boardTestServer(t)
	srv.store = session.NewStore(srv.db)
	srv.mu.Lock()
	srv.running = map[session.SessionID]context.CancelFunc{}
	srv.runningToken = map[session.SessionID]uint64{}
	srv.runningDone = map[session.SessionID]chan struct{}{}
	srv.loopControls = map[session.SessionID]*agent.LoopControl{}
	srv.mu.Unlock()
	// The registry is empty, so the new parent loop exits on its first resolve
	// instead of reaching a model. What this test watches is the supersede.
	srv.loopRunner = &agent.LoopRunner{Store: srv.store, Bus: srv.bus, Registry: srv.registry}

	superceded := atomic.Bool{}
	childCancelled := atomic.Bool{}
	parentID := session.SessionID("ses_supersede")
	childID := session.SessionID("ses_teammate_child")

	// The parent already has a live loop, the way a second prompt finds it:
	// a previous cancel func whose call the test records.
	srv.mu.Lock()
	srv.running[parentID] = func() { superceded.Store(true) }
	srv.runningToken[parentID] = 1000
	srv.runningDone[parentID] = make(chan struct{})
	srv.mu.Unlock()
	registerBoardAgent(t, srv, board.Handle{
		Handle: "build-x", SessionID: string(parentID), Role: "build",
		Kind: board.KindSession, Status: board.StatusRunning,
	})

	// A live teammate child under that parent: its loop registry entry and its
	// roster row, both of which a teardown would touch.
	registerBoardLoop(t, srv, childID)
	srv.mu.Lock()
	srv.running[childID] = func() { childCancelled.Store(true) }
	srv.runningToken[childID] = 1001
	srv.runningDone[childID] = make(chan struct{})
	srv.mu.Unlock()
	registerBoardAgent(t, srv, board.Handle{
		Handle: "worker", SessionID: string(childID), Role: "build",
		Kind: board.KindTeammate, SpawnedBy: "build-x", Status: board.StatusRunning,
	})

	srv.startSessionLoop(parentID, "build", 0, 0, "")

	if !superceded.Load() {
		t.Error("the previous loop's cancel was not called, so this was a plain start, not a supersede")
	}
	if childCancelled.Load() {
		t.Error("a plain second start cancelled a live teammate's loop")
	}
	worker, err := srv.board.Get(context.Background(), "worker")
	if err != nil {
		t.Fatalf("get worker: %v", err)
	}
	if worker.Status != board.StatusRunning {
		t.Fatalf("teammate row = %q after the supersede, want %q — it was stopped with the parent's prompt", worker.Status, board.StatusRunning)
	}

	// Let the new parent loop exit before the temp DBs are closed under it.
	deadline := time.Now().Add(10 * time.Second)
	for srv.sessionRunning(parentID) {
		if time.Now().After(deadline) {
			t.Fatal("the superseding loop did not exit")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestMintFreeHandleRetriesPastCollisions pins the roll: a minted name that
// lands on a row somebody already holds is re-rolled and claimed, and the row
// it first collided with is left untouched.
func TestMintFreeHandleRetriesPastCollisions(t *testing.T) {
	srv := boardTestServer(t)
	registerBoardAgent(t, srv, board.Handle{
		Handle: "teammate-aaaa", SessionID: "s_taken", Role: "build",
		Kind: board.KindTeammate, Status: board.StatusRunning,
	})
	rolls := 0
	h, err := srv.mintFreeHandle(context.Background(), board.Handle{
		Role: "build", Kind: board.KindTeammate,
		SpawnedBy: "build-x", Status: board.StatusRunning,
	}, 3, func() string {
		rolls++
		if rolls == 1 {
			return "teammate-aaaa"
		}
		return "teammate-bbbb"
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if rolls != 2 || h.Handle != "teammate-bbbb" {
		t.Fatalf("minted %q after %d rolls, want teammate-bbbb after a collision and one retry", h.Handle, rolls)
	}
	claimed, err := srv.board.Get(context.Background(), "teammate-bbbb")
	if err != nil || claimed.SpawnedBy != "build-x" || claimed.Kind != board.KindTeammate {
		t.Fatalf("claimed row = %+v (err %v), want the minted row on the board", claimed, err)
	}
	taken, err := srv.board.Get(context.Background(), "teammate-aaaa")
	if err != nil || taken.SessionID != "s_taken" {
		t.Fatalf("the collided row = %+v (err %v), want it untouched under s_taken", taken, err)
	}
}

// TestMintFreeHandleExhaustsItsRolls pins the other end: when every roll
// collides the caller is told so, with the attempts the mint burned.
func TestMintFreeHandleExhaustsItsRolls(t *testing.T) {
	srv := boardTestServer(t)
	registerBoardAgent(t, srv, board.Handle{
		Handle: "teammate-aaaa", SessionID: "s_taken", Role: "build",
		Kind: board.KindTeammate, Status: board.StatusRunning,
	})
	_, err := srv.mintFreeHandle(context.Background(), board.Handle{
		Role: "build", Kind: board.KindTeammate, Status: board.StatusRunning,
	}, 3, func() string { return "teammate-aaaa" })
	if err == nil {
		t.Fatal("mint: want an error once every roll collides")
	}
	if !strings.Contains(err.Error(), "3 attempts") {
		t.Errorf("error = %v, want it to name the exhausted attempts", err)
	}
	taken, err := srv.board.Get(context.Background(), "teammate-aaaa")
	if err != nil || taken.SessionID != "s_taken" {
		t.Fatalf("the collided row = %+v (err %v), want it untouched under s_taken", taken, err)
	}
}

// TestSpawnTeammateRefusesANameSomebodyHolds pins the human-typed spawn's
// refusal, and the row's safety behind it: asking for a name the board already
// holds is answered with the pick-another notice, and the row the other
// session is running under keeps its session and status — a spawn never
// overwrites a live row.
func TestSpawnTeammateRefusesANameSomebodyHolds(t *testing.T) {
	srv := boardTestServer(t)
	ctx := context.Background()
	parent := &session.Session{
		ID: session.NewSessionID(), ProjectID: srv.dir, Directory: srv.dir,
		SessionType: "build", CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}
	parentHandle, err := srv.joinBoard(ctx, parent.ID, "build")
	if err != nil {
		t.Fatalf("join parent: %v", err)
	}
	registerBoardAgent(t, srv, board.Handle{
		Handle: "worker", SessionID: "s_other", Role: "build",
		Kind: board.KindTeammate, Status: board.StatusRunning,
	})

	res, err := srv.spawnTeammate(ctx, parent, parentHandle, tool.TeammateRequest{
		Op: "spawn", Name: "worker", Prompt: "look here",
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	want := fmt.Sprintf("The handle %q is already on the board — pick another name, or wait for it to finish.", "worker")
	if res.Notice != want {
		t.Fatalf("refusal notice = %q, want %q", res.Notice, want)
	}
	row, err := srv.board.Get(ctx, "worker")
	if err != nil || row.SessionID != "s_other" || row.Status != board.StatusRunning {
		t.Fatalf("the taken row = %+v (err %v), want it untouched under s_other", row, err)
	}
}

// TestWaitTeammateReturnsPromptlyWhenTheTurnIsCancelled pins the abort-side of
// the wait: the parent turn's context is what an abort, a superseding prompt or
// a session delete cancels, and the wait must break on that and hand back the
// clean partial answer instead of sitting out the full timeout.
func TestWaitTeammateReturnsPromptlyWhenTheTurnIsCancelled(t *testing.T) {
	srv := boardTestServer(t)
	parent := registerBoardAgent(t, srv, board.Handle{
		Handle: "build-x", SessionID: "s1", Role: "build",
		Kind: board.KindSession, Status: board.StatusRunning,
	})
	registerBoardAgent(t, srv, board.Handle{
		Handle: "worker", SessionID: "s2", Role: "build",
		Kind: board.KindTeammate, SpawnedBy: "build-x", Status: board.StatusRunning,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	res, err := srv.waitTeammate(ctx, parent, tool.TeammateRequest{
		Op: "wait", Handles: []string{"worker"}, Timeout: boardWaitMaxSeconds,
	})
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("a cancelled turn's wait took %s, want a prompt break well under the %s timeout", took, boardWaitMaxSeconds*time.Second)
	}
	if !strings.Contains(res.Notice, "Still running") {
		t.Fatalf("wait notice = %q, want the still-running partial answer", res.Notice)
	}
}

// TestWaitTeammateKeepsWaitingWhenOnlyItsOpContextExpires pins the other edge:
// the tool call's own deadline expiring is not a reason to stop waiting — the
// wait runs on its own context well past that bound, and only a cancellation
// of the parent turn may break it early.
func TestWaitTeammateKeepsWaitingWhenOnlyItsOpContextExpires(t *testing.T) {
	srv := boardTestServer(t)
	parent := registerBoardAgent(t, srv, board.Handle{
		Handle: "build-x", SessionID: "s1", Role: "build",
		Kind: board.KindSession, Status: board.StatusRunning,
	})
	registerBoardAgent(t, srv, board.Handle{
		Handle: "worker", SessionID: "s2", Role: "build",
		Kind: board.KindTeammate, SpawnedBy: "build-x", Status: board.StatusRunning,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	res, err := srv.waitTeammate(ctx, parent, tool.TeammateRequest{
		Op: "wait", Handles: []string{"worker"}, Timeout: 2,
	})
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if took := time.Since(start); took < 2*time.Second {
		t.Fatalf("an expired op context cut the wait short at %s, want it to run out its full d (2s)", took)
	}
	if !strings.Contains(res.Notice, "Still running") {
		t.Fatalf("wait notice = %q, want the still-running partial answer", res.Notice)
	}
}
