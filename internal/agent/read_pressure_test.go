package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// reminderMarker is the phrase the assertions key off. It is the one sentence a
// reader would look for in a transcript to tell whether the nudge fired.
const reminderMarker = "Context pressure:"

func TestReadPressureThresholdTokens(t *testing.T) {
	tests := []struct {
		name             string
		compactThreshold int
		want             int
	}{
		{"share of a typical unknown window", 108000, 32400},
		{"capped for very large windows", 380000, readPressureCeilTokens},
		{"never more than half the compaction threshold", 10000, 3000},
		{"floored so tiny windows still produce a number", 4000, readPressureFloorTokens},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := readPressureThresholdTokens(tt.compactThreshold); got != tt.want {
				t.Fatalf("readPressureThresholdTokens(%d) = %d, want %d", tt.compactThreshold, got, tt.want)
			}
		})
	}
}

// readStep drives one step of n content-returning results through the tracker and
// returns the outputs as the store would have recorded them.
func readStep(rp *readPressure, toolName string, outputs ...string) []string {
	got := make([]string, 0, len(outputs))
	for _, out := range outputs {
		res := tool.Result{Output: out}
		rp.observe(toolName, &res)
		got = append(got, res.Output)
	}
	rp.endStep()
	return got
}

// chunk builds text whose estimated token cost is at least want.
func chunk(t *testing.T, want int) string {
	t.Helper()
	const unit = "internal/agent/loop.go:120: func (lr *LoopRunner) RunLoop(ctx context.Context)\n"
	per := estimateTokens(unit)
	n := want/per + 1
	// estimateTokens averages two heuristics and rounds, so repeating a unit
	// undershoots its own per-unit estimate slightly. Grow until it clears want.
	for i := 0; i < 8; i++ {
		s := strings.Repeat(unit, n)
		if estimateTokens(s) >= want {
			return s
		}
		n += n/16 + 1
	}
	t.Fatalf("chunk(%d) never reached the requested token count", want)
	return ""
}

func TestReadPressure_SilentUntilBothGatesAreMet(t *testing.T) {
	rp := newReadPressure(108000) // threshold 32400 tokens

	t.Run("volume without enough steps says nothing", func(t *testing.T) {
		rp := newReadPressure(108000)
		rp.setOffered(true)
		// One step, far past the byte threshold: a single big read is not a
		// reading phase, and there is nothing earlier to summarize away.
		out := readStep(rp, "read", chunk(t, 100000))
		if strings.Contains(out[0], reminderMarker) {
			t.Error("reminded after a single step")
		}
		if rp.armed {
			t.Error("armed after a single step")
		}
	})

	t.Run("steps without enough volume say nothing", func(t *testing.T) {
		rp.setOffered(true)
		for i := 0; i < 6; i++ {
			out := readStep(rp, "read", chunk(t, 100))
			if strings.Contains(out[0], reminderMarker) {
				t.Fatalf("reminded on step %d with almost nothing read", i+1)
			}
		}
	})
}

func TestReadPressure_RemindsOnTheNextReadOnce(t *testing.T) {
	rp := newReadPressure(108000)
	rp.setOffered(true)

	// Three steps well past the threshold: the arming condition is met at the
	// end of the third, so nothing during those steps carries the reminder.
	for i := 0; i < 3; i++ {
		for _, out := range readStep(rp, "read", chunk(t, 15000)) {
			if strings.Contains(out, reminderMarker) {
				t.Fatalf("reminder attached during the accumulating steps (step %d)", i+1)
			}
		}
	}
	if !rp.armed {
		t.Fatal("not armed after three steps well past the threshold")
	}

	// The reminder lands on the NEXT content-returning result, and on only one
	// of them even when the step runs several reads in parallel.
	outs := readStep(rp, "read", chunk(t, 10), chunk(t, 10), chunk(t, 10))
	hits := 0
	for _, out := range outs {
		if strings.Contains(out, reminderMarker) {
			hits++
		}
	}
	if hits != 1 {
		t.Fatalf("reminder attached to %d of 3 parallel results, want exactly 1", hits)
	}
	if !strings.Contains(outs[0], "compact_context") {
		t.Error("the reminder does not name the tool it is asking for")
	}
	if rp.armed {
		t.Error("still armed after the reminder fired")
	}

	// And it does not repeat on the step after, having already been said.
	for _, out := range readStep(rp, "read", chunk(t, 10)) {
		if strings.Contains(out, reminderMarker) {
			t.Error("reminder repeated immediately")
		}
	}
}

