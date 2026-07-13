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

// hangMockProvider simulates a provider whose first stream stalls (connects but
// emits nothing — the free-tier "0 parts, stuck" case) and whose second stream
// responds normally. It records the System prompt of each call so the test can
// verify guidance was injected on the resumed call.
type hangMockProvider struct {
	mu      sync.Mutex
	calls   int
	systems [][]string
	entered chan int // signals the call number each time StreamChat is entered
}

func (m *hangMockProvider) ID() string { return "mock" }
func (m *hangMockProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", ProviderID: "mock"}}
}

func (m *hangMockProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	m.mu.Lock()
	m.calls++
	call := m.calls
	m.systems = append(m.systems, req.System)
	m.mu.Unlock()

	ch := make(chan provider.StreamEvent)
	go func() {
		defer close(ch)
		m.entered <- call
		if call == 1 {
			// Stall until the context is cancelled (guidance "cancel current work").
			<-ctx.Done()
			return
		}
		// Resumed call: emit a token and a normal finish.
		fr := "stop"
		select {
		case ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "resumed output"}:
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

// TestRunLoop_GuidanceCancelsAndResumes drives the full agent loop against a
// provider whose first stream stalls, then injects mid-loop guidance with
// stream cancellation (exactly what handleGuidance does) and asserts the loop
// (1) unblocks the stalled stream, (2) resumes, and (3) injects the guidance
// into the resumed request. This reproduces the reported "guidance queued but
// nothing happens" hang end-to-end.
func TestRunLoop_GuidanceCancelsAndResumes(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	// Avoid the image-support probe (it would call StreamChat) by pre-seeding the
	// capability record.
	if err := session.SetModelCapability(database, &session.ModelCapability{ModelID: "mock-model", SupportsImages: false, ProbedAt: session.Now()}); err != nil {
		t.Fatalf("set capability: %v", err)
	}

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	mock := &hangMockProvider{entered: make(chan int, 8)}
	reg.Register(mock)

	lr := &LoopRunner{
		Store:    store,
		Bus:      bus.New(64),
		Registry: reg,
		Tools:    tool.NewRegistry(), // empty → no tools offered
		Dir:      t.TempDir(),
		MaxSteps: 20,
	}

	sess := &session.Session{
		ID:          session.NewSessionID(),
		ProjectID:   "p",
		Directory:   t.TempDir(),
		Title:       "t",
		Model:       "mock-model",
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
	textData, _ := json.Marshal(session.TextPartData{Text: "do the initial task"})
	if err := store.CreatePart(&session.Part{ID: session.NewPartID(), MessageID: userMsg.ID, SessionID: sess.ID, Type: session.PartText, Data: textData, CreatedAt: session.Now(), UpdatedAt: session.Now()}); err != nil {
		t.Fatalf("create user part: %v", err)
	}

	lc := NewLoopControl()
	baseCtx, cancelAll := context.WithCancel(context.Background())
	defer cancelAll()
	ctx := WithLoopControl(baseCtx, lc)

	done := make(chan error, 1)
	go func() { done <- lr.RunLoop(ctx, sess.ID, "build", 0, 0) }()

	// Wait until the loop is inside the stalled first stream.
	waitFor(t, mock.entered, 1, 3*time.Second, "loop never entered the first stream")

	// Simulate handleGuidance with "cancel current work": push then cancel.
	lc.PushGuidance("STOP the current approach and do Y instead")
	if !lc.CancelStream() {
		t.Fatal("CancelStream returned false — the stream cancel func was not registered when guidance arrived (the loop cannot be interrupted)")
	}

	// The loop MUST resume and enter a second stream. If it hangs, this fails.
	waitFor(t, mock.entered, 2, 5*time.Second, "loop did not resume after guidance cancellation — it is stuck")

	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("RunLoop returned error: %v", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunLoop did not complete after resuming")
	}

	// The resumed (2nd) call must carry the guidance in its system prompt.
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.systems) < 2 {
		t.Fatalf("expected at least 2 stream calls, got %d", len(mock.systems))
	}
	joined := ""
	for _, s := range mock.systems[1] {
		joined += s + "\n"
	}
	if !strings.Contains(joined, "STOP the current approach and do Y instead") {
		t.Errorf("resumed request did not include the guidance in its system prompt; got system entries: %v", mock.systems[1])
	}
}

func waitFor(t *testing.T, ch <-chan int, want int, d time.Duration, msg string) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case got := <-ch:
			if got == want {
				return
			}
		case <-deadline:
			t.Fatal(msg)
		}
	}
}

