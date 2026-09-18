package agent

import (
	"context"
	"encoding/json"
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

// reactiveCapProvider scripts a turn that overflows the model's context window
// on every step after a warmup of successful tool rounds. Every overflow is
// the shape Ollama actually returns for a too-long prompt: a bare 400 with an
// empty body. A summarizer-shaped request (llmCompact's own) is answered with
// a short text summary and grants the next main attempt a success — the freed
// room the compaction bought.
type reactiveCapProvider struct {
	mu         sync.Mutex
	warmup     int // successful tool rounds before overflowing begins
	grants     int // successful main attempts owed after each compaction
	summarizer int // compaction (LLM summarizer) calls
	overflows  int // main attempts rejected with a context-length error
	served     int // successful main attempts
}

func (m *reactiveCapProvider) ID() string { return "mock" }
func (m *reactiveCapProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", ProviderID: "mock", ContextWindow: 130000}}
}

func toolCallStream(call int) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent, 8)
	go func() {
		defer close(ch)
		id := fmt.Sprintf("call_%d", call)
		ch <- provider.StreamEvent{Type: provider.EventToolCallStart, ToolCallID: id, ToolName: "grep"}
		ch <- provider.StreamEvent{Type: provider.EventToolCallDelta, ToolCallID: id, ToolInput: []byte(`{}`)}
		ch <- provider.StreamEvent{Type: provider.EventToolCallEnd, ToolCallID: id}
		fr := "tool_use"
		ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
	}()
	return ch
}

func (m *reactiveCapProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	m.mu.Lock()
	if len(req.System) > 0 && strings.Contains(req.System[0], "conversation summarizer") {
		m.summarizer++
		m.grants++
		m.mu.Unlock()
		ch := make(chan provider.StreamEvent, 4)
		go func() {
			defer close(ch)
			ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "compact summary"}
			fr := "stop"
			ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
		}()
		return ch, nil
	}
	if m.warmup > 0 {
		m.warmup--
		m.served++
		call := m.served
		m.mu.Unlock()
		return toolCallStream(call), nil
	}
	if m.grants > 0 {
		m.grants--
		m.served++
		call := m.served
		m.mu.Unlock()
		return toolCallStream(call), nil
	}
	m.overflows++
	m.mu.Unlock()
	return nil, fmt.Errorf("ollama API error 400: ")
}

// TestRunLoop_ReactiveCompactionIsBudgetedPerRun pins the run-wide cap: a turn
// that overflows again after every compaction must stop compacting after
// maxCompactions for the whole RunLoop and surface the provider's error — not
// reset the budget each step and compact its way through dozens of steps.
func TestRunLoop_ReactiveCompactionIsBudgetedPerRun(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := session.SetModelCapability(database, &session.ModelCapability{
		ModelID: "mock-model", SupportsImages: false, ProbedAt: session.Now(),
	}); err != nil {
		t.Fatalf("set capability: %v", err)
	}
	store := session.NewStore(database)
	reg := provider.NewRegistry()
	mock := &reactiveCapProvider{warmup: 7} // 7 tool rounds → 15 messages before the first overflow
	reg.Register(mock)

	tools := tool.NewRegistry()
	tools.Register(noopGrepTool{})

	lr := &LoopRunner{
		Store: store, Bus: bus.New(64), Registry: reg, Tools: tools,
		Dir: t.TempDir(), MaxSteps: 20,
	}
	sess := &session.Session{
		ID: session.NewSessionID(), ProjectID: "p", Directory: t.TempDir(),
		Title: "t", Model: "mock-model", SessionType: "build",
		CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	userMsg := &session.MessageInfo{ID: session.NewMessageID(), SessionID: sess.ID, Role: session.RoleUser, CreatedAt: session.Now()}
	if err := store.CreateMessage(userMsg); err != nil {
		t.Fatalf("create user msg: %v", err)
	}
	textData, _ := json.Marshal(session.TextPartData{Text: "work on the task"})
	if err := store.CreatePart(&session.Part{
		ID: session.NewPartID(), MessageID: userMsg.ID, SessionID: sess.ID,
		Type: session.PartText, Data: textData, CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}); err != nil {
		t.Fatalf("create user part: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- lr.RunLoop(context.Background(), sess.ID, "build", 0, 0) }()
	var runErr error
	select {
	case runErr = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("RunLoop did not complete in time")
	}

	mock.mu.Lock()
	summarizer, overflows, served := mock.summarizer, mock.overflows, mock.served
	mock.mu.Unlock()

	// With the cap budgeted per RUN: compactions fire on the first two
	// overflowing steps (5 requests' warmup + two overflow+retry pairs …), and
	// the third overflow has no budget left — the loop gives up and surfaces
	// the provider's error instead of compacting forever.
	if summarizer != maxCompactions {
		t.Errorf("summarizer calls = %d, want %d (the run-wide reactive budget)", summarizer, maxCompactions)
	}
	if overflows != maxCompactions+1 {
		t.Errorf("overflow rejections = %d, want %d (two absorbed, the third gives up)", overflows, maxCompactions+1)
	}
	if served != 9 { // 7 warmup rounds + 2 post-compaction retries
		t.Errorf("successful main calls = %d, want 9", served)
	}
	if runErr == nil || !strings.Contains(runErr.Error(), "stream chat") {
		t.Errorf("RunLoop error = %v, want the give-up stream error once the budget is spent", runErr)
	}
}
