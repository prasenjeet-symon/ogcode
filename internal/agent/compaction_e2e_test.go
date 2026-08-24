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

const e2eSummary = "Task: wire the refresh path. Established that middleware/auth.go:40-120 holds " +
	"the token check and that the session store needs no change. Ruled out touching the store. " +
	"Remaining: write the refresh handler and its test."

// compactionScriptProvider drives a fixed four-step turn: two ordinary tool
// rounds, then a compact_context call, then a final text answer. It records the
// messages of every request so the test can compare the request made before the
// compaction with the one made after it.
type compactionScriptProvider struct {
	mu       sync.Mutex
	summary  string
	calls    int
	messages [][]provider.ModelMessage
	tools    [][]string
	systems  [][]string
}

func (m *compactionScriptProvider) ID() string { return "mock" }
func (m *compactionScriptProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", ProviderID: "mock"}}
}

func (m *compactionScriptProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	m.mu.Lock()
	m.calls++
	call := m.calls
	snapshot := make([]provider.ModelMessage, len(req.Messages))
	copy(snapshot, req.Messages)
	m.messages = append(m.messages, snapshot)
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
		emit := func(name string, input string) {
			callID := fmt.Sprintf("call_%d", call)
			ch <- provider.StreamEvent{Type: provider.EventToolCallStart, ToolCallID: callID, ToolName: name}
			ch <- provider.StreamEvent{Type: provider.EventToolCallDelta, ToolCallID: callID, ToolInput: []byte(input)}
			ch <- provider.StreamEvent{Type: provider.EventToolCallEnd, ToolCallID: callID}
			fr := "tool_use"
			ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
		}
		switch call {
		case 1, 2:
			emit("grep", `{}`)
		case 3:
			args, _ := json.Marshal(map[string]string{"summary": m.summary})
			emit("compact_context", string(args))
		default:
			ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "done"}
			fr := "stop"
			ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
		}
	}()
	return ch, nil
}

func TestRunLoop_CompactContextNarrowsTheNextRequest(t *testing.T) {
	reqs, toolLists := runCompactionScript(t, provider.CacheAbsent)

	if len(reqs) != 4 {
		t.Fatalf("expected 4 provider calls, got %d", len(reqs))
	}

	// The tool must actually have been offered, or the whole turn proves nothing.
	if !containsString(toolLists[0], "compact_context") {
		t.Fatalf("compact_context was not offered on a non-caching endpoint; tools were %v", toolLists[0])
	}

	before, after := reqs[2], reqs[3]

	// The compact_context call must have actually RUN. executeTool applies the
	// agent's tool list as an allowlist, so a dynamically-offered tool that is
	// not in that list is rejected — the call appears to succeed while nothing
	// happens. Its tool_result carries the refusal text when that occurs.
	if refusal := toolResultText(reqs[3], "call_3"); strings.Contains(refusal, "not available") {
		t.Fatalf("compact_context was offered but rejected at execution: %q", refusal)
	}

	// Before compacting, the two earlier grep rounds are in the request.
	if got := len(toolUseIDs(before)); got != 2 {
		t.Fatalf("pre-compaction request should carry 2 tool_use blocks, got %d", got)
	}

	// After compacting, everything before the compact_context call is gone.
	uses := toolUseIDs(after)
	for _, dropped := range []string{"call_1", "call_2"} {
		if containsString(uses, dropped) {
			t.Errorf("%s survived the compaction — the watermark did not narrow the request", dropped)
		}
	}
	if !containsString(uses, "call_3") {
		t.Error("the compact_context call itself was dropped; its tool_result is now orphaned")
	}

	// Structural validity: the request must open with a user turn and pair every
	// tool_use with its tool_result, or a real provider rejects it outright.
	if after[0].Role != "user" {
		t.Errorf("post-compaction request opens with role %q, want user", after[0].Role)
	}
	var lead string
	if err := json.Unmarshal(after[0].Content, &lead); err != nil {
		t.Fatalf("leading message content is not a JSON string: %v", err)
	}
	if !strings.Contains(lead, e2eSummary) {
		t.Error("the agent's summary is missing from the narrowed request")
	}
	results := toolResultIDs(after)
	for _, id := range uses {
		if !results[id] {
			t.Errorf("tool_use %q has no tool_result in the post-compaction request", id)
		}
	}

}

func TestRunLoop_CompactContextWithheldOnACachingEndpoint(t *testing.T) {
	reqs, toolLists := runCompactionScript(t, provider.CacheSupported)

	// On a caching endpoint compacting is a net loss, so the tool must not be on
	// the menu at all — the agent should never be tempted to invalidate a prefix
	// it is being billed at a discount for.
	for i, names := range toolLists {
		if containsString(names, "compact_context") {
			t.Errorf("step %d offered compact_context on a caching endpoint: %v", i+1, names)
		}
	}
	// And with the tool withheld, the call is refused and nothing is narrowed:
	// the last request must still carry the earlier tool rounds.
	last := reqs[len(reqs)-1]
	if got := len(toolUseIDs(last)); got < 2 {
		t.Errorf("final request carries %d tool_use blocks; history was narrowed despite the tool being withheld", got)
	}
}

