package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
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

// The compaction boundary is the most expensive place in a turn to lose the
// provider's prefix cache: the request made right after compacting is the
// largest of the turn (the maximum recent tool output kept verbatim) and the
// step after it shares almost all of that body. A provider's cache is a
// longest-common-prefix of the whole request, so the compacting step must send
// the same System and the same leading messages the following step will send —
// whichever path compacted it (proactive, before the send; reactive, after a
// context-length error).

// compactionSummaryMarker is a distinctive phrase from e2eSummary, used to find
// the summary wherever it was put on the wire.
const compactionSummaryMarker = "wire the refresh path"

// requestSnapshot is one StreamChat call as the provider saw it.
type requestSnapshot struct {
	summarizer bool
	system     []string
	messages   []provider.ModelMessage
}

// cacheIdentityProvider scripts a turn by agent-call number and records every
// request it receives, including the summarizer calls compaction makes.
type cacheIdentityProvider struct {
	mu      sync.Mutex
	summary string
	agent   int
	reqs    []requestSnapshot
	grepOn  func(call int) bool
	// usageOn reports an input-side token count on the chosen agent calls, the
	// way a real provider reports the exact size of the prompt it just read. The
	// proactive path compacts against that count, so a test can pin WHERE the
	// boundary falls instead of sizing a payload against the estimator.
	usageOn  map[int]int
	overflow int // 1-based agent call answered with a context-length error; 0 = never
	body     string
}

func (m *cacheIdentityProvider) ID() string { return "mock" }
func (m *cacheIdentityProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", ProviderID: "mock"}}
}

func (m *cacheIdentityProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	summarizer := len(req.Tools) == 0 && len(req.System) == 1 && strings.Contains(req.System[0], "summarizer")
	snapshot := requestSnapshot{
		summarizer: summarizer,
		system:     append([]string{}, req.System...),
		messages:   make([]provider.ModelMessage, len(req.Messages)),
	}
	copy(snapshot.messages, req.Messages)

	m.mu.Lock()
	m.reqs = append(m.reqs, snapshot)
	if !summarizer {
		m.agent++
	}
	call := m.agent
	m.mu.Unlock()

	if summarizer {
		ch := make(chan provider.StreamEvent, 4)
		go func() {
			defer close(ch)
			ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: m.summary}
			fr := "stop"
			ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
		}()
		return ch, nil
	}

	if m.overflow != 0 && call == m.overflow {
		return nil, &provider.APIError{Provider: "mock", StatusCode: 400, Body: m.body}
	}

	ch := make(chan provider.StreamEvent, 8)
	go func() {
		defer close(ch)
		if m.grepOn != nil && m.grepOn(call) {
			callID := fmt.Sprintf("call_%d", call)
			ch <- provider.StreamEvent{Type: provider.EventToolCallStart, ToolCallID: callID, ToolName: "grep"}
			ch <- provider.StreamEvent{Type: provider.EventToolCallDelta, ToolCallID: callID, ToolInput: []byte(`{}`)}
			ch <- provider.StreamEvent{Type: provider.EventToolCallEnd, ToolCallID: callID}
			if n, ok := m.usageOn[call]; ok {
				ch <- provider.StreamEvent{Type: provider.EventUsage, Usage: &provider.TokenUsage{InputTokens: n}}
			}
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

// snapshots returns a copy of every request recorded so far.
func (m *cacheIdentityProvider) snapshots() []requestSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]requestSnapshot{}, m.reqs...)
}

// agentOnly drops the summarizer calls, so indices line up with loop steps.
func agentOnly(reqs []requestSnapshot) []requestSnapshot {
	out := make([]requestSnapshot, 0, len(reqs))
	for _, r := range reqs {
		if !r.summarizer {
			out = append(out, r)
		}
	}
	return out
}

// cacheIdentityGrepTool answers "grep" — the tool round that grows the turn
// long enough for a compaction boundary to exist.
type cacheIdentityGrepTool struct{}

