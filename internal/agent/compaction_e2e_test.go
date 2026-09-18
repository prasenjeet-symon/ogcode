package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
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
	reqs, toolLists := runCompactionScript(t)

	if len(reqs) != 4 {
		t.Fatalf("expected 4 provider calls, got %d", len(reqs))
	}

	// The tool must actually have been offered, or the whole turn proves nothing.
	if !containsString(toolLists[0], "compact_context") {
		t.Fatalf("compact_context was not offered; tools were %v", toolLists[0])
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

func TestRunLoop_RejectedSummaryLeavesContextIntact(t *testing.T) {
	// A summary the tool refuses must not move the watermark. Dropping history
	// for a compaction that never happened would truncate the agent mid-turn
	// while it believes nothing changed.
	reqs, toolLists := runCompactionScriptWithSummary(t, "too short")

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
// compact_context call, then a final answer — and returns the messages and tool
// lists of every request.
func runCompactionScript(t *testing.T) ([][]provider.ModelMessage, [][]string) {
	t.Helper()
	return runCompactionScriptWithSummary(t, e2eSummary)
}

func runCompactionScriptWithSummary(t *testing.T, summary string) ([][]provider.ModelMessage, [][]string) {
	t.Helper()
	return runCompactionScriptFull(t, summary, true, nil)
}

// runCompactionScriptFull additionally controls whether compact_context is
// actually registered, so a test can drive the case where the tool is offered
// but its execution fails. tweak, when non-nil, adjusts the LoopRunner before
// the turn starts — used to drive the per-project setting that withholds the
// tool.
func runCompactionScriptFull(t *testing.T, summary string, registerTool bool, tweak func(*LoopRunner)) ([][]provider.ModelMessage, [][]string) {
	t.Helper()

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
	if tweak != nil {
		tweak(lr)
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
	reqs, _ := runCompactionScriptFull(t, e2eSummary, false, nil)

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
	runCompactionScript(t)
	if !systemMentions(0, "Reclaiming Your Own Context") {
		t.Error("compact_context was offered without its guidance in the system prompt")
	}
	if !systemMentions(0, "Your summary is the only thing that survives") {
		t.Error("guidance is present but missing the warning that carries the real risk")
	}
}

// TestRunLoop_ProjectSettingWithholdsCompactContext drives the same four-step
// script in a project that has the setting turned off. The model still asks to
// compact — the script is fixed — and the turn must simply carry on with its
// full history, because the tool was never on the wire to begin with.
func TestRunLoop_ProjectSettingWithholdsCompactContext(t *testing.T) {
	reqs, toolLists := runCompactionScriptFull(t, e2eSummary, true, func(lr *LoopRunner) {
		lr.CompactContextEnabled = func() bool { return false }
	})

	for step, tools := range toolLists {
		if containsString(tools, "compact_context") {
			t.Errorf("step %d offered compact_context although the project has it disabled: %v", step, tools)
		}
	}

	// The guidance names a tool the agent does not have; sending it would spend
	// context telling the model about something it cannot call.
	for step := range lastSystems {
		if systemMentions(step, "Reclaiming Your Own Context") {
			t.Errorf("step %d carried the compact_context guidance with the tool disabled", step)
		}
	}

	// Nothing was narrowed: the earlier rounds are still in the final request.
	last := reqs[len(reqs)-1]
	uses := toolUseIDs(last)
	for _, kept := range []string{"call_1", "call_2"} {
		if !containsString(uses, kept) {
			t.Errorf("%s was dropped even though compaction is disabled for this project", kept)
		}
	}
	var lead string
	if json.Unmarshal(last[0].Content, &lead) == nil && strings.Contains(lead, "compacted to reclaim context") {
		t.Error("a compaction summary was prepended although the tool was never offered")
	}
}

// The default — no source wired, as in the CLI and every test that predates the
// setting — must keep offering the tool.
func TestCompactContextAllowedDefaultsToEnabled(t *testing.T) {
	if !(&LoopRunner{}).compactContextAllowed() {
		t.Error("a LoopRunner with no CompactContextEnabled source should allow compaction")
	}
	if (&LoopRunner{CompactContextEnabled: func() bool { return false }}).compactContextAllowed() {
		t.Error("an explicit false should withhold compaction")
	}
}

// The project setting is resolved ONCE per turn, never per step. It decides both
// a tool on the wire and a ~2.5KB system entry, and on OpenAI/Ollama every system
// entry is joined into messages[0] where the longest-common-prefix cache lives —
// so a value that changed between steps would hand the model a different tool
// list and a different system prompt mid-turn, costing the cache for the rest of
// it. The read also fails OPEN (a DB error reports the default), which is exactly
// how a single transient error could have flipped one step.
//
// The closure here flips on every call. If the loop read it per step, the steps
// would disagree; resolved per turn, it is called once and every step matches.
func TestRunLoop_CompactContextSettingIsReadOncePerTurn(t *testing.T) {
	calls := 0
	_, toolLists := runCompactionScriptFull(t, e2eSummary, true, func(lr *LoopRunner) {
		lr.CompactContextEnabled = func() bool {
			calls++
			return calls%2 == 1 // true, false, true, false, ...
		}
	})

	if calls != 1 {
		t.Errorf("CompactContextEnabled called %d times; a turn must resolve it once", calls)
	}
	if len(toolLists) < 2 {
		t.Fatalf("need at least 2 steps to compare, got %d", len(toolLists))
	}
	first := containsString(toolLists[0], "compact_context")
	for step, tools := range toolLists {
		if containsString(tools, "compact_context") != first {
			t.Errorf("step %d disagrees with step 0 about whether compact_context is offered "+
				"— the tool list changed mid-turn, which breaks the cached prefix on every provider: %v",
				step, tools)
		}
	}

	// And the guidance block must track the tool list exactly, or the system
	// prompt churns even when the tool list does not.
	for step := range lastSystems {
		if systemMentions(step, "Reclaiming Your Own Context") != first {
			t.Errorf("step %d disagrees with step 0 about the compact_context guidance block", step)
		}
	}
}

// The cache-critical half of the contract: while the registry is unchanged, every
// step of a turn must be offered the byte-identical tool array. Tool definitions
// are part of the cached prompt prefix on every provider — on Anthropic they LEAD
// it (tools → system → messages), so any churn there invalidates the
// cache_control'd system block and the entire message history along with it.
func TestRunLoop_ToolsetIsIdenticalAcrossStepsWhenRegistryIsUnchanged(t *testing.T) {
	_, toolLists := runCompactionScriptFull(t, e2eSummary, true, func(lr *LoopRunner) {
		// Several glob-matched tools, so map-iteration order would be visible if
		// ForAgent ever stopped sorting.
		for i := 0; i < 12; i++ {
			lr.Tools.Register(noopNamedTool{fmt.Sprintf("mcp_srv_%02d", i)})
		}
	})

	if len(toolLists) < 2 {
		t.Fatalf("need at least 2 steps to compare, got %d", len(toolLists))
	}
	if !containsString(toolLists[0], "mcp_srv_00") {
		t.Fatalf("mcp_* did not expand; this test would prove nothing: %v", toolLists[0])
	}

	want := strings.Join(toolLists[0], ",")
	for step, tools := range toolLists {
		if got := strings.Join(tools, ","); got != want {
			t.Errorf("step %d differs from step 0 with an unchanged registry\n  step 0: %s\n  step %d: %s",
				step, want, step, got)
		}
	}

	var mcps []string
	for _, id := range toolLists[0] {
		if strings.HasPrefix(id, "mcp_") {
			mcps = append(mcps, id)
		}
	}
	if !sort.StringsAreSorted(mcps) {
		t.Errorf("expanded MCP tools are not sorted: %v", mcps)
	}
}

// The availability half. A pure snapshot would have been wrong: mid-loop guidance
// does not start a new turn, so a user steering a working agent keeps feeding the
// same RunLoop — and a turn can run to 30,000 steps. The case that actually bites
// is enabling an MCP server *because* the agent needs it right now. So a real
// registry change must be picked up mid-turn, exactly once.
func TestRunLoop_ToolsetPicksUpARegistryChangeMidTurn(t *testing.T) {
	const lateTool = "mcp_late_arrival"

	_, toolLists := runCompactionScriptFull(t, e2eSummary, true, func(lr *LoopRunner) {
		lr.Tools.Register(noopNamedTool{"mcp_early"})
		// Overrides the harness's grep with one that registers a tool the first
		// time it runs — a deterministic stand-in for an MCP server finishing its
		// dial between two steps. The script calls grep on steps 1 and 2.
		lr.Tools.Register(&registeringGrepTool{reg: lr.Tools, id: lateTool})
	})

	if len(toolLists) < 3 {
		t.Fatalf("need at least 3 steps, got %d", len(toolLists))
	}
	if containsString(toolLists[0], lateTool) {
		t.Fatalf("step 0 already had %s; the test cannot show it being picked up", lateTool)
	}
	last := toolLists[len(toolLists)-1]
	if !containsString(last, lateTool) {
		t.Errorf("a tool registered mid-turn never reached the agent: final step had %v", last)
	}
	// And the earlier tools must survive the re-resolve.
	if !containsString(last, "mcp_early") {
		t.Errorf("re-resolve dropped a pre-existing tool: %v", last)
	}
}

// noopNamedTool is a registerable stand-in for an MCP-contributed tool.
type noopNamedTool struct{ id string }

func (n noopNamedTool) ID() string                  { return n.id }
func (n noopNamedTool) Description() string         { return "noop" }
func (n noopNamedTool) Parameters() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (n noopNamedTool) Execute(ctx context.Context, args json.RawMessage, tctx tool.Context) (tool.Result, error) {
	return tool.Result{Output: "ok"}, nil
}

// registeringGrepTool answers "grep" and, on its first call only, registers a new
// tool — mimicking an MCP server that finishes connecting partway through a turn.
type registeringGrepTool struct {
	reg  *tool.Registry
	id   string
	once sync.Once
}

func (g *registeringGrepTool) ID() string          { return "grep" }
func (g *registeringGrepTool) Description() string { return "grep" }
func (g *registeringGrepTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (g *registeringGrepTool) Execute(ctx context.Context, args json.RawMessage, tctx tool.Context) (tool.Result, error) {
	g.once.Do(func() { g.reg.Register(noopNamedTool{g.id}) })
	return tool.Result{Output: "no matches"}, nil
}
