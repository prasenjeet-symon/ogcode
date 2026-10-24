package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// joinedContent concatenates the string contents of model messages, the way
// the guidance race tests read back what a request carried. Tool-call entries
// and non-string payloads are skipped — only user/text bubbles matter here.
func joinedContent(msgs []provider.ModelMessage) string {
	var b strings.Builder
	for _, m := range msgs {
		if m.Content == nil {
			continue
		}
		var s string
		if json.Unmarshal(m.Content, &s) == nil {
			b.WriteString(s)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// gapNoticesProvider streams a plain text + stop completion on every call and
// lets the test hook into PlacesGuidance, which RunLoop consults between the
// notice take and the pre-stream gap re-check — a deterministic observation
// point inside the race window that is otherwise far too narrow to pin.
type gapNoticesProvider struct {
	mu       sync.Mutex
	calls    int
	messages [][]provider.ModelMessage
	events   []string
	// onPlacesGuidance runs each time the loop asks whether this provider
	// positions guidance itself; the test pushes a notice here to land inside
	// the gap between take and re-check.
	onPlacesGuidance func()
}

func (m *gapNoticesProvider) ID() string { return "mock-gap" }
func (m *gapNoticesProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-gap-model", ProviderID: "mock-gap"}}
}

// PlacesGuidance implements provider.GuidancePlacer with false: the loop
// consults it at the injection site and falls back to appending the notices
// block at the tail of Messages exactly as it would for a provider without
// the interface — so the hook runs, but the wire shape is unchanged.
func (m *gapNoticesProvider) PlacesGuidance() bool {
	if m.onPlacesGuidance != nil {
		m.onPlacesGuidance()
	}
	return false
}

func (m *gapNoticesProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	m.mu.Lock()
	m.calls++
	call := m.calls
	m.messages = append(m.messages, req.Messages)
	m.events = append(m.events, fmt.Sprintf("dispatch:%d", call))
	m.mu.Unlock()

	ch := make(chan provider.StreamEvent)
	go func() {
		defer close(ch)
		fr := "stop"
		select {
		case ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "working"}:
		case <-ctx.Done():
			return
		}
		select {
		case ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}:
		case <-ctx.Done():
		}
	}()
	return ch, nil
}

// noteEvent appends to the shared event log from a mark closure, so the test
// can assert the marks ran after the dispatch, never before.
func (m *gapNoticesProvider) noteEvent(ev string) {
	m.mu.Lock()
	m.events = append(m.events, ev)
	m.mu.Unlock()
}

// TestRunLoop_NoticeGapRestartRidesTheNextRequestFull pins the loop end of the
// restore contract: notice A is drained and taken into step 1's request tail,
// a second report B is pushed while the step sits in the pre-stream gap, the
// step restarts — and the restarted request must carry A's FULL text (not a
// digest claiming it was already delivered), with both board rows stamped
// exactly once, only by the request that actually dispatched.
func TestRunLoop_NoticeGapRestartRidesTheNextRequestFull(t *testing.T) {
	const (
		noticeA    = "Teammate dev-1 is done — /session/gap-a\n\nIts full report:\n\nrebuilt the notice restore path detail"
		noticeAHit = "rebuilt the notice restore path detail"
		noticeB    = "Teammate dev-2 is done — /session/gap-b\n\nIts full report:\n\nwired the stamp at dispatch detail"
		noticeBHit = "wired the stamp at dispatch detail"
	)

	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if err := session.SetModelCapability(database, &session.ModelCapability{ModelID: "mock-gap-model", SupportsImages: false, ProbedAt: session.Now()}); err != nil {
		t.Fatalf("set capability: %v", err)
	}

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	mock := &gapNoticesProvider{}
	reg.Register(mock)

	lr := &LoopRunner{
		Store:    store,
		Bus:      bus.New(64),
		Registry: reg,
		Tools:    tool.NewRegistry(),
		Dir:      t.TempDir(),
		MaxSteps: 20,
	}

	sess := &session.Session{
		ID:          session.NewSessionID(),
		ProjectID:   "p",
		Directory:   t.TempDir(),
		Title:       "t",
		Model:       "mock-gap-model",
		SessionType: "build",
		CreatedAt:   session.Now(),
		UpdatedAt:   session.Now(),
	}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	userMsg := &session.MessageInfo{ID: session.NewMessageID(), SessionID: sess.ID, Role: session.RoleUser, CreatedAt: session.Now()}
	if err := store.CreateMessage(userMsg); err != nil {
		t.Fatalf("create user msg: %v", err)
	}
	textData, _ := json.Marshal(session.TextPartData{Text: "do the task"})
	if err := store.CreatePart(&session.Part{ID: session.NewPartID(), MessageID: userMsg.ID, SessionID: sess.ID, Type: session.PartText, Data: textData, CreatedAt: session.Now(), UpdatedAt: session.Now()}); err != nil {
		t.Fatalf("create user part: %v", err)
	}

	lc := NewLoopControl()
	lr.TrackBoardReport(lc, sess.ID, BoardReport{
		Text: noticeA,
		Mark: func() error { mock.noteEvent("mark:A"); return nil },
	})
	lr.TrackBoardReport(lc, sess.ID, BoardReport{
		Text: noticeB,
		Mark: func() error { mock.noteEvent("mark:B"); return nil },
	})

	// A is queued before the turn starts, so step 1 drains and takes it.
	lc.PushNotice(Notice{Text: noticeA, Cause: "teammate"})

	// B is pushed when the loop consults PlacesGuidance — strictly after the
	// step's take, strictly before the pre-stream gap re-check — so the
	// restart fires while A sits taken-but-unsent.
	var pushBOnce sync.Once
	mock.onPlacesGuidance = func() {
		pushBOnce.Do(func() {
			lc.PushNotice(Notice{Text: noticeB, Cause: "teammate"})
		})
	}

	baseCtx, cancelAll := context.WithCancel(context.Background())
	defer cancelAll()
	ctx := WithLoopControl(baseCtx, lc)

	done := make(chan error, 1)
	go func() { done <- lr.RunLoop(ctx, sess.ID, "build", 0, 0) }()

	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("RunLoop returned error: %v", runErr)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunLoop did not complete")
	}

	// Exactly ONE StreamChat call: step 1 restarted in the gap before
	// dispatching, and the restarted step dispatched both notices together.
	mock.mu.Lock()
	calls, events, messages := mock.calls, append([]string{}, mock.events...), len(mock.messages)
	mock.mu.Unlock()
	if calls != 1 {
		t.Fatalf("StreamChat called %d times, want 1 (gap restart must not have dispatched step 1's request)", calls)
	}
	if messages != 1 {
		t.Fatalf("recorded %d requests, want 1", messages)
	}

	// The single request carries both notices as FULL text — not the digest
	// block, which would claim text was "already delivered above" that no
	// request ever carried — restored notice first, matching queue order.
	joined := joinedContent(mock.messages[0])
	if strings.Contains(joined, noticesDigestLabel) {
		t.Errorf("restarted request carries the already-delivered digest block:\n%s", joined)
	}
	if !strings.Contains(joined, noticeAHit) {
		t.Errorf("restarted request lost the restored notice's full text:\n%s", joined)
	}
	if !strings.Contains(joined, noticeBHit) {
		t.Errorf("restarted request lost the new report's full text:\n%s", joined)
	}
	if a, b := strings.Index(joined, noticeAHit), strings.Index(joined, noticeBHit); a > b {
		t.Errorf("restarted request ordered B before the restored A (a=%d, b=%d):\n%s", a, b, joined)
	}

	// Both marks ran exactly once, and only AFTER the dispatch — the old
	// stamp-at-drain order put mark:A before any dispatch existed.
	want := []string{"dispatch:1", "mark:A", "mark:B"}
	if len(events) != len(want) {
		t.Fatalf("event log = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("event log[%d] = %q, want %q (full log %v)", i, events[i], want[i], events)
		}
	}
}

