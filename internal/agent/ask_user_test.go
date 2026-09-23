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
	"github.com/prasenjeet-symon/ogcode/internal/question"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// askScriptProvider drives a two-step turn: an ask_user call, then a final
// answer. It records every request's tool list and system entries so the test
// can tell whether the tool and its guidance were on the wire.
type askScriptProvider struct {
	mu      sync.Mutex
	calls   int
	tools   [][]string
	systems [][]string
	msgs    [][]provider.ModelMessage
}

func (m *askScriptProvider) ID() string { return "mock" }
func (m *askScriptProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", ProviderID: "mock"}}
}

func (m *askScriptProvider) StreamChat(_ context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	m.mu.Lock()
	m.calls++
	call := m.calls
	snapshot := make([]provider.ModelMessage, len(req.Messages))
	copy(snapshot, req.Messages)
	m.msgs = append(m.msgs, snapshot)
	names := make([]string, 0, len(req.Tools))
	for _, td := range req.Tools {
		names = append(names, td.Name)
	}
	m.tools = append(m.tools, names)
	m.systems = append(m.systems, append([]string{}, req.System...))
	m.mu.Unlock()

	ch := make(chan provider.StreamEvent, 8)
	go func() {
		defer close(ch)
		if call == 1 {
			callID := "call_ask"
			ch <- provider.StreamEvent{Type: provider.EventToolCallStart, ToolCallID: callID, ToolName: "ask_user"}
			ch <- provider.StreamEvent{Type: provider.EventToolCallDelta, ToolCallID: callID, ToolInput: []byte(askUserCallArgs)}
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

const askUserCallArgs = `{"questions":[{"header":"Storage","question":"Which store?","options":[{"label":"Postgres"},{"label":"SQLite"}]}]}`

// askScriptOptions tunes one run of the scripted turn.
type askScriptOptions struct {
	gated        bool // run under permission gating, as the server's session loop does
	registerTool bool // register ask_user in the tool registry
	manager      bool // give the LoopRunner a question manager
}

type askScriptResult struct {
	msgs    [][]provider.ModelMessage
	tools   [][]string
	systems [][]string
	reply   question.Reply
	asked   int
}

// runAskScript runs the scripted turn. When the loop is genuinely waiting, a
// helper goroutine answers the batch through the manager — the same path the
// HTTP handler takes — so the turn can complete.
func runAskScript(t *testing.T, opts askScriptOptions, answer question.Reply) askScriptResult {
	t.Helper()

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
	mock := &askScriptProvider{}
	reg.Register(mock)

	tools := tool.NewRegistry()
	tools.Register(noopGrepTool{})

	lr := &LoopRunner{
		Store: store, Bus: bus.New(64), Registry: reg, Tools: tools,
		Dir: t.TempDir(), MaxSteps: 20,
	}
	if opts.manager {
		lr.Questions = question.NewManager()
	}
	if opts.registerTool {
		tools.Register(tool.AskUserTool{Ask: lr.AskUser})
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
	textData, _ := json.Marshal(session.TextPartData{Text: "do the task"})
	if err := store.CreatePart(&session.Part{
		ID: session.NewPartID(), MessageID: userMsg.ID, SessionID: sess.ID,
		Type: session.PartText, Data: textData, CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}); err != nil {
		t.Fatalf("create user part: %v", err)
	}

	result := askScriptResult{reply: answer}
	// The answering goroutine stands in for the browser: it waits for the batch
	// to appear, then replies through the manager.
	answering := make(chan struct{})
	asked := 0
	if opts.manager {
		go func() {
			deadline := time.After(10 * time.Second)
			for {
				select {
				case <-answering:
					return
				case <-deadline:
					return
				default:
				}
				pending := lr.Questions.PendingForSession(string(sess.ID))
				if len(pending) > 0 {
					asked = len(pending)
					lr.Questions.Reply(pending[0].ID, answer)
					return
				}
				time.Sleep(2 * time.Millisecond)
			}
		}()
	}

	ctx := context.Background()
	if opts.gated {
		ctx = WithPermissionGating(ctx)
	}
	done := make(chan error, 1)
	go func() { done <- lr.RunLoop(ctx, sess.ID, "build", 0, 0) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunLoop returned error: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunLoop did not complete in time")
	}
	close(answering)

	mock.mu.Lock()
	result.msgs, result.tools, result.systems = mock.msgs, mock.tools, mock.systems
	mock.mu.Unlock()
	result.asked = asked
	return result
}

func offered(systems [][]string, step int, s string) bool {
	if step >= len(systems) {
		return false
	}
	for _, entry := range systems[step] {
		if strings.Contains(entry, s) {
			return true
		}
	}
	return false
}

func TestRunLoop_AskUserOfferedWhenGated(t *testing.T) {
	res := runAskScript(t, askScriptOptions{gated: true, registerTool: true, manager: true},
		question.Reply{Answers: []question.Answer{{Selected: []string{"Postgres"}}}})

	if len(res.tools) < 2 {
		t.Fatalf("expected at least 2 provider calls, got %d", len(res.tools))
	}
	if !containsString(res.tools[0], "ask_user") {
		t.Fatalf("ask_user was not offered in a gated session: %v", res.tools[0])
	}
	if !offered(res.systems, 0, "Asking the User") {
		t.Error("ask_user was offered without its guidance in the system prompt")
	}
	if res.asked != 1 {
		t.Errorf("the loop registered %d pending batches, want 1", res.asked)
	}

	// The answers must reach the model as the tool's result, so the turn can
	// resume from what the user said.
	got := toolResultText(res.msgs[len(res.msgs)-1], "call_ask")
	if !strings.Contains(got, "Postgres") {
		t.Errorf("the answer never reached the model; tool result was:\n%s", got)
	}
	if strings.Contains(got, "not available") {
		t.Errorf("ask_user was offered but rejected at execution: %q", got)
	}
}

// The tool and its guidance travel together. A tool offered with no explanation
// of when to use it, or guidance naming a tool the agent was never given, are
// both worse than shipping neither.
func TestRunLoop_AskUserAbsentWhenUngated(t *testing.T) {
	res := runAskScript(t, askScriptOptions{registerTool: true, manager: true}, question.Reply{})

	for step, names := range res.tools {
		if containsString(names, "ask_user") {
			t.Errorf("step %d offered ask_user to an ungated run — a headless loop has nobody to answer: %v", step, names)
		}
		if offered(res.systems, step, "Asking the User") {
			t.Errorf("step %d carried the ask_user guidance without the tool", step)
		}
	}
}

// A loop with no question manager (headless, a test, a sub-agent) has nobody to
// route the batch to, so the tool must not be offered however interactive the
// context claims to be.
func TestRunLoop_AskUserAbsentWithoutManager(t *testing.T) {
	res := runAskScript(t, askScriptOptions{gated: true, registerTool: true}, question.Reply{})

	for step, names := range res.tools {
		if containsString(names, "ask_user") {
			t.Errorf("step %d offered ask_user with no question manager wired: %v", step, names)
		}
	}
}

// The tool must also be registered, not merely present in the agent's toolset —
// offering a tool the registry cannot execute would produce a call that fails
// after the user was already interrupted.
func TestRunLoop_AskUserAbsentWhenUnregistered(t *testing.T) {
	res := runAskScript(t, askScriptOptions{gated: true, manager: true}, question.Reply{})

	for step, names := range res.tools {
		if containsString(names, "ask_user") {
			t.Errorf("step %d offered ask_user although it is not in the registry: %v", step, names)
		}
	}
}

// The sub-agent runners clear Questions precisely because WithoutLoopControl
// does NOT strip permission gating — so a sub-agent inherits the gated context
// and would otherwise be offered a dialog nobody is watching on the other end of.
// This pins the reason the childRunner.Questions = nil lines are load-bearing.
func TestWithoutLoopControlKeepsPermissionGating(t *testing.T) {
	ctx := WithPermissionGating(context.Background())
	if !PermissionGatingEnabled(WithoutLoopControl(ctx)) {
		t.Fatal("WithoutLoopControl stripped permission gating; the sub-agent Questions=nil lines would be unnecessary")
	}
}

// ask_user is offered the way compact_context is — appended to the turn's tool
// list by the loop, never baked into an agent's static toolset. So it must be in
// no agent's Tools list, and the loop's offer must come from canAskUser.
func TestNoAgentBakesInAskUser(t *testing.T) {
	for _, name := range []string{"build", "task", "subagent", "memory-recall", "plan", "breakdown", "note", "index", "search"} {
		a := GetAgent(name)
		if a.HasTool("ask_user") {
			t.Errorf("agent %q lists ask_user statically; it must be added per turn by the loop", name)
		}
	}
}

func TestCanAskUserIsOnlyTheBuildAgent(t *testing.T) {
	if !BuildAgent.canAskUser() {
		t.Error("the build agent must be able to ask the user")
	}
	// TaskAgent shares the build agent's toolset but is headless — the case this
	// check exists for.
	for _, name := range []string{"task", "subagent", "memory-recall", "plan", "breakdown", "note", "index", "search"} {
		a := GetAgent(name)
		if a.canAskUser() {
			t.Errorf("agent %q may ask the user; only the interactive build agent has a user to ask", name)
		}
	}
}
