package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// textOnlyMockProvider streams partial text on the first call (which gets
// cancelled by mid-loop guidance) and a complete text response on the second
// call. No tool calls are emitted — this exercises the text-only cancel path
// where the orphan assistant message must be deleted to avoid consecutive
// assistant messages on the next prompt.
type textOnlyMockProvider struct {
	calls   int
	entered chan int
}

func (m *textOnlyMockProvider) ID() string { return "mock-text" }
func (m *textOnlyMockProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-text-model", ProviderID: "mock-text"}}
}

func (m *textOnlyMockProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	m.calls++
	call := m.calls
	ch := make(chan provider.StreamEvent)
	go func() {
		defer close(ch)
		m.entered <- call
		if call == 1 {
			// Stream some partial text, then stall until cancelled by guidance.
			select {
			case ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "partial"}:
			case <-ctx.Done():
				return
			}
			<-ctx.Done()
			return
		}
		// Resumed call: emit a token and a normal finish.
		fr := "stop"
		select {
		case ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "final output"}:
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

// TestRunLoop_GuidanceCancelsTextOnlyStream_DeletesOrphan reproduces the bug
// where mid-loop guidance cancels a stream that produced text only (no tool
// calls). Without the fix, the partial assistant message stays in the DB with
// finish="stop", and the next prompt produces two consecutive assistant role
// messages — which the Anthropic and OpenAI APIs reject with a 400. The fix
// deletes the orphan assistant message so the history stays valid.
func TestRunLoop_GuidanceCancelsTextOnlyStream_DeletesOrphan(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if err := session.SetModelCapability(database, &session.ModelCapability{ModelID: "mock-text-model", SupportsImages: false, ProbedAt: session.Now()}); err != nil {
		t.Fatalf("set capability: %v", err)
	}

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	mock := &textOnlyMockProvider{entered: make(chan int, 8)}
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
		Model:       "mock-text-model",
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

	// Simulate handleGuidance with stream cancellation.
	lc.PushGuidance("STOP and do something else instead")
	if !lc.CancelStream() {
		t.Fatal("CancelStream returned false — stream cancel func was not registered")
	}

	// Wait for the resumed second stream.
	waitFor(t, mock.entered, 2, 5*time.Second, "loop did not resume after guidance cancellation")

	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("RunLoop returned error: %v", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunLoop did not complete after resuming")
	}

	// The DB history must NOT contain the partial text-only assistant message
	// from the cancelled first stream. It should be deleted so the next prompt
	// doesn't produce two consecutive assistant messages.
	msgs, err := store.GetMessages(sess.ID, "", 100)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}

	// Expected: [user, assistant(final)]. The partial assistant message from
	// the cancelled stream must be gone.
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (user + final assistant), got %d:", len(msgs))
		for i, m := range msgs {
			t.Logf("  [%d] role=%s finish=%v", i, m.Info.Role, m.Info.Finish)
		}
	}

	// The only assistant message should be the final "final output" one.
	assistantTexts := 0
	for _, m := range msgs {
		if m.Info.Role != session.RoleAssistant {
			continue
		}
		for _, p := range m.Parts {
			if p.Type != session.PartText {
				continue
			}
			var data session.TextPartData
			if json.Unmarshal(p.Data, &data) == nil {
				if data.Text == "partial" {
					t.Errorf("partial text from cancelled stream should have been deleted, found: %q", data.Text)
				}
				assistantTexts++
			}
		}
	}
	if assistantTexts != 1 {
		t.Errorf("expected exactly 1 assistant text message, got %d", assistantTexts)
	}

	// The converted provider messages must alternate user → assistant with no
	// consecutive assistant messages.
	model := convertMessages(msgs, false, "claude-opus-4-6")
	for i := 1; i < len(model); i++ {
		if model[i].Role == "assistant" && model[i-1].Role == "assistant" {
			t.Errorf("consecutive assistant messages at positions %d-%d — the API would reject this with a 400", i-1, i)
		}
	}
}