func (cacheIdentityGrepTool) ID() string            { return "grep" }
func (cacheIdentityGrepTool) Description() string    { return "grep for a pattern" }
func (cacheIdentityGrepTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

func (cacheIdentityGrepTool) Execute(ctx context.Context, args json.RawMessage, tctx tool.Context) (tool.Result, error) {
	return tool.Result{Title: "grep", Output: "match"}, nil
}

// runCacheIdentityTurn drives one build turn with a pinned context window and a
// scripted provider. It mirrors runCompactionScriptFull so the harness matches
// what a real build turn carries.
func runCacheIdentityTurn(t *testing.T, mock *cacheIdentityProvider, window int) []requestSnapshot {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	// Pre-seed the window so no probe call offsets the script and the loop sizes
	// compaction against a known value.
	if err := session.SetModelCapability(database, &session.ModelCapability{
		ModelID: "mock-model", SupportsImages: false, ContextWindow: window, ProbedAt: session.Now(),
	}); err != nil {
		t.Fatalf("set capability: %v", err)
	}
	store := session.NewStore(database)
	reg := provider.NewRegistry()
	reg.Register(mock)

	tools := tool.NewRegistry()
	tools.Register(cacheIdentityGrepTool{})
	tools.Register(tool.NewUpdateIntentTool())

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

	return mock.snapshots()
}

// messageCarriesSummary reports whether the message's content mentions the
// compaction summary.
func messageCarriesSummary(m provider.ModelMessage) bool {
	if m.Content == nil {
		return false
	}
	var text string
	if json.Unmarshal(m.Content, &text) == nil {
		return strings.Contains(text, compactionSummaryMarker)
	}
	return strings.Contains(string(m.Content), compactionSummaryMarker)
}

// assertSameWirePrefix pins the contract: the compacted request and the step
// that follows it must agree on System and share their whole message prefix, so
// the provider's longest-common-prefix cache keeps the verbatim tail.
func assertSameWirePrefix(t *testing.T, compacted, next requestSnapshot) {
	t.Helper()

	if !reflect.DeepEqual(compacted.system, next.system) {
		t.Fatalf("compacted step and the step after it send different system prompts\ncompacted: %v\nnext:      %v",
			compacted.system, next.system)
	}
	if len(compacted.messages) > len(next.messages) {
		t.Fatalf("compacted step sends %d messages, the step after it only %d", len(compacted.messages), len(next.messages))
	}
	for i := range compacted.messages {
		if !reflect.DeepEqual(compacted.messages[i], next.messages[i]) {
			a, _ := json.Marshal(compacted.messages[i])
			b, _ := json.Marshal(next.messages[i])
			t.Fatalf("message %d differs between the compacted step and the step after it\ncompacted: %s\nnext:      %s", i, a, b)
		}
	}
}

// TestRunLoop_ReactiveCompactionKeepsRequestIdentity covers compaction triggered
// by a context-length error. The retry that compaction produces must already be
// the body the next step will send: the summary folded into the turn's first
// user message (not appended to System), and the watermark advanced so the next
// step does not rebuild the full history it just summarized away.
func TestRunLoop_ReactiveCompactionKeepsRequestIdentity(t *testing.T) {
	const window = 40000
	mock := &cacheIdentityProvider{
		summary: e2eSummary,
		// Five tool rounds build a turn long enough (≥ 9 messages) for the
		// compaction boundary to exist; the sixth call overflows; the retry and
		// the step after it are ordinary rounds.
		grepOn:   func(call int) bool { return call <= 5 || call == 7 },
		overflow: 6,
		body:     "This model's maximum context length is 40000 tokens. However, you requested 50000 tokens.",
	}

	reqs := agentOnly(runCacheIdentityTurn(t, mock, window))
	if len(reqs) < 8 {
		t.Fatalf("expected at least 8 agent requests, got %d", len(reqs))
	}

	overflowReq := reqs[5]
	if got := len(overflowReq.messages); got < 9 {
		t.Fatalf("overflow request carried %d messages; the turn must be long enough for a compaction boundary", got)
	}
	compacted, next := reqs[6], reqs[7]

	if systemMentionsIn(compacted.system, compactionSummaryMarker) {
		t.Fatalf("compacted retry still parks the summary in the system prompt: %v", compacted.system)
	}
	if len(compacted.messages) == 0 || !messageCarriesSummary(compacted.messages[0]) {
		t.Fatalf("compacted retry does not fold the summary into the turn's first user message")
	}

	assertSameWirePrefix(t, compacted, next)

	// The watermark must outlive the step: without it the next step rebuilds the
	// whole turn (the overflow request plus the retry's two new messages).
	if got, full := len(next.messages), len(overflowReq.messages)+2; got >= full {
		t.Fatalf("the step after the retry rebuilt the full turn: %d messages, full history would be %d", got, full)
	}
	if systemMentionsIn(next.system, compactionSummaryMarker) {
		t.Fatalf("the step after the retry re-appends the summary to the system prompt: %v", next.system)
	}
	if !messageCarriesSummary(next.messages[0]) {
		t.Fatalf("the step after the retry does not carry the summary in the turn's first user message")
	}
}

// TestRunLoop_ProactiveCompactionKeepsRequestIdentity covers the pre-send,
// size-based path. Same contract: the request the loop sends after compacting is
// the body the following step will send.
func TestRunLoop_ProactiveCompactionKeepsRequestIdentity(t *testing.T) {
	const window = 40000 // keep 8 recent, compact above 20000 tokens
	mock := &cacheIdentityProvider{
		summary: e2eSummary,
		grepOn:  func(call int) bool { return call <= 6 },
		// A real provider reports the exact prompt size; reporting it on calls 4
		// and 5 puts the compaction boundary at the two steps where the working
		// set is finally long enough for the watermark to drop something (len 9
		// then len 11). Step 5's compaction can drop nothing (boundary b=1), step
		// 6's drops the first tool round (b=3) — the last two requests are then
		// the compacted step and the step after it.
		usageOn: map[int]int{4: 50000, 5: 50000},
	}

	reqs := agentOnly(runCacheIdentityTurn(t, mock, window))
	if len(reqs) < 3 {
		t.Fatalf("expected at least 3 agent requests, got %d", len(reqs))
	}
	compacted, next := reqs[len(reqs)-2], reqs[len(reqs)-1]

	if systemMentionsIn(compacted.system, compactionSummaryMarker) {
		t.Fatalf("the compacted request still parks the summary in the system prompt: %v", compacted.system)
	}
	if len(compacted.messages) == 0 || !messageCarriesSummary(compacted.messages[0]) {
		t.Fatalf("the compacted request does not fold the summary into the turn's first user message")
	}
	assertSameWirePrefix(t, compacted, next)
	if systemMentionsIn(next.system, compactionSummaryMarker) {
		t.Fatalf("the step after the compacted request re-appends the summary to the system prompt: %v", next.system)
	}
}

// systemMentionsIn reports whether any system entry contains s.
func systemMentionsIn(entries []string, s string) bool {
	for _, entry := range entries {
		if strings.Contains(entry, s) {
			return true
		}
	}
	return false
}
