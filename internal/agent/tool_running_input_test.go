package agent

import (
	"context"
	"encoding/json"
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

const runningProbeArgs = `{"pattern":"slow command the user should see"}`

// deltaArgsProvider streams one tool call the way Anthropic does — the call
// opens with no arguments and they arrive only in the delta after it — then
// answers with text.
type deltaArgsProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *deltaArgsProvider) ID() string { return "mock" }
func (p *deltaArgsProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", ProviderID: "mock"}}
}

func (p *deltaArgsProvider) StreamChat(_ context.Context, _ provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()

	ch := make(chan provider.StreamEvent, 8)
	go func() {
		defer close(ch)
		if call == 1 {
			callID := "call_probe"
			ch <- provider.StreamEvent{Type: provider.EventToolCallStart, ToolCallID: callID, ToolName: "grep"}
			ch <- provider.StreamEvent{Type: provider.EventToolCallDelta, ToolCallID: callID, ToolInput: []byte(runningProbeArgs)}
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

// runningProbeTool reads its own tool part back from the store while it is
// executing — what a browser polling the session sees mid-call.
type runningProbeTool struct {
	store *session.Store
	seen  chan session.ToolState
}

func (runningProbeTool) ID() string          { return "grep" }
func (runningProbeTool) Description() string { return "records its running state (test)" }
func (runningProbeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"}}}`)
}
func (t runningProbeTool) Execute(_ context.Context, _ json.RawMessage, tctx tool.Context) (tool.Result, error) {
	msgs, err := t.store.GetMessages(tctx.SessionID, "", 100)
	if err != nil {
		return tool.Result{}, err
	}
	for _, m := range msgs {
		for _, p := range m.Parts {
			if p.Type != session.PartTool {
				continue
			}
			var data session.ToolPartData
			if json.Unmarshal(p.Data, &data) == nil && data.CallID == tctx.CallID {
				t.seen <- data.State
			}
		}
	}
	return tool.Result{Title: "grep", Output: "match"}, nil
}

// A slow tool shows the user what it is doing while it runs. The part is
// written when the call opens, before any arguments have streamed, so the
// "running" state has to take its input from the call as streamed — copying the
// part's own input forward left every running row reading `{}`: a spinner and a
// tool name, with no command, path or query to say what was taking so long.
func TestRunningToolPartCarriesStreamedInput(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := session.SetModelCapability(database, &session.ModelCapability{
		ModelID: "mock-model", ProbedAt: session.Now(),
	}); err != nil {
		t.Fatalf("set capability: %v", err)
	}

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	reg.Register(&deltaArgsProvider{})
	probe := runningProbeTool{store: store, seen: make(chan session.ToolState, 1)}
	tools := tool.NewRegistry()
	tools.Register(probe)

	lr := &LoopRunner{
		Store: store, Bus: bus.New(64), Registry: reg, Tools: tools,
		Dir: t.TempDir(), MaxSteps: 5,
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
	textData, _ := json.Marshal(session.TextPartData{Text: "find it"})
	if err := store.CreatePart(&session.Part{
		ID: session.NewPartID(), MessageID: userMsg.ID, SessionID: sess.ID,
		Type: session.PartText, Data: textData, CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}); err != nil {
		t.Fatalf("create user part: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- lr.RunLoop(context.Background(), sess.ID, "build", 0, 0) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunLoop returned error: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunLoop did not complete in time")
	}

	select {
	case st := <-probe.seen:
		if st.Status != session.ToolRunning {
			t.Fatalf("status while executing = %q, want %q", st.Status, session.ToolRunning)
		}
		var got, want map[string]any
		if err := json.Unmarshal(st.Input, &got); err != nil {
			t.Fatalf("running input is not a JSON object: %s", st.Input)
		}
		json.Unmarshal([]byte(runningProbeArgs), &want)
		if got["pattern"] != want["pattern"] {
			t.Fatalf("running input = %s, want the streamed arguments %s", st.Input, runningProbeArgs)
		}
	default:
		t.Fatal("the tool never ran, or never found its own part")
	}
}