func TestReadPressure_MarksTheReminderAsHarnessInjected(t *testing.T) {
	rp := newReadPressure(108000)
	rp.setOffered(true)
	for i := 0; i < 3; i++ {
		readStep(rp, "read", chunk(t, 15000))
	}
	res := tool.Result{Output: "hit"}
	rp.observe("grep", &res)
	if ok, _ := res.Metadata["contextPressureReminder"].(bool); !ok {
		t.Fatal("the appended reminder is indistinguishable from the tool's own output")
	}
}

func TestReadPressure_WithheldWhileCompactContextIsNotOffered(t *testing.T) {
	rp := newReadPressure(108000)
	rp.setOffered(false) // caching endpoint: the tool is not on the menu

	for i := 0; i < 4; i++ {
		for _, out := range readStep(rp, "read", chunk(t, 15000)) {
			if strings.Contains(out, reminderMarker) {
				t.Fatal("told the agent to call a tool it was never given")
			}
		}
	}
	if !rp.armed {
		t.Fatal("pressure should still be armed, waiting for the tool to be offered")
	}

	// Once the tool is offered, the reminder that was held back is delivered.
	rp.setOffered(true)
	if out := readStep(rp, "read", chunk(t, 10)); !strings.Contains(out[0], reminderMarker) {
		t.Error("reminder was not delivered on the first step that offered the tool")
	}
}

func TestReadPressure_IgnoresWhatDoesNotEnterContext(t *testing.T) {
	rp := newReadPressure(108000)
	rp.setOffered(true)

	// Writing a large file costs nothing on the way back — the content went out,
	// not in — so it must not count toward reading pressure.
	for i := 0; i < 6; i++ {
		readStep(rp, "write", chunk(t, 30000))
		readStep(rp, "edit", chunk(t, 30000))
	}
	if rp.tokens != 0 || rp.readSteps != 0 {
		t.Fatalf("mutating tools counted as reading: %d tokens over %d steps", rp.tokens, rp.readSteps)
	}

	// A denied call never ran, so its refusal text is not content either.
	for i := 0; i < 6; i++ {
		res := tool.Result{Output: chunk(t, 30000), Denied: true}
		rp.observe("read", &res)
		rp.endStep()
	}
	if rp.tokens != 0 {
		t.Fatalf("a denied call counted %d tokens of reading", rp.tokens)
	}
}

func TestReadPressure_CountsMCPToolsAsReading(t *testing.T) {
	if !isContentReturningTool("mcp_github_get_issue") {
		t.Error("MCP results do not count; they are exactly the unbounded outside content that fills a context")
	}
	if isContentReturningTool("write") || isContentReturningTool("compact_context") {
		t.Error("a non-reading tool counted toward reading pressure")
	}
}

func TestReadPressure_BoundedPerTurnAndResetByCompaction(t *testing.T) {
	rp := newReadPressure(108000)
	rp.setOffered(true)

	fired := 0
	for step := 0; step < 60; step++ {
		for _, out := range readStep(rp, "read", chunk(t, 15000)) {
			if strings.Contains(out, reminderMarker) {
				fired++
			}
		}
	}
	if fired != readPressureMaxNudges {
		t.Fatalf("reminded %d times over 60 reading steps, want the cap of %d", fired, readPressureMaxNudges)
	}

	// Compacting is what the reminder asks for; afterwards the counted content is
	// no longer being sent, so the tracker starts over.
	rp.reset()
	if rp.tokens != 0 || rp.readSteps != 0 || rp.nudges != 0 || rp.armed || rp.nextAt != rp.base {
		t.Fatalf("reset left state behind: %+v", *rp)
	}
	for step := 0; step < 3; step++ {
		readStep(rp, "read", chunk(t, 15000))
	}
	if !rp.armed {
		t.Error("the tracker did not re-arm after a compaction and a fresh reading phase")
	}
}

