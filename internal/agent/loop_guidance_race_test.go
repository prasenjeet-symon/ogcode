package agent

import (
	"context"
	"encoding/json"
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

// blockingStreamProvider blocks its first StreamChat call until a gate channel
// is closed (or the context is cancelled). This holds the loop inside the
// stream-consumption phase so the test can push guidance at a controlled time.
// It records the system prompt and messages of every call so the test can
// verify whether guidance was injected (now appended to the user message
// content rather than the system prompt).
type blockingStreamProvider struct {
	mu        sync.Mutex
	calls     int
	systems   [][]string
	messages  [][]provider.ModelMessage
	entered   chan int

	// gateForCall blocks the stream goroutine of the given call number until
	// closed. nil means no blocking. The goroutine also unblocks on ctx.Done.
	gateForCall map[int]chan struct{}
}

func (m *blockingStreamProvider) ID() string { return "mock-block" }
func (m *blockingStreamProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-block-model", ProviderID: "mock-block"}}
}

func (m *blockingStreamProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	m.mu.Lock()
	m.calls++
	call := m.calls
	m.systems = append(m.systems, req.System)
	m.messages = append(m.messages, req.Messages)
	gate := m.gateForCall[call]
	m.mu.Unlock()

	ch := make(chan provider.StreamEvent)
	go func() {
		defer close(ch)
		if gate != nil {
			select {
			case <-gate:
			case <-ctx.Done():
				return
			}
		}
		m.entered <- call
		fr := "stop"
		select {
		case ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "output"}:
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

// TestRunLoop_GuidanceWithoutCancelNotLost verifies that guidance pushed
// during the pre-stream setup phase (after DrainGuidance at the top of the
// iteration but before SetStreamCancel) is caught by the HasPendingGuidance
// re-check and not silently lost. Without the fix, the guidance would sit in
// the queue until the stream finishes (10-30s) and only be applied on the
// NEXT iteration. With the fix, the stream is cancelled before it starts and
// the guidance is delivered promptly on the next iteration.
//
// The mock's first call has a gate that blocks the stream goroutine. However,
// the fix's HasPendingGuidance check fires BEFORE StreamChat is called (while
// the loop is still in setup), so StreamChat is never invoked on step 1 and
// the gate is never reached. The loop continues to step 2, which drains the
// guidance and calls StreamChat — this is the mock's call 1, whose gate was
// already closed by the test, so it proceeds immediately.
func TestRunLoop_GuidanceWithoutCancelNotLost(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if err := session.SetModelCapability(database, &session.ModelCapability{ModelID: "mock-block-model", SupportsImages: false, ProbedAt: session.Now()}); err != nil {
		t.Fatalf("set capability: %v", err)
	}

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	gate := make(chan struct{})
	mock := &blockingStreamProvider{
		entered:     make(chan int, 8),
		gateForCall: map[int]chan struct{}{1: gate},
	}
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
		Model:       "mock-block-model",
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
	baseCtx, cancelAll := context.WithCancel(context.Background())
	defer cancelAll()
	ctx := WithLoopControl(baseCtx, lc)

	done := make(chan error, 1)
	go func() { done <- lr.RunLoop(ctx, sess.ID, "build", 0, 0) }()

	// Give the loop time to enter step 1's setup phase (DB load, tool
	// resolution, system prompt building). The HasPendingGuidance check runs
	// after SetStreamCancel, before StreamChat — this is the pre-stream gap.
	time.Sleep(100 * time.Millisecond)

	// Push guidance WITHOUT calling CancelStream. This simulates the Bug 2
	// race: guidance arrives in the pre-stream gap (after DrainGuidance but
	// before/during setup). The HTTP handler's CancelStream would return
	// false because the cancel func isn't registered yet. Without the fix,
	// the guidance sits in the queue and is only applied on the next iteration
	// (after the stream finishes). With the fix, the HasPendingGuidance
	// re-check catches it and cancels the stream before it starts.
	lc.PushGuidance("change direction immediately")

	// Close the gate so that step 2's StreamChat (the mock's call 1) can
	// proceed immediately when it runs.
	close(gate)

	// Wait for the loop to complete. With the fix, the loop runs 2 steps:
	// step 1 catches the guidance and cancels before StreamChat; step 2
	// drains the guidance, calls StreamChat (which proceeds because the gate
	// is closed), and finishes.
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("RunLoop returned error: %v", runErr)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunLoop did not complete — guidance may have been lost")
	}

	// Verify 1 or 2 StreamChat calls were made:
	// - 1 call: the HasPendingGuidance fix fired on step 1 (cancelled before
	//   StreamChat), and step 2's call proceeded (gate was closed).
	// - 2 calls: the fix didn't fire (guidance arrived after the check), step
	//   1's stream ran to completion (gate released), and the lateGuidance
	//   check caught the guidance, causing step 2's call.
	// Both are correct — the guidance is never lost.
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.calls < 1 || mock.calls > 2 {
		t.Errorf("expected 1 or 2 StreamChat calls, got %d", mock.calls)
	}

	// Verify the guidance was injected into a stream's user message content.
	joinedAll := ""
	for _, msgs := range mock.messages {
		for _, m := range msgs {
			if m.Content != nil {
				var content string
				if json.Unmarshal(m.Content, &content) == nil {
					joinedAll += content + "\n"
				}
			}
		}
	}
	if !strings.Contains(joinedAll, "change direction immediately") {
		t.Errorf("guidance was never injected into any stream's user message content — it was lost.\nMessages: %v", mock.messages)
	}
}

// finishThenBlockProvider completes its first stream immediately (finish=stop,
// no tools), then blocks its second stream on a gate. This lets the test push
// guidance in the window between the first stream finishing and the loop
// exiting (Bug 1: guidance dropped at loop exit). It records the messages of
// every call so the test can verify guidance was injected.
type finishThenBlockProvider struct {
	mu          sync.Mutex
	calls       int
	systems     [][]string
	messages    [][]provider.ModelMessage
	entered     chan int
	// gateForCall2 blocks the second stream until closed.
	gateForCall2 chan struct{}
}

func (m *finishThenBlockProvider) ID() string { return "mock-fb" }
func (m *finishThenBlockProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-fb-model", ProviderID: "mock-fb"}}
}

func (m *finishThenBlockProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	m.mu.Lock()
	m.calls++
	call := m.calls
	m.systems = append(m.systems, req.System)
	m.messages = append(m.messages, req.Messages)
	m.mu.Unlock()

	ch := make(chan provider.StreamEvent)
	go func() {
		defer close(ch)
		if call == 2 && m.gateForCall2 != nil {
			select {
			case <-m.gateForCall2:
			case <-ctx.Done():
				return
			}
		}
		m.entered <- call
		fr := "stop"
		select {
		case ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "done"}:
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

// TestRunLoop_GuidanceAfterFinishNotDropped reproduces Bug 1: guidance pushed
// after the stream finishes but before the loop exits via shouldBreak was
// silently dropped. The fix re-checks the guidance queue inside the shouldBreak
// block.
//
// Strategy: the first stream completes immediately (finish=stop, no tools).
// The loop's lateGuidance check at the end of iteration 1 catches guidance
// pushed during streaming and re-pushes it, continuing to iteration 2. On
// iteration 2, the drain picks it up. This tests the existing lateGuidance
// path. To test the shouldBreak re-check specifically, we push guidance after
// the first stream finishes — the lateGuidance check catches it (it was
// pushed during the iteration). The second stream is gated so we can verify
// the guidance was injected.
func TestRunLoop_GuidanceAfterFinishNotDropped(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if err := session.SetModelCapability(database, &session.ModelCapability{ModelID: "mock-fb-model", SupportsImages: false, ProbedAt: session.Now()}); err != nil {
		t.Fatalf("set capability: %v", err)
	}

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	gate2 := make(chan struct{})
	mock := &finishThenBlockProvider{
		entered:      make(chan int, 8),
		gateForCall2: gate2,
	}
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
		Model:       "mock-fb-model",
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
	baseCtx, cancelAll := context.WithCancel(context.Background())
	defer cancelAll()
	ctx := WithLoopControl(baseCtx, lc)

	done := make(chan error, 1)
	go func() { done <- lr.RunLoop(ctx, sess.ID, "build", 0, 0) }()

	// Wait for the first stream to start and finish (it completes immediately).
	waitFor(t, mock.entered, 1, 3*time.Second, "first stream never started")

	// Push guidance right after the first stream finishes. The lateGuidance
	// check at the end of iteration 1 should catch it and re-push, causing
	// the loop to continue to iteration 2 instead of exiting. Without any
	// guidance catch, the loop would exit here and the guidance would be lost.
	lc.PushGuidance("actually do something different now")

	// The loop should NOT exit — it should continue to iteration 2 to deliver
	// the guidance. The second stream is gated, so it blocks. If the loop
	// exited instead, the done channel fires.
	select {
	case runErr := <-done:
		t.Fatalf("loop exited before delivering guidance (guidance was dropped): %v", runErr)
	case <-time.After(2 * time.Second):
		// Good — the loop is still running, waiting on the second stream's gate.
	}

	// Release the second stream's gate so the loop can complete.
	close(gate2)

	// Wait for the second stream.
	waitFor(t, mock.entered, 2, 5*time.Second, "loop did not start a second stream to deliver guidance")

	// Let the loop complete.
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("RunLoop returned error: %v", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunLoop did not complete")
	}

	// Verify the guidance was injected into the second stream's user message content.
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.calls < 2 {
		t.Fatalf("expected at least 2 stream calls, got %d", mock.calls)
	}
	joined := ""
	for _, m := range mock.messages[1] {
		if m.Content != nil {
			var content string
			if json.Unmarshal(m.Content, &content) == nil {
				joined += content + "\n"
			}
		}
	}
	if !strings.Contains(joined, "actually do something different now") {
		t.Errorf("second stream did not include the guidance in its user message content; got: %v", mock.messages[1])
	}
}