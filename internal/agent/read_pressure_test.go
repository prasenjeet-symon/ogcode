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

// reminderMarker is the phrase the volume reminder is asserted by. It is the one
// sentence a reader would look for in a transcript to tell whether the nudge
// fired.
const reminderMarker = "Context pressure:"

// resendCostMarker is the phrase the cost reminder is asserted by. The two
// triggers share the machinery but must be distinguishable in a transcript, so
// each carries its own opening line.
const resendCostMarker = "Re-send cost:"

// costWindow is the context window the tracker unit tests build with. Zero means
// "unknown", whose stand-in is fallbackMaxRequestTokens; the volume tests never
// report a step through observeContext, so their cost trigger never arms and the
// window cannot influence them.
const costWindow = 0

func TestReadPressureThresholdTokens(t *testing.T) {
	t.Setenv(readPressureThresholdEnv, "") // pin the built-in default
	if got := readPressureThresholdTokens(); got != readPressureThresholdDefault {
		t.Fatalf("readPressureThresholdTokens() = %d, want the default %d", got, readPressureThresholdDefault)
	}
}

func TestResendCostBaseTokens(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		window int
		want   int
	}{
		{"unset uses the default multiple", "", 200000, resendCostMultipleDefault * 200000},
		{"a usable multiple is taken verbatim", "4", 200000, 800000},
		{"unparseable falls back to the default", "lots", 100000, resendCostMultipleDefault * 100000},
		{"below the floor is clamped up", "0", 100000, resendCostMultipleMin * 100000},
		{"above the max is clamped down", "99999", 1000, resendCostMultipleMax * 1000},
		{"an unknown window falls back to the fixed cap", "", 0, resendCostMultipleDefault * fallbackMaxRequestTokens},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(resendCostMultipleEnv, tt.raw)
			if got := resendCostBaseTokens(tt.window); got != tt.want {
				t.Fatalf("resendCostBaseTokens(%d) with %q = %d, want %d", tt.window, tt.raw, got, tt.want)
			}
		})
	}
}