// bigGrepTool stands in for a search that pulls a real chunk of the codebase into
// context on every call.
type bigGrepTool struct{}

func (bigGrepTool) ID() string          { return "grep" }
func (bigGrepTool) Description() string { return "big grep (test)" }
func (bigGrepTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (bigGrepTool) Execute(ctx context.Context, args json.RawMessage, tctx tool.Context) (tool.Result, error) {
	var b strings.Builder
	for i := 0; i < 1400; i++ {
		fmt.Fprintf(&b, "internal/agent/loop.go:%d: func (lr *LoopRunner) step(ctx context.Context)\n", i)
	}
	return tool.Result{Title: "grep", Output: b.String()}, nil
}

// TestRunLoop_ReadPressureReminderReachesTheModel is the wiring test: the
// reminder is only worth anything if it survives into the request the provider
// actually receives. The loop rebuilds the model-facing history from the store on
// every step, so a reminder that was not written into the stored tool output
// would be silently dropped — invisible to every unit test above.
func TestRunLoop_ReadPressureReminderReachesTheModel(t *testing.T) {
	t.Run("delivered on a non-caching endpoint", func(t *testing.T) {
		reqs := runReadPressureScript(t, provider.CacheAbsent, 8)
		hits := countReminders(reqs[len(reqs)-1])
		if hits == 0 {
			t.Fatal("no read-pressure reminder reached the model after 8 large search rounds")
		}
		if hits > readPressureMaxNudges {
			t.Errorf("%d reminders in one request, want at most %d", hits, readPressureMaxNudges)
		}
		// The early requests must be clean: nudging before the agent has read
		// enough to have anything worth summarizing is noise, not guidance.
		if got := countReminders(reqs[1]); got != 0 {
			t.Errorf("%d reminders in the second request, before the threshold could be reached", got)
		}
	})

	t.Run("silent on a caching endpoint", func(t *testing.T) {
		reqs := runReadPressureScript(t, provider.CacheSupported, 8)
		for i, req := range reqs {
			if got := countReminders(req); got != 0 {
				t.Errorf("request %d carries %d reminders on a caching endpoint, where compact_context is withheld", i+1, got)
			}
		}
	})
}

// countReminders counts tool results in one request that carry the reminder.
func countReminders(msgs []provider.ModelMessage) int {
	n := 0
	for _, m := range msgs {
		if m.Role != "tool" || m.Content == nil {
			continue
		}
		var text string
		if json.Unmarshal(m.Content, &text) != nil {
			continue
		}
		if strings.Contains(text, reminderMarker) {
			n++
		}
	}
	return n
}

// runReadPressureScript drives a turn of `rounds` large search rounds against an
// endpoint with the given cache verdict and returns every request made.
func runReadPressureScript(t *testing.T, verdict provider.CacheVerdict, rounds int) [][]provider.ModelMessage {
	t.Helper()
	resetCacheVerdicts()
	t.Cleanup(resetCacheVerdicts)

	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if err := session.SetModelCapability(database, &session.ModelCapability{
		ModelID: "mock-model", SupportsImages: false, ProbedAt: session.Now(),
	}); err != nil {
		t.Fatalf("set capability: %v", err)
	}
	if err := session.SetModelCacheSupport(database, "mock-model", "mock", string(verdict), session.Now()); err != nil {
		t.Fatalf("seed cache verdict: %v", err)
	}

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	mock := &toolRoundsProvider{rounds: rounds}
	reg.Register(mock)

	tools := tool.NewRegistry()
	tools.Register(bigGrepTool{})
	tools.Register(tool.NewCompactContextTool())

	lr := &LoopRunner{
		Store: store, Bus: bus.New(256), Registry: reg, Tools: tools,
		Dir: t.TempDir(), MaxSteps: 40,
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
	textData, _ := json.Marshal(session.TextPartData{Text: "find every call site"})
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
	case <-time.After(30 * time.Second):
		t.Fatal("RunLoop did not complete in time")
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	return mock.messages
}
