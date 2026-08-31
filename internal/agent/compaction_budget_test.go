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

// budgetModelContextWindow and budgetModelMaxOutput are chosen so the arithmetic
// in outputTokenBudget separates cleanly: a step sized by the stale reported
// count (30000) leaves 40000-30000-8000 = 2000 of room and falls to the 4096
// floor, while a step sized by what is actually being sent leaves far more than
// the model's own 8000 ceiling.
const (
	budgetModelContextWindow = 40000
	budgetModelMaxOutput     = 8000
	budgetPreCompactTokens   = 30000
)

// budgetScriptProvider runs the same four-step script as compactionScriptProvider
// — two tool rounds, a compact_context call, a final answer — but additionally
// reports token usage and records the output budget of every request.
//
// Usage is small for the first two steps and large on the step that compacts,
// which is the realistic shape: the agent reaches for compact_context precisely
// because the turn has grown.
type budgetScriptProvider struct {
	mu        sync.Mutex
	calls     int
	maxTokens []int
}

func (m *budgetScriptProvider) ID() string { return "budget" }
func (m *budgetScriptProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{
		ID: "budget-model", ProviderID: "budget",
		ContextWindow: budgetModelContextWindow, MaxOutputTokens: budgetModelMaxOutput,
	}}
}

func (m *budgetScriptProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	m.mu.Lock()
	m.calls++
	call := m.calls
	m.maxTokens = append(m.maxTokens, req.MaxTokens)
	m.mu.Unlock()

	ch := make(chan provider.StreamEvent, 8)
	go func() {
		defer close(ch)
		emit := func(name string, input string) {
			callID := fmt.Sprintf("call_%d", call)
			ch <- provider.StreamEvent{Type: provider.EventToolCallStart, ToolCallID: callID, ToolName: name}
			ch <- provider.StreamEvent{Type: provider.EventToolCallDelta, ToolCallID: callID, ToolInput: []byte(input)}
			ch <- provider.StreamEvent{Type: provider.EventToolCallEnd, ToolCallID: callID}
		}
		switch call {
		case 1, 2:
			emit("grep", `{}`)
		case 3:
			args, _ := json.Marshal(map[string]string{"summary": e2eSummary})
			emit("compact_context", string(args))
		default:
			ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "done"}
		}

		input := 5000
		if call == 3 {
			// The request that motivated compacting.
			input = budgetPreCompactTokens
		}
		ch <- provider.StreamEvent{Type: provider.EventUsage, Usage: &provider.TokenUsage{
			InputTokens: input, OutputTokens: 20,
		}}
		fr := "tool_use"
		if call >= 4 {
			fr = "stop"
		}
		ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
	}()
	return ch, nil
}

// After compact_context succeeds, the provider-reported input count describes a
// request that is no longer being sent. effectiveRequestTokens prefers the
// LARGER of estimate and reported, so leaving it in place sizes every remaining
// step of the turn against history the agent just discarded — flooring the
// output budget (which truncates long tool arguments mid-write) and, on a large
// enough turn, triggering an LLM-driven compaction of an already-tiny request:
// the exact expense compact_context exists to avoid on these endpoints.
func TestRunLoop_CompactContextClearsTheReportedInputCount(t *testing.T) {
	budgets, llmCompactions := runBudgetScript(t)

	if len(budgets) != 4 {
		t.Fatalf("expected 4 provider calls, got %d", len(budgets))
	}

	// The load-bearing assertion. The agent compacted at step 3, taking the
	// request down to a few thousand tokens. If the reported count from the
	// discarded request is still in play, step 4 measures itself at 30000
	// against a 20000 threshold and runs an LLM-driven compaction of a request
	// that is already small — re-sending the whole history to summarize it, on
	// precisely the non-caching endpoint compact_context exists to protect.
	if llmCompactions != 0 {
		t.Errorf("%d LLM-driven compaction(s) ran after the agent compacted in-turn; "+
			"the pre-compaction reported count (%d) is still sizing the request",
			llmCompactions, budgetPreCompactTokens)
	}

	// Same stale count, second consequence: it eats the output budget. Every
	// step here should be able to afford the model's full ceiling.
	for i, mt := range budgets {
		if mt != budgetModelMaxOutput {
			t.Errorf("step %d MaxTokens = %d, want the model's full %d", i+1, mt, budgetModelMaxOutput)
		}
	}
}

// runBudgetScript returns each request's output budget and the number of
// LLM-driven compactions the turn triggered.
func runBudgetScript(t *testing.T) ([]int, int) {
	t.Helper()
	resetCacheVerdicts()
	t.Cleanup(resetCacheVerdicts)

	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if err := session.SetModelCapability(database, &session.ModelCapability{
		ModelID: "budget-model", SupportsImages: false, ProbedAt: session.Now(),
	}); err != nil {
		t.Fatalf("set capability: %v", err)
	}
	// Non-caching, so compact_context is offered from step 1 with no observation
	// window in the way.
	if err := session.SetModelCacheSupport(database, "budget-model", "budget", string(provider.CacheAbsent), session.Now()); err != nil {
		t.Fatalf("seed cache verdict: %v", err)
	}

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	mock := &budgetScriptProvider{}
	reg.Register(mock)

	tools := tool.NewRegistry()
	tools.Register(noopGrepTool{})
	tools.Register(tool.NewCompactContextTool())

	eventBus := bus.New(64)
	// loop.compacted is published by compactRequest and nowhere else, so it is
	// an exact count of the LLM-driven compactions this turn performed.
	events := eventBus.SubscribeAll()
	compactions := make(chan int, 1)
	go func() {
		n := 0
		for e := range events {
			if e.Type == "loop.compacted" {
				n++
			}
		}
		compactions <- n
	}()

	lr := &LoopRunner{
		Store: store, Bus: eventBus, Registry: reg, Tools: tools,
		Dir: t.TempDir(), MaxSteps: 20,
	}
	sess := &session.Session{
		ID: session.NewSessionID(), ProjectID: "p", Directory: t.TempDir(),
		Title: "t", Model: "budget-model", SessionType: "build",
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

	eventBus.Unsubscribe(events)
	llmCompactions := <-compactions

	mock.mu.Lock()
	defer mock.mu.Unlock()
	return append([]int{}, mock.maxTokens...), llmCompactions
}