func TestReadPressureThresholdTokens_Override(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int
	}{
		{"unset uses the default", "", readPressureThresholdDefault},
		{"a usable value is taken verbatim", "90000", 90000},
		{"unparseable falls back to the default", "lots", readPressureThresholdDefault},
		{"below the floor is clamped up", "10", readPressureThresholdMin},
		{"above the max is clamped down", "999999999", readPressureThresholdMax},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(readPressureThresholdEnv, tt.raw)
			if got := readPressureThresholdTokens(); got != tt.want {
				t.Fatalf("readPressureThresholdTokens() with %q = %d, want %d", tt.raw, got, tt.want)
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

// contextStep reports one step's processed input-token count to the tracker, the
// way the loop does from the provider's usage event. It is what the cost trigger
// accumulates; a plain readStep contributes no cost.
func contextStep(rp *readPressure, inputTokens int) {
	rp.observeContext(inputTokens)
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
	t.Setenv(readPressureThresholdEnv, "32400")
	rp := newReadPressure("", costWindow) // built-in 40k threshold

	t.Run("volume without enough steps says nothing", func(t *testing.T) {
		rp := newReadPressure("", costWindow)
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
	t.Setenv(readPressureThresholdEnv, "32400")
	rp := newReadPressure("", costWindow)
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
	t.Setenv(readPressureThresholdEnv, "32400")
	rp := newReadPressure("", costWindow)
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
	t.Setenv(readPressureThresholdEnv, "32400")
	rp := newReadPressure("", costWindow)
	rp.setOffered(false) // an agent without compact_context (e.g. the index agent)

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
	rp := newReadPressure("", costWindow)
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

// TestReadPressure_KeepsRemindingUntilCompacted pins the strictness the
// reminder is meant to have: it is not advice the agent may weigh once and
// discard, so a turn that keeps reading without compacting keeps being told.
// There is no cap after which a crowded turn is left in peace.
func TestReadPressure_KeepsRemindingUntilCompacted(t *testing.T) {
	t.Setenv(readPressureThresholdEnv, "32400")
	rp := newReadPressure("", costWindow)
	rp.setOffered(true)

	fired := 0
	for step := 0; step < 60; step++ {
		for _, out := range readStep(rp, "read", chunk(t, 15000)) {
			if strings.Contains(out, reminderMarker) {
				fired++
			}
		}
	}
	// 60 reading steps at this pacing fire the reminder far more than the three
	// the old cap allowed. A handful would mean it gave up again.
	if fired < 10 {
		t.Fatalf("reminded %d times over 60 reading steps; the reminder must keep arriving until compaction", fired)
	}

	// Compacting is what the reminder asks for; afterwards the counted content is
	// no longer being sent, so the tracker starts over.
	rp.reset()
	if rp.tokens != 0 || rp.readSteps != 0 || rp.armed || rp.nextAt != rp.base {
		t.Fatalf("reset left state behind: %+v", *rp)
	}
	if rp.cost != 0 || rp.steps != 0 || rp.nextCostAt != rp.baseCost {
		t.Fatalf("reset left the cost trigger behind: %+v", *rp)
	}
	for step := 0; step < 3; step++ {
		readStep(rp, "read", chunk(t, 15000))
	}
	if !rp.armed {
		t.Error("the tracker did not re-arm after a compaction and a fresh reading phase")
	}
}

// TestReadPressure_CostTriggerArmsWithoutReading pins the second trigger: a turn
// that reads little but runs long still pays the re-send cost on every step, so
// it is reminded even though its read volume alone never would be.
func TestReadPressure_CostTriggerArmsWithoutReading(t *testing.T) {
	t.Setenv(readPressureThresholdEnv, "1000000") // volume can never arm here
	t.Setenv(resendCostMultipleEnv, "2")
	rp := newReadPressure("", 100000)
	rp.setOffered(true)

	// Steps that read a trickle each: far too little volume, but 100k of context
	// re-sent per step. Nothing fires until the accumulated cost crosses 2×100k.
	total := 0
	for i := 0; i < 2; i++ {
		contextStep(rp, 100000)
		total += 100000
		for _, out := range readStep(rp, "read", chunk(t, 10)) {
			if strings.Contains(out, reminderMarker) || strings.Contains(out, resendCostMarker) {
				t.Fatalf("reminded after %d of cost, before the threshold", total)
			}
		}
	}
	if !rp.armed {
		t.Fatal("the cost trigger did not arm at the threshold")
	}
	if rp.armedReason != reasonResendCost {
		t.Fatalf("armed for %q; the volume trigger cannot have fired here", rp.armedReason)
	}

	// It lands on the next content-returning result, and the volume reminder's
	// marker is NOT present — the two triggers must be distinguishable.
	outs := readStep(rp, "read", chunk(t, 10))
	if !strings.Contains(outs[0], resendCostMarker) {
		t.Fatalf("cost reminder not attached:\n%s", outs[0])
	}
	if strings.Contains(outs[0], reminderMarker) {
		t.Error("the cost trigger emitted the volume reminder's text")
	}
	if !strings.Contains(outs[0], "compact_context") {
		t.Error("the cost reminder does not name the tool it is asking for")
	}
}

// TestReadPressure_CostTriggerHoldsBackForAMutationOnlyStep pins the attachment
// rule: the cost reminder lands only on a result that returned content, never on
// a write or an edit, even though every step pays the re-send cost.
func TestReadPressure_CostTriggerHoldsBackForAMutationOnlyStep(t *testing.T) {
	t.Setenv(readPressureThresholdEnv, "1000000")
	t.Setenv(resendCostMultipleEnv, "2")
	rp := newReadPressure("", 100000)
	rp.setOffered(true)

	contextStep(rp, 100000)
	readStep(rp, "write", chunk(t, 10)) // a mutating step offers no attach point
	contextStep(rp, 100000)
	readStep(rp, "write", chunk(t, 10)) // this step's end is where the threshold is crossed
	if !rp.armed {
		t.Fatal("the cost trigger did not arm at the threshold")
	}

	// The reminder is still pending, waiting for a content-returning result.
	outs := readStep(rp, "write", chunk(t, 10))
	if strings.Contains(outs[0], resendCostMarker) {
		t.Fatal("the cost reminder was attached to a write result")
	}
	if !rp.armed || rp.stepNudged {
		t.Fatal("a mutating step consumed the pending reminder")
	}
	outs = readStep(rp, "read", chunk(t, 10))
	if !strings.Contains(outs[0], resendCostMarker) {
		t.Fatal("the cost reminder was not delivered on the first reading step after the mutation-only steps")
	}
}

// TestReadPressure_CostTriggerRearmsUntilCompacted mirrors the volume trigger's
// strictness for the cost trigger: it keeps arriving as the turn keeps sending
// the context, and a compaction starts it over.
func TestReadPressure_CostTriggerRearmsUntilCompacted(t *testing.T) {
	t.Setenv(readPressureThresholdEnv, "1000000")
	t.Setenv(resendCostMultipleEnv, "2")
	rp := newReadPressure("", 100000)
	rp.setOffered(true)

	fired := 0
	for step := 0; step < 40; step++ {
		contextStep(rp, 100000)
		for _, out := range readStep(rp, "read", chunk(t, 10)) {
			if strings.Contains(out, resendCostMarker) {
				fired++
			}
		}
	}
	if fired < 5 {
		t.Fatalf("the cost reminder fired %d times over 40 cost-heavy steps; it must keep arriving until compaction", fired)
	}

	rp.reset()
	if rp.cost != 0 || rp.steps != 0 || rp.armed || rp.nextCostAt != rp.baseCost {
		t.Fatalf("reset left the cost trigger behind: %+v", *rp)
	}
}

// TestReadPressure_CostTriggerWithheldWhileCompactContextIsNotOffered pins that
// the cost trigger obeys the same tool-reachability rule as the volume trigger:
// a reminder naming compact_context must never reach an agent without it.
func TestReadPressure_CostTriggerWithheldWhileCompactContextIsNotOffered(t *testing.T) {
	t.Setenv(readPressureThresholdEnv, "1000000")
	rp := newReadPressure("", 100000)
	rp.setOffered(false)

	for i := 0; i < 5; i++ {
		contextStep(rp, 100000)
		for _, out := range readStep(rp, "read", chunk(t, 10)) {
			if strings.Contains(out, resendCostMarker) || strings.Contains(out, reminderMarker) {
				t.Fatal("told the agent to call a tool it was never given")
			}
		}
	}
	if !rp.armed {
		t.Fatal("the cost trigger should still be armed, waiting for the tool to be offered")
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
	reqs := runReadPressureScript(t, 8)
	hits := countReminders(reqs[len(reqs)-1])
	if hits == 0 {
		t.Fatal("no read-pressure reminder reached the model after 8 large search rounds")
	}
	// The reminder is not a one-shot: a turn that keeps reading without compacting
	// is reminded again. More than one across 8 rounds is what "keeps arriving"
	// means in practice.
	if hits < 2 {
		t.Errorf("%d reminders in the final request after 8 rounds; the reminder must keep arriving until compaction", hits)
	}
	// The early requests must be clean: nudging before the agent has read
	// enough to have anything worth summarizing is noise, not guidance.
	if got := countReminders(reqs[1]); got != 0 {
		t.Errorf("%d reminders in the second request, before the threshold could be reached", got)
	}
}

// countReminders counts tool results in one request that carry the reminder.
func countReminders(msgs []provider.ModelMessage) int {
	return countMarker(msgs, reminderMarker)
}

// countMarker counts tool results in one request whose text carries marker.
func countMarker(msgs []provider.ModelMessage, marker string) int {
	n := 0
	for _, m := range msgs {
		if m.Role != "tool" || m.Content == nil {
			continue
		}
		var text string
		if json.Unmarshal(m.Content, &text) != nil {
			continue
		}
		if strings.Contains(text, marker) {
			n++
		}
	}
	return n
}

// TestRunLoop_CostReminderReachesTheModel is the cost trigger's wiring test. The
// volume trigger is pinned off (its threshold parked past any reading) so that a
// reminder in the request can only have come from the re-send cost — proving the
// usage count the loop reports each step is actually folded into the tracker and
// the cost reminder survives into the model-facing history.
func TestRunLoop_CostReminderReachesTheModel(t *testing.T) {
	reqs := runResendCostScript(t, 12)
	final := reqs[len(reqs)-1]
	if got := countMarker(final, resendCostMarker); got == 0 {
		t.Fatal("no cost reminder reached the model after 12 cost-heavy rounds")
	}
	if got := countMarker(final, reminderMarker); got != 0 {
		t.Errorf("the volume reminder fired too (%d); the test cannot attribute the reminder to cost", got)
	}
	// It is not a one-shot: the turn keeps sending the context, so it keeps being
	// told. More than one across 12 rounds is what "keeps arriving" means.
	if got := countMarker(final, resendCostMarker); got < 2 {
		t.Errorf("%d cost reminders in the final request after 12 rounds; the reminder must keep arriving until compaction", got)
	}
	// The earliest request must be clean — no reminder before the threshold.
	if got := countMarker(reqs[1], resendCostMarker); got != 0 {
		t.Errorf("%d cost reminders in the second request, before the threshold could be reached", got)
	}
}

// runResendCostScript drives a turn of `rounds` cost-heavy rounds — tiny reads,
// but a large reported input count every step — and returns every request made.
func runResendCostScript(t *testing.T, rounds int) [][]provider.ModelMessage {
	t.Helper()
	// Park the volume trigger past anything these tiny reads could reach, so the
	// cost trigger is the only one that can fire.
	t.Setenv(readPressureThresholdEnv, "1000000")

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

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	// A window of 100k with 40k of input reported per step puts the 1× threshold
	// (100k) at the third step; the half-threshold re-arm then fires again as the
	// cost keeps climbing, so 12 rounds leave room for the reminder to repeat.
	mock := &toolRoundsProvider{rounds: rounds, window: 100000, inputPerCall: 40000}
	reg.Register(mock)

	tools := tool.NewRegistry()
	tools.Register(noopGrepTool{})
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
	textData, _ := json.Marshal(session.TextPartData{Text: "do the long task"})
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

// runReadPressureScript drives a turn of `rounds` large search rounds and
// returns every request made.
func runReadPressureScript(t *testing.T, rounds int) [][]provider.ModelMessage {
	t.Helper()
	t.Setenv(readPressureThresholdEnv, "30000")

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