func TestRunLoop_RejectedSummaryLeavesContextIntact(t *testing.T) {
	// A summary the tool refuses must not move the watermark. Dropping history
	// for a compaction that never happened would truncate the agent mid-turn
	// while it believes nothing changed.
	reqs, toolLists := runCompactionScriptWithSummary(t, provider.CacheAbsent, "too short")

	if !containsString(toolLists[0], "compact_context") {
		t.Fatalf("compact_context was not offered; tools were %v", toolLists[0])
	}
	last := reqs[len(reqs)-1]
	uses := toolUseIDs(last)
	for _, kept := range []string{"call_1", "call_2"} {
		if !containsString(uses, kept) {
			t.Errorf("%s was dropped after a REJECTED compaction — context truncated for work that never happened", kept)
		}
	}
	var lead string
	if json.Unmarshal(last[0].Content, &lead) == nil && strings.Contains(lead, "compacted to reclaim context") {
		t.Error("a compaction summary was prepended even though the tool rejected it")
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// runCompactionScript drives a full four-step turn — two tool rounds, a
// compact_context call, then a final answer — against an endpoint with the
// given cache verdict, and returns the messages and tool lists of every request.
func runCompactionScript(t *testing.T, verdict provider.CacheVerdict) ([][]provider.ModelMessage, [][]string) {
	t.Helper()
	return runCompactionScriptWithSummary(t, verdict, e2eSummary)
}

func runCompactionScriptWithSummary(t *testing.T, verdict provider.CacheVerdict, summary string) ([][]provider.ModelMessage, [][]string) {
	t.Helper()
	return runCompactionScriptFull(t, verdict, summary, true)
}

// runCompactionScriptFull additionally controls whether compact_context is
// actually registered, so a test can drive the case where the tool is offered
// but its execution fails.
func runCompactionScriptFull(t *testing.T, verdict provider.CacheVerdict, summary string, registerTool bool) ([][]provider.ModelMessage, [][]string) {
	t.Helper()
	resetCacheVerdicts()
	t.Cleanup(resetCacheVerdicts)

	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	// Pre-seed so no probe call offsets the request counter.
	if err := session.SetModelCapability(database, &session.ModelCapability{
		ModelID: "mock-model", SupportsImages: false, ProbedAt: session.Now(),
	}); err != nil {
		t.Fatalf("set capability: %v", err)
	}
	// Stand in for an endpoint whose caching behaviour is already known, so the
	// tool decision is made on step 1 rather than after an observation window.
	if err := session.SetModelCacheSupport(database, "mock-model", "mock", string(verdict), session.Now()); err != nil {
		t.Fatalf("seed cache verdict: %v", err)
	}

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	mock := &compactionScriptProvider{summary: summary}
	reg.Register(mock)

	tools := tool.NewRegistry()
	tools.Register(noopGrepTool{})
	if registerTool {
		tools.Register(tool.NewCompactContextTool())
	}

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

	// Compaction narrows the model's view, never the record.
	msgs, gerr := store.GetMessages(sess.ID, "", 100)
	if gerr != nil {
		t.Fatalf("get messages: %v", gerr)
	}
	if len(msgs) < 6 {
		t.Errorf("stored conversation has %d messages; compaction must not delete history", len(msgs))
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	lastSystems = mock.systems
	return mock.messages, mock.tools
}

// lastSystems holds the system-prompt entries of the most recent scripted run,
// so a test can assert on guidance without threading another return value
// through every call site.
var lastSystems [][]string

// systemMentions reports whether any system entry of step i contains s.
func systemMentions(step int, s string) bool {
	if step >= len(lastSystems) {
		return false
	}
	for _, entry := range lastSystems[step] {
		if strings.Contains(entry, s) {
			return true
		}
	}
	return false
}

// toolResultText returns the text of the tool_result answering a given call.
func toolResultText(msgs []provider.ModelMessage, callID string) string {
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID == callID {
			var text string
			if json.Unmarshal(m.Content, &text) == nil {
				return text
			}
			return string(m.Content)
		}
	}
	return ""
}

func TestRunLoop_WatermarkRequiresTheToolToHaveActuallyRun(t *testing.T) {
	// The summary here is perfectly valid, so the loop's own parse of the tool
	// arguments succeeds. What fails is the execution: compact_context is offered
	// but not registered, so executeTool errors and the call never runs.
	//
	// This is the case the "compacted" success marker exists for. Without it the
	// loop would record a watermark on the strength of the call having been made,
	// and drop the whole turn's history for a compaction that never happened —
	// truncating the agent mid-turn while it believes nothing changed.
	reqs, _ := runCompactionScriptFull(t, provider.CacheAbsent, e2eSummary, false)

	last := reqs[len(reqs)-1]
	uses := toolUseIDs(last)
	for _, kept := range []string{"call_1", "call_2"} {
		if !containsString(uses, kept) {
			t.Errorf("%s was dropped although compact_context never executed", kept)
		}
	}
	var lead string
	if json.Unmarshal(last[0].Content, &lead) == nil && strings.Contains(lead, "compacted to reclaim context") {
		t.Error("a compaction summary was prepended for a call that never ran")
	}
}

func TestRunLoop_CompactContextGuidanceShipsWithTheTool(t *testing.T) {
	// The tool and its guidance must travel together. A tool offered with no
	// explanation of when to use it, or guidance describing a tool the agent was
	// never given, are both worse than shipping neither.
	runCompactionScript(t, provider.CacheAbsent)
	if !systemMentions(0, "Reclaiming Your Own Context") {
		t.Error("compact_context was offered without its guidance in the system prompt")
	}
	if !systemMentions(0, "Your summary is the only thing that survives") {
		t.Error("guidance is present but missing the warning that carries the real risk")
	}

	runCompactionScript(t, provider.CacheSupported)
	if systemMentions(0, "Reclaiming Your Own Context") {
		t.Error("guidance shipped on a caching endpoint, describing a tool that was withheld")
	}
}
