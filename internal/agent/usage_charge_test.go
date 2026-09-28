package agent

import (
	"context"
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

// usageTestRunner is a LoopRunner over a fresh database with one registered
// provider, plus a parent session that spawned work can charge.
func usageTestRunner(t *testing.T, p provider.Provider) (*LoopRunner, *session.Session, string) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := session.SetModelCapability(database, &session.ModelCapability{ModelID: "mock-model", ProbedAt: session.Now()}); err != nil {
		t.Fatalf("set capability: %v", err)
	}
	store := session.NewStore(database)
	reg := provider.NewRegistry()
	reg.Register(p)
	dir := t.TempDir()
	parent := &session.Session{
		ID: session.NewSessionID(), ProjectID: dir, Directory: dir, Title: "parent",
		Model: "mock-model", SessionType: "build", CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}
	if err := store.Create(parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	lr := &LoopRunner{
		Store: store, Bus: bus.New(64), Registry: reg, Tools: tool.NewRegistry(),
		Dir: dir, MaxSteps: 20,
	}
	return lr, parent, dir
}

func utilityOf(t *testing.T, store *session.Store, id session.SessionID) session.TokenCounts {
	t.Helper()
	s, err := store.Get(id)
	if err != nil || s == nil {
		t.Fatalf("get session %s: %v", id, err)
	}
	if s.UtilityTokens == nil {
		return session.TokenCounts{}
	}
	return *s.UtilityTokens
}

// ctxSessionProvider records which session each request's context runs on
// behalf of, then answers with plain text.
type ctxSessionProvider struct {
	mu   sync.Mutex
	seen []session.SessionID
}

func (p *ctxSessionProvider) ID() string { return "mock" }
func (p *ctxSessionProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", ProviderID: "mock", ContextWindow: 200_000}}
}
func (p *ctxSessionProvider) StreamChat(ctx context.Context, _ provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	id, _ := usageSessionFrom(ctx)
	p.mu.Lock()
	p.seen = append(p.seen, id)
	p.mu.Unlock()
	ch := make(chan provider.StreamEvent, 2)
	fr := "stop"
	ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "ok"}
	ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
	close(ch)
	return ch, nil
}