// failThenSucceedProvider fails its first dispatch (StreamChat returns a
// non-transient error, so the loop neither retries nor sleeps on it) and
// streams a completion on every later call. It records the dispatch events
// and the messages so the test can pin the mark-at-dispatch ordering.
type failThenSucceedProvider struct {
	mu       sync.Mutex
	calls    int
	messages [][]provider.ModelMessage
	events   []string
}

func (m *failThenSucceedProvider) ID() string { return "mock-fail-then-ok" }
func (m *failThenSucceedProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-fail-then-ok-model", ProviderID: "mock-fail-then-ok"}}
}

func (m *failThenSucceedProvider) noteEvent(ev string) {
	m.mu.Lock()
	m.events = append(m.events, ev)
	m.mu.Unlock()
}

func (m *failThenSucceedProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	m.mu.Lock()
	m.calls++
	call := m.calls
	m.events = append(m.events, fmt.Sprintf("dispatch:%d", call))
	m.mu.Unlock()
	if call == 1 {
		// Non-transient on purpose: no retry, no backoff sleep in the test.
		return nil, errors.New("quota exhausted")
	}
	m.mu.Lock()
	m.messages = append(m.messages, req.Messages)
	m.mu.Unlock()

	ch := make(chan provider.StreamEvent)
	go func() {
		defer close(ch)
		fr := "stop"
		select {
		case ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "resumed"}:
		case <-ctx.Done():
			return
		}
		select {
		case ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}:
		case <-ctx.Done():
		}
	}()
	return ch, nil
}

