package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// noopGrepTool stands in for the real "grep" tool (which the build agent is
// allowed to call) but does no I/O — it just returns a fixed result. Registering
// it under the "grep" ID lets a mock provider drive real multi-iteration tool
// rounds through RunLoop without touching the filesystem.
type noopGrepTool struct{}

func (noopGrepTool) ID() string          { return "grep" }
func (noopGrepTool) Description() string { return "noop grep (test)" }
func (noopGrepTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (noopGrepTool) Execute(ctx context.Context, args json.RawMessage, tctx tool.Context) (tool.Result, error) {
	return tool.Result{Title: "grep", Output: "match"}, nil
}

// toolRoundsProvider emits a tool call on the first `rounds` StreamChat calls and
// a final text answer afterward, recording the request messages of every call so
// a test can inspect the working set the loop built for each turn.
type toolRoundsProvider struct {
	mu       sync.Mutex
	rounds   int
	calls    int
	messages [][]provider.ModelMessage
}

func (m *toolRoundsProvider) ID() string { return "mock" }
func (m *toolRoundsProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", ProviderID: "mock"}}
}

func (m *toolRoundsProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	m.mu.Lock()
	m.calls++
	call := m.calls
	snapshot := make([]provider.ModelMessage, len(req.Messages))
	copy(snapshot, req.Messages)
	m.messages = append(m.messages, snapshot)
	m.mu.Unlock()

	ch := make(chan provider.StreamEvent, 8)
	go func() {
		defer close(ch)
		if call <= m.rounds {
			callID := fmt.Sprintf("call_%d", call)
			ch <- provider.StreamEvent{Type: provider.EventToolCallStart, ToolCallID: callID, ToolName: "grep"}
			ch <- provider.StreamEvent{Type: provider.EventToolCallDelta, ToolCallID: callID, ToolInput: []byte(`{}`)}
			ch <- provider.StreamEvent{Type: provider.EventToolCallEnd, ToolCallID: callID}
			fr := "tool_use"
			ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
			return
		}
		ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "final answer"}
		fr := "stop"
		ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
	}()
	return ch, nil
}

// TestRunLoop_WorkingSetAccumulatesAcrossToolRounds verifies P1-2: the loop keeps
// its in-memory working set correct across many tool iterations by folding in the
// messages it creates (by known ID) instead of reloading the whole history each
// step. It drives three tool rounds and asserts the final request the provider
// receives contains every prior tool_use paired with its tool_result — which only
// holds if the fold preserved the full, correctly-ordered conversation.
func TestRunLoop_WorkingSetAccumulatesAcrossToolRounds(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	// Pre-seed the capability record so resolveImageSupport doesn't spend a probe
	// StreamChat call (which would offset the call counter).
	if err := session.SetModelCapability(database, &session.ModelCapability{ModelID: "mock-model", SupportsImages: false, ProbedAt: session.Now()}); err != nil {
		t.Fatalf("set capability: %v", err)
	}

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	mock := &toolRoundsProvider{rounds: 3}
	reg.Register(mock)

	tools := tool.NewRegistry()
	tools.Register(noopGrepTool{}) // registered under ID "grep", which the build agent may call

	lr := &LoopRunner{
		Store:    store,
		Bus:      bus.New(64),
		Registry: reg,
		Tools:    tools,
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
	textData, _ := json.Marshal(session.TextPartData{Text: "do the task"})
	if err := store.CreatePart(&session.Part{ID: session.NewPartID(), MessageID: userMsg.ID, SessionID: sess.ID, Type: session.PartText, Data: textData, CreatedAt: session.Now(), UpdatedAt: session.Now()}); err != nil {
		t.Fatalf("create user part: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- lr.RunLoop(context.Background(), sess.ID, "build", 0, 0) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunLoop returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunLoop did not complete in time")
	}

	// 3 tool rounds + 1 final text turn = 4 provider calls.
	mock.mu.Lock()
	calls := mock.calls
	finalReq := mock.messages[len(mock.messages)-1]
	mock.mu.Unlock()
	if calls != 4 {
		t.Fatalf("expected 4 provider calls (3 tool rounds + final), got %d", calls)
	}

	// The final request must carry all three tool_use blocks, each paired with a
	// tool_result — proving the working set accumulated the full history across
	// the folds, in the right order.
	uses := toolUseIDs(finalReq)
	results := toolResultIDs(finalReq)
	if len(uses) != 3 {
		t.Fatalf("final request should contain 3 tool_use blocks, got %d (%v)", len(uses), uses)
	}
	for _, id := range uses {
		if !results[id] {
			t.Errorf("tool_use %q missing its tool_result in the final request — working set fold dropped a message", id)
		}
	}

	// The persisted conversation must match what a full reload produces and end
	// with the final assistant text.
	msgs, err := store.GetMessages(sess.ID, "", 100)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	last := msgs[len(msgs)-1]
	if last.Info.Role != session.RoleAssistant {
		t.Fatalf("expected last message to be the assistant's final answer, got role %q", last.Info.Role)
	}
	if got := extractLastAssistantText(msgs); got != "final answer" {
		t.Errorf("final assistant text = %q, want %q", got, "final answer")
	}
}
