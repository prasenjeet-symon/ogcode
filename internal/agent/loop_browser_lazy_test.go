// Package agent: end-to-end test for the lazy browser wiring. RunLoop runs a
// matcher turn whose single tool call loads the browser-takeover skill; the
// test checks that browser_status rides the wire from step one while the other
// takeover ops stay off it, and that the family arrives on the NEXT provider
// request once the skill-load hook has offered it.
package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	bus "github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// browserSkillTool is a fake "skill" tool: the tool-load hook only looks at the
// tool call's name and input, never the result, so it needs nothing real behind
// it.
type browserSkillTool struct{}

func (browserSkillTool) ID() string          { return "skill" }
func (browserSkillTool) Description() string { return "noop skill (test)" }
func (browserSkillTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`)
}
func (browserSkillTool) Execute(_ context.Context, _ json.RawMessage, _ tool.Context) (tool.Result, error) {
	return tool.Result{Title: "skill", Output: "loaded"}, nil
}

// browserIntentTool stands in for update_intent, which decides intentOffered —
// the flag that makes the turn a matcher turn. The mock never calls it, so the
// test proves the sentinel is kept on a turn the matcher never routes.
type browserIntentTool struct{}

func (browserIntentTool) ID() string          { return "update_intent" }
func (browserIntentTool) Description() string { return "noop intent (test)" }
func (browserIntentTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"steps":{"type":"array"}}}`)
}
func (browserIntentTool) Execute(_ context.Context, _ json.RawMessage, _ tool.Context) (tool.Result, error) {
	return tool.Result{Title: "intent", Output: "set", Metadata: map[string]any{"intentSet": true}}, nil
}

// browserCallProvider answers the first StreamChat call with a call to the
// "skill" tool loading browser-takeover, then a final answer. It records the
// tool names each call received, so the test can watch the browser family move
// onto and stay on the wire.
type browserCallProvider struct {
	mu    sync.Mutex
	calls int
	names [][]string
}

func (p *browserCallProvider) ID() string { return "mock" }

func (p *browserCallProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", ProviderID: "mock", ContextWindow: 128000}}
}

func (p *browserCallProvider) StreamChat(_ context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	names := make([]string, 0, len(req.Tools))
	for _, def := range req.Tools {
		names = append(names, def.Name)
	}
	p.names = append(p.names, names)
	p.mu.Unlock()

	ch := make(chan provider.StreamEvent, 8)
	go func() {
		defer close(ch)
		if call == 1 {
			callID := "skill_call"
			ch <- provider.StreamEvent{Type: provider.EventToolCallStart, ToolCallID: callID, ToolName: "skill"}
			ch <- provider.StreamEvent{Type: provider.EventToolCallDelta, ToolCallID: callID, ToolInput: []byte(`{"name":"browser-takeover"}`)}
			ch <- provider.StreamEvent{Type: provider.EventToolCallEnd, ToolCallID: callID}
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

func TestRunLoop_BrowserSkillLoadPutsFamilyOnWire(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	// Pre-seed the capability record so resolveImageSupport doesn't spend a
	// probe StreamChat call, which would offset the call counter.
	if err := session.SetModelCapability(database, &session.ModelCapability{ModelID: "mock-model", SupportsImages: false, ProbedAt: session.Now()}); err != nil {
		t.Fatalf("set capability: %v", err)
	}

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	mock := &browserCallProvider{}
	reg.Register(mock)

	tools := tool.NewRegistry()
	tools.Register(browserSkillTool{})
	tools.Register(browserIntentTool{})
	for _, bt := range tool.NewBrowserTools(nil) {
		tools.Register(bt)
	}

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
	textData, _ := json.Marshal(session.TextPartData{Text: "take over the browser"})
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

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.calls != 2 {
		t.Fatalf("expected 2 provider calls (skill round + final answer), got %d", mock.calls)
	}

	has := func(names []string, id string) bool {
		for _, n := range names {
			if n == id {
				return true
			}
		}
		return false
	}

	first := mock.names[0]
	if !has(first, "browser_status") {
		t.Errorf("sentinel missing on the matcher turn's first request; wire = %v", first)
	}
	for _, op := range []string{"browser_navigate", "browser_click", "browser_type", "browser_screenshot"} {
		if has(first, op) {
			t.Errorf("takeover op %q rode the wire before the family was offered", op)
		}
	}

	second := mock.names[1]
	for _, id := range tool.BrowserToolIDs() {
		if !has(second, id) {
			t.Errorf("offer id %q missing from the post-skill-load wire; wire = %v", id, second)
		}
	}
}