// TestLoopBoardReportsStampOnlyAtDispatch pins the delivery window end to end:
// a turn that drains a notice and then fails BEFORE the dispatch (all retry
// attempts, here the first — the error is non-transient) leaves the board row
// UNMARKED, and the catch-up re-delivery on a later turn marks it exactly
// once, with the mark strictly after the request that carries the text
// dispatched.
func TestLoopBoardReportsStampOnlyAtDispatch(t *testing.T) {
	const noticeT = "[Teammate dev-1] done — its full report below\n\ndispatch-stamp detail marker"

	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if err := session.SetModelCapability(database, &session.ModelCapability{ModelID: "mock-fail-then-ok-model", SupportsImages: false, ProbedAt: session.Now()}); err != nil {
		t.Fatalf("set capability: %v", err)
	}

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	mock := &failThenSucceedProvider{}
	reg.Register(mock)

	lr := &LoopRunner{
		Store:    store,
		Bus:      bus.New(64),
		Registry: reg,
		Tools:    tool.NewRegistry(),
		Dir:      t.TempDir(),
		MaxSteps: 20,
	}

	sess := &session.Session{
		ID:          session.NewSessionID(),
		ProjectID:   "p",
		Directory:   t.TempDir(),
		Title:       "t",
		Model:       "mock-fail-then-ok-model",
		SessionType: "build",
		CreatedAt:   session.Now(),
		UpdatedAt:   session.Now(),
	}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	userMsg := &session.MessageInfo{ID: session.NewMessageID(), SessionID: sess.ID, Role: session.RoleUser, CreatedAt: session.Now()}
	if err := store.CreateMessage(userMsg); err != nil {
		t.Fatalf("create user msg: %v", err)
	}
	textData, _ := json.Marshal(session.TextPartData{Text: "do the task"})
	if err := store.CreatePart(&session.Part{ID: session.NewPartID(), MessageID: userMsg.ID, SessionID: sess.ID, Type: session.PartText, Data: textData, CreatedAt: session.Now(), UpdatedAt: session.Now()}); err != nil {
		t.Fatalf("create user part: %v", err)
	}

	// Turn 1: the report is tracked, its notice is pushed, the step drains
	// and takes it — and the dispatch fails before landing.
	lc1 := NewLoopControl()
	marked1 := 0
	lr.TrackBoardReport(lc1, sess.ID, BoardReport{
		Text: noticeT,
		Mark: func() error { marked1++; return nil },
	})
	lc1.PushNotice(Notice{Text: noticeT, Cause: "teammate"})
	ctx1 := WithLoopControl(context.Background(), lc1)

	if runErr := lr.RunLoop(ctx1, sess.ID, "build", 0, 0); runErr == nil {
		t.Fatal("turn 1 should have ended with the failed dispatch, got nil error")
	}
	if marked1 != 0 {
		t.Fatalf("a turn whose dispatch never landed marked the report %d times", marked1)
	}

	// Turn 2: the board's catch-up re-delivers the row as a fresh report in a
	// fresh turn — exactly what the server does. This time the dispatch
	// lands, and the row is stamped exactly once, after the dispatch.
	lc2 := NewLoopControl()
	marked2 := 0
	lr.TrackBoardReport(lc2, sess.ID, BoardReport{
		Text: noticeT,
		Mark: func() error {
			marked2++
			mock.noteEvent("mark")
			return nil
		},
	})
	lc2.PushNotice(Notice{Text: noticeT, Cause: "teammate"})
	ctx2 := WithLoopControl(context.Background(), lc2)

	if runErr := lr.RunLoop(ctx2, sess.ID, "build", 0, 0); runErr != nil {
		t.Fatalf("turn 2 returned error: %v", runErr)
	}
	if marked2 != 1 {
		t.Fatalf("the catching-up turn marked the report %d times, want exactly 1", marked2)
	}

	// The delivered request carries the notice in full (not as a headline or
	// the digest label), and the event order is dispatch → mark, never the
	// reverse.
	if len(mock.messages) == 0 {
		t.Fatal("turn 2 dispatched no request")
	}
	joined := joinedContent(mock.messages[len(mock.messages)-1])
	if !strings.Contains(joined, "dispatch-stamp detail marker") {
		t.Errorf("the delivered request lost the notice's full text:\n%s", joined)
	}
	if strings.Contains(joined, noticesDigestLabel) {
		t.Errorf("the delivered request carries the already-delivered digest block:\n%s", joined)
	}
	mock.mu.Lock()
	events := append([]string{}, mock.events...)
	mock.mu.Unlock()
	if len(events) != 3 {
		t.Fatalf("event log = %v, want [dispatch:1, dispatch:2, mark]", events)
	}
	for i, want := range []string{"dispatch:1", "dispatch:2", "mark"} {
		if events[i] != want {
			t.Fatalf("event log[%d] = %q, want %q (a mark ran before the dispatch that carries the text)", i, events[i], want)
		}
	}
}
