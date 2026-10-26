// Package agent: tests for the per-turn browser takeover steer. When the live
// bridge reports connected, RunLoop appends a system entry steering
// current-web checks to the takeover tools while deep_search keeps its own
// external-knowledge section; when the bridge is dark (or no status source is
// wired, as in the CLI), the steer is absent and nothing else moves.
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

// steerFakeDeepSearch stands in for the deep_search tool: never called (the
// provider answers with text at once), registered only so hasDeepSearch — the
// gate that keeps the steer off an agent with no search backend — passes.
type steerFakeDeepSearch struct{}

func (steerFakeDeepSearch) ID() string          { return "deep_search" }
func (steerFakeDeepSearch) Description() string { return "deep search (test)" }
func (steerFakeDeepSearch) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`)
}
func (steerFakeDeepSearch) Execute(_ context.Context, _ json.RawMessage, _ tool.Context) (tool.Result, error) {
	return tool.Result{Title: "deep_search", Output: "sources"}, nil
}

// steerRecordingProvider answers at once with a final text and records the
// system entries every request carried, so the test can watch the steer move
// on and off the wire.
type steerRecordingProvider struct {
	mu     sync.Mutex
	calls  int
	system []string
}

func (p *steerRecordingProvider) ID() string { return "mock" }

func (p *steerRecordingProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", ProviderID: "mock", ContextWindow: 128000}}
}

func (p *steerRecordingProvider) StreamChat(_ context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	p.mu.Lock()
	p.calls++
	p.system = append([]string{strings.Join(req.System, "\n\n")}, p.system...)
	p.mu.Unlock()

	ch := make(chan provider.StreamEvent, 4)
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "done"}
		fr := "stop"
		ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
	}()
	return ch, nil
}

// runSteerTurn boots a one-call build turn against the recording provider and
// returns the joined system prompt of the first request.
func runSteerTurn(t *testing.T, status func() (bool, int)) string {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	// Pre-seed the capability record so resolveImageSupport doesn't spend a
	// probe StreamChat call, which would offset the first-recorded prompt.
	if err := session.SetModelCapability(database, &session.ModelCapability{ModelID: "mock-model", SupportsImages: false, ProbedAt: session.Now()}); err != nil {
		t.Fatalf("set capability: %v", err)
	}

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	mock := &steerRecordingProvider{}
	reg.Register(mock)

	tools := tool.NewRegistry()
	tools.Register(steerFakeDeepSearch{})
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
	if status != nil {
		lr.BrowserStatus = status
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
	textData, _ := json.Marshal(session.TextPartData{Text: "check the news"})
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
	return mock.system[0]
}

func TestRunLoop_BrowserSteerMarksAGrantedTab(t *testing.T) {
	got := runSteerTurn(t, func() (bool, int) { return true, 1 })

	if !strings.Contains(got, "## Browser at hand") {
		t.Errorf("no takeover steer in the system prompt:\n%s", got)
	}
	if !strings.Contains(got, "1 tab(s) are granted") {
		t.Errorf("steer does not report the granted count:\n%s", got)
	}
	if !strings.Contains(got, "browser_navigate") || !strings.Contains(got, "browser_snapshot") {
		t.Errorf("steer does not name the takeover ops:\n%s", got)
	}
	// The steer must refine deep_search, not hide it: the external-knowledge
	// section still stands beside it.
	if !strings.Contains(got, "deep_search") {
		t.Errorf("steer dropped deep_search from the prompt it steers:\n%s", got)
	}
}

func TestRunLoop_BrowserSteerConnectedWithoutAGrantedTab(t *testing.T) {
	got := runSteerTurn(t, func() (bool, int) { return true, 0 })

	if !strings.Contains(got, "## Browser at hand") {
		t.Errorf("a connected bridge with no granted tab still deserves the steer:\n%s", got)
	}
	if !strings.Contains(got, "newTab") {
		t.Errorf("steer does not point at the newTab auto-grant:\n%s", got)
	}
}

func TestRunLoop_BrowserSteerAbsentWhenBridgeDark(t *testing.T) {
	got := runSteerTurn(t, func() (bool, int) { return false, 0 })

	if strings.Contains(got, "## Browser at hand") {
		t.Errorf("unpaired bridge must not emit the steer:\n%s", got)
	}
	// deep_search keeps its own section regardless.
	if !strings.Contains(got, "deep_search") {
		t.Errorf("deep_search section missing alongside a dark bridge:\n%s", got)
	}
}

func TestRunLoop_BrowserSteerAbsentWithoutAStatusSource(t *testing.T) {
	// nil BrowserStatus is the CLI's wiring: no bridge there, so no steer.
	got := runSteerTurn(t, nil)

	if strings.Contains(got, "## Browser at hand") {
		t.Errorf("no status source wired but the steer was emitted:\n%s", got)
	}
}

// TestBrowserSteerPromptVariants pins the two steer wordings at the unit
// level, so an edit that drops the deep_search fallback language from either
// fails locally.
func TestBrowserSteerPromptVariants(t *testing.T) {
	granted := browserSteerPrompt(2)
	if !strings.Contains(granted, "2 tab(s)") {
		t.Errorf("granted steer lost the count: %s", granted)
	}
	if !strings.Contains(granted, "broad multi-source synthesis") {
		t.Errorf("granted steer lost the deep_search carve-out: %s", granted)
	}

	none := browserSteerPrompt(0)
	if !strings.Contains(none, "newTab") {
		t.Errorf("no-grant steer lost the newTab auto-grant: %s", none)
	}
	if strings.Contains(none, "tab(s) are granted") {
		t.Errorf("no-grant steer still claims tabs are granted: %s", none)
	}
}