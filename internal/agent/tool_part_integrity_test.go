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

// badArgsProvider opens a tool call whose arguments are not valid JSON — the
// shape an OpenAI-compatible proxy produces when it forwards a truncated
// tool-call delta as a complete one.
type badArgsProvider struct{ calls int }

func (m *badArgsProvider) ID() string { return "mock-badargs" }
func (m *badArgsProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-badargs-model", ProviderID: "mock-badargs"}}
}

func (m *badArgsProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	m.calls++
	call := m.calls
	ch := make(chan provider.StreamEvent, 8)
	go func() {
		defer close(ch)
		if call == 1 {
			ch <- provider.StreamEvent{
				Type:       provider.EventToolCallStart,
				ToolCallID: "call_truncated",
				ToolName:   "grep",
				ToolInput:  json.RawMessage(`{"pattern":"foo`), // never closed
			}
			ch <- provider.StreamEvent{Type: provider.EventToolCallEnd, ToolCallID: "call_truncated"}
			fr := "tool_use"
			ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
			return
		}
		ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "done"}
		fr := "stop"
		ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
	}()
	return ch, nil
}

// One tool call with unparseable arguments used to poison a session for good.
// Marshalling the part validated the raw arguments, the error was discarded, and
// a part with nil Data went to disk. From then on the UI rendered it as
// "Malformed tool part: missing state", and every request rebuilt from that
// history carried a tool result with no id — which Anthropic rejects outright
// with `unknown variant \`tool\“. Retrying and resuming both reproduce it
// exactly, because the history is rebuilt the same way each time.
func TestRunLoop_InvalidToolArgumentsStayPairable(t *testing.T) {
	resetCacheVerdicts()
	t.Cleanup(resetCacheVerdicts)

	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := session.SetModelCapability(database, &session.ModelCapability{
		ModelID: "mock-badargs-model", SupportsImages: false, ProbedAt: session.Now(),
	}); err != nil {
		t.Fatalf("set capability: %v", err)
	}
	if err := session.SetModelCacheSupport(database, "mock-badargs-model", "mock-badargs",
		string(provider.CacheSupported), session.Now()); err != nil {
		t.Fatalf("seed cache verdict: %v", err)
	}

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	reg.Register(&badArgsProvider{})
	tools := tool.NewRegistry()
	tools.Register(noopGrepTool{})

	lr := &LoopRunner{
		Store: store, Bus: bus.New(64), Registry: reg, Tools: tools,
		Dir: t.TempDir(), MaxSteps: 4,
	}
	sess := &session.Session{
		ID: session.NewSessionID(), ProjectID: "p", Directory: t.TempDir(),
		Title: "t", Model: "mock-badargs-model", SessionType: "build",
		CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	prompt := &session.MessageInfo{
		ID: session.NewMessageID(), SessionID: sess.ID,
		Role: session.RoleUser, CreatedAt: session.Now(),
	}
	if err := store.CreateMessage(prompt); err != nil {
		t.Fatalf("create user msg: %v", err)
	}
	textData, _ := json.Marshal(session.TextPartData{Text: "search for foo"})
	if err := store.CreatePart(&session.Part{
		ID: session.NewPartID(), MessageID: prompt.ID, SessionID: sess.ID,
		Type: session.PartText, Data: textData, CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}); err != nil {
		t.Fatalf("create user part: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- lr.RunLoop(context.Background(), sess.ID, "build", 0, 0) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run loop: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("run loop did not finish")
	}

	msgs, err := store.GetMessages(sess.ID, "", 100)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}

	// Every tool part on disk must be readable and carry its call id. The id is
	// what keeps the conversation valid; the arguments are recoverable, an
	// unreadable record is not.
	sawToolPart := false
	for _, m := range msgs {
		for _, p := range m.Parts {
			if p.Type != session.PartTool {
				continue
			}
			sawToolPart = true
			var data session.ToolPartData
			if err := json.Unmarshal(p.Data, &data); err != nil {
				t.Fatalf("tool part is unreadable (%q): %v — the UI shows this as a malformed part", string(p.Data), err)
			}
			if data.CallID == "" {
				t.Error("tool part lost its call id; nothing downstream can pair it")
			}
			if !json.Valid(data.State.Input) {
				t.Errorf("tool part input is not valid JSON: %q", string(data.State.Input))
			}
		}
	}
	if !sawToolPart {
		t.Fatal("no tool part was written at all")
	}

	// And the request rebuilt from that history must be one a provider accepts:
	// every tool_use answered, no empty ids, no stray "tool" role reaching a
	// provider that has no such role.
	calls, results := map[string]bool{}, map[string]bool{}
	for _, mm := range convertMessages(msgs, false, "claude-opus-4-6") {
		if mm.ToolCalls != nil {
			var cs []struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(mm.ToolCalls, &cs); err != nil {
				t.Fatalf("tool_calls not JSON: %v", err)
			}
			for _, c := range cs {
				if c.ID == "" {
					t.Error("a tool_use with an empty id reached the request")
				}
				calls[c.ID] = true
			}
		}
		if mm.Role == "tool" {
			if mm.ToolCallID == "" {
				t.Error("a tool result with an empty tool_call_id reached the request")
			}
			results[mm.ToolCallID] = true
		}
	}
	for id := range calls {
		if !results[id] {
			t.Errorf("tool_use %q has no matching tool_result; both APIs reject that", id)
		}
	}
	if len(calls) == 0 {
		t.Error("the tool call vanished entirely — it should be repaired, not dropped")
	}
}