// Work a turn spawns only inherits RunLoop's context, so the session has to be
// on it for that work to know whom to charge. A nested loop re-stamps its own.
func TestRunLoopStampsItsSessionOnTheContext(t *testing.T) {
	p := &ctxSessionProvider{}
	lr, parent, _ := usageTestRunner(t, p)
	user := &session.MessageInfo{ID: session.NewMessageID(), SessionID: parent.ID, Role: session.RoleUser, CreatedAt: session.Now()}
	if err := lr.Store.CreateMessage(user); err != nil {
		t.Fatalf("create user message: %v", err)
	}
	if err := lr.RunLoop(context.Background(), parent.ID, "build", 0, 0); err != nil {
		t.Fatalf("RunLoop: %v", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.seen) == 0 {
		t.Fatal("provider was never called")
	}
	for i, id := range p.seen {
		if id != parent.ID {
			t.Errorf("request %d ran on behalf of %q, want the loop's session %q", i, id, parent.ID)
		}
	}
}

// A task sub-agent runs a full loop in an ephemeral session that is deleted when
// it returns. Its tokens used to be deleted with it; they must now land on the
// session that delegated the work.
func TestRunTaskSessionChargesItsRunToTheParent(t *testing.T) {
	// One tool round, then a final answer: two model calls of 1000 in / 10 out.
	p := &toolRoundsProvider{rounds: 1, window: 200_000, inputPerCall: 1000}
	lr, parent, dir := usageTestRunner(t, p)

	ctx := withUsageSession(context.Background(), parent.ID)
	done := make(chan error, 1)
	go func() {
		_, err := lr.RunTaskSession(ctx, "investigate", "find X and report", dir, "mock-model", "")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunTaskSession: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunTaskSession did not complete")
	}

	if p.calls != 2 {
		t.Fatalf("child made %d model calls, want 2", p.calls)
	}
	got := utilityOf(t, lr.Store, parent.ID)
	if got.Input != 2000 || got.Output != 20 {
		t.Errorf("parent utility = %+v, want input 2000, output 20 (both child steps)", got)
	}
	if got.Total != 2020 {
		t.Errorf("parent utility Total = %d, want 2020", got.Total)
	}
	// The child is still cleaned up.
	sessions, err := lr.Store.List(dir)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != parent.ID {
		t.Errorf("sessions left = %d, want only the parent", len(sessions))
	}
}

// Work started outside any turn has no session to charge; it must charge
// nothing rather than guess.
func TestRunTaskSessionWithoutAnOwnerChargesNothing(t *testing.T) {
	p := &toolRoundsProvider{rounds: 0, window: 200_000, inputPerCall: 1000}
	lr, parent, dir := usageTestRunner(t, p)
	if _, err := lr.RunTaskSession(context.Background(), "x", "y", dir, "mock-model", ""); err != nil {
		t.Fatalf("RunTaskSession: %v", err)
	}
	if got := utilityOf(t, lr.Store, parent.ID); got != (session.TokenCounts{}) {
		t.Errorf("parent utility = %+v, want nothing charged", got)
	}
}

// The fold covers the child's own utility calls as well as its steps, and adds
// to whatever the parent already had.
func TestFoldChildUsageAddsStepsAndChildUtility(t *testing.T) {
	lr, parent, dir := usageTestRunner(t, &ctxSessionProvider{})
	child := &session.Session{ID: session.NewSessionID(), ProjectID: dir, Directory: dir, SessionType: "subagent", CreatedAt: session.Now(), UpdatedAt: session.Now()}
	if err := lr.Store.Create(child); err != nil {
		t.Fatalf("create child: %v", err)
	}
	for _, tc := range []session.TokenCounts{
		{Input: 100, CacheRead: 900, Output: 40, Reasoning: 10},
		{Input: 50, CacheWrite: 300, Output: 60},
	} {
		tc := tc
		m := &session.MessageInfo{ID: session.NewMessageID(), SessionID: child.ID, Role: session.RoleAssistant, Tokens: &tc, CreatedAt: session.Now()}
		if err := lr.Store.CreateMessage(m); err != nil {
			t.Fatalf("create message: %v", err)
		}
		if err := lr.Store.UpdateMessage(m); err != nil {
			t.Fatalf("update message: %v", err)
		}
	}
	if err := lr.Store.AddUtilityUsage(child.ID, session.TokenCounts{Input: 7, Output: 3}); err != nil {
		t.Fatalf("child utility: %v", err)
	}
	if err := lr.Store.AddUtilityUsage(parent.ID, session.TokenCounts{Input: 1, Output: 1}); err != nil {
		t.Fatalf("parent utility: %v", err)
	}

	lr.foldChildUsage(withUsageSession(context.Background(), parent.ID), child.ID)

	want := session.TokenCounts{Input: 158, CacheRead: 900, CacheWrite: 300, Output: 104, Reasoning: 10}
	want.Total = want.Consumed()
	if got := utilityOf(t, lr.Store, parent.ID); got != want {
		t.Errorf("parent utility = %+v, want %+v", got, want)
	}
}

// Deep search's two calls go through oneShotLLM, which used to drop the usage.
// Providers may repeat cumulative usage; the last report is the one that counts.
func TestOneShotLLMReturnsTheLastUsageReport(t *testing.T) {
	p := scriptedSearchProvider{events: []provider.StreamEvent{
		{Type: provider.EventTextDelta, Text: "answer"},
		{Type: provider.EventUsage, Usage: &provider.TokenUsage{InputTokens: 500, OutputTokens: 5}},
		{Type: provider.EventUsage, Usage: &provider.TokenUsage{InputTokens: 500, OutputTokens: 42}},
	}}
	text, usage, err := oneShotLLM(context.Background(), p, "test-model", "s", "u", 0)
	if err != nil || text != "answer" {
		t.Fatalf("oneShotLLM = %q, %v", text, err)
	}
	if usage == nil || usage.InputTokens != 500 || usage.OutputTokens != 42 {
		t.Errorf("usage = %+v, want the last report (500 in, 42 out)", usage)
	}
}

// A failed call can still have been billed, so its usage is returned with the error.
func TestOneShotLLMReturnsUsageWithAStreamError(t *testing.T) {
	p := scriptedSearchProvider{events: []provider.StreamEvent{
		{Type: provider.EventUsage, Usage: &provider.TokenUsage{InputTokens: 800}},
		{Type: provider.EventError, Error: "boom"},
	}}
	_, usage, err := oneShotLLM(context.Background(), p, "test-model", "s", "u", 0)
	if err == nil {
		t.Fatal("want the stream error")
	}
	if usage == nil || usage.InputTokens != 800 {
		t.Errorf("usage = %+v, want the 800 input tokens already spent", usage)
	}
}

func TestChargeUsageRecordsOnTheStampedSession(t *testing.T) {
	lr, parent, _ := usageTestRunner(t, &ctxSessionProvider{})
	lr.chargeUsage(withUsageSession(context.Background(), parent.ID), "", "", &provider.TokenUsage{InputTokens: 300, CacheReadTokens: 700, OutputTokens: 25})
	lr.chargeUsage(context.Background(), "", "", &provider.TokenUsage{InputTokens: 999}) // no owner: dropped
	got := utilityOf(t, lr.Store, parent.ID)
	if got.Input != 300 || got.CacheRead != 700 || got.Output != 25 || got.Total != 1025 {
		t.Errorf("parent utility = %+v, want input 300, cacheRead 700, output 25, total 1025", got)
	}
}

// The turn-memory writer runs after every turn; its summary call must hand back
// what it spent so the loop can charge it.
func TestSynthClientReturnsUsage(t *testing.T) {
	p := scriptedSearchProvider{events: []provider.StreamEvent{
		{Type: provider.EventTextDelta, Text: "# Summary"},
		{Type: provider.EventUsage, Usage: &provider.TokenUsage{InputTokens: 4000, CacheReadTokens: 1000, OutputTokens: 300}},
	}}
	text, usage, err := newSynthClient(p, "test-model").Chat(context.Background(), "sys", "digest")
	if err != nil || text != "# Summary" {
		t.Fatalf("Chat = %q, %v", text, err)
	}
	if usage == nil || usage.InputTokens != 4000 || usage.CacheReadTokens != 1000 || usage.OutputTokens != 300 {
		t.Errorf("usage = %+v, want 4000 in, 1000 cache read, 300 out", usage)
	}
}
