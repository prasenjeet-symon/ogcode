package agent

import (
	"context"
	"encoding/json"
	"math"
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

// stepCheckMarker is the phrase the step-count reminder is asserted by. It is
// the one sentence a reader would look for in a transcript to tell whether the
// nudge fired.
const stepCheckMarker = "Step check:"

// stepRun drives one step through the tracker and returns the tool outputs as
// the store would have recorded them.
func stepRun(sr *stepReminder, step int, outputs ...string) []string {
	sr.beginStep(step)
	got := make([]string, 0, len(outputs))
	for _, out := range outputs {
		res := tool.Result{Output: out}
		sr.observe("grep", &res)
		got = append(got, res.Output)
	}
	return got
}

func TestStepReminderInterval(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int
	}{
		{"unset uses the default", "", stepReminderIntervalDefault},
		{"a usable value is taken verbatim", "250", 250},
		{"unparseable falls back to the default", "lots", stepReminderIntervalDefault},
		{"below the floor is clamped up", "1", stepReminderIntervalMin},
		{"above the max is clamped down", "999999", stepReminderIntervalMax},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(stepReminderIntervalEnv, tt.raw)
			if got := newStepReminder("", 0).interval; got != tt.want {
				t.Fatalf("interval with %q = %d, want %d", tt.raw, got, tt.want)
			}
		})
	}
}

// A short turn must never see the reminder: 100 steps is the length at which a
// turn is no longer a task with a few detours, and nudging before that is noise.
func TestStepReminder_SilentUntilTheInterval(t *testing.T) {
	t.Setenv(stepReminderIntervalEnv, "")
	sr := newStepReminder("", 1000)
	for step := 1; step < stepReminderIntervalDefault; step++ {
		for _, out := range stepRun(sr, step, "match") {
			if strings.Contains(out, stepCheckMarker) {
				t.Fatalf("reminded on step %d, before the interval", step)
			}
		}
	}
	if out := stepRun(sr, stepReminderIntervalDefault, "match"); !strings.Contains(out[0], stepCheckMarker) {
		t.Fatal("no reminder on the interval's step")
	}
}

// A block of parallel calls carries the reminder exactly once, and the step
// after it is clean again.
func TestStepReminder_RidesOneResultPerStep(t *testing.T) {
	t.Setenv(stepReminderIntervalEnv, "")
	sr := newStepReminder("", 1000)
	outs := stepRun(sr, 100, "a", "b", "c")
	hits := 0
	for _, out := range outs {
		if strings.Contains(out, stepCheckMarker) {
			hits++
		}
	}
	if hits != 1 {
		t.Fatalf("reminder attached to %d of 3 parallel results, want exactly 1", hits)
	}
	for _, out := range stepRun(sr, 101, "a") {
		if strings.Contains(out, stepCheckMarker) {
			t.Error("reminder repeated on the step after it fired")
		}
	}
}

// It keeps arriving every interval after the first, so a turn that ignores it
// once — the turn it exists for — keeps being told.
func TestStepReminder_RepeatsEveryInterval(t *testing.T) {
	t.Setenv(stepReminderIntervalEnv, "")
	sr := newStepReminder("", 1000)

	fired := map[int]bool{}
	for step := 1; step <= 300; step++ {
		for _, out := range stepRun(sr, step, "match") {
			if strings.Contains(out, stepCheckMarker) {
				fired[step] = true
			}
		}
	}
	for _, want := range []int{100, 200, 300} {
		if !fired[want] {
			t.Errorf("no reminder on step %d", want)
		}
	}
	for step := range fired {
		if step%stepReminderIntervalDefault != 0 {
			t.Errorf("reminder fired on step %d, off the interval", step)
		}
	}
}

func TestStepReminder_MarksTheReminderAsHarnessInjected(t *testing.T) {
	t.Setenv(stepReminderIntervalEnv, "")
	sr := newStepReminder("", 1000)
	sr.beginStep(100)
	res := tool.Result{Output: "match"}
	sr.observe("grep", &res)
	if ok, _ := res.Metadata["stepReminder"].(bool); !ok {
		t.Fatal("the appended reminder is indistinguishable from the tool's own output")
	}
}

// A step whose calls were all denied offers no attach point; the reminder waits
// for the next successful result rather than being dropped.
func TestStepReminder_WaitsForARealResult(t *testing.T) {
	t.Setenv(stepReminderIntervalEnv, "")
	sr := newStepReminder("", 1000)
	sr.beginStep(100)
	denied := tool.Result{Output: "refused", Denied: true}
	sr.observe("write", &denied)
	if strings.Contains(denied.Output, stepCheckMarker) {
		t.Fatal("reminder attached to a denied call")
	}
	if !sr.pending {
		t.Fatal("a denied step consumed the pending reminder")
	}
	if out := stepRun(sr, 101, "match"); !strings.Contains(out[0], stepCheckMarker) {
		t.Fatal("reminder was not delivered on the first successful result after the denied step")
	}
}

func TestStepReminder_QuotesTheRemainingBudget(t *testing.T) {
	t.Setenv(stepReminderIntervalEnv, "")
	sr := newStepReminder("", 1000)
	if out := stepRun(sr, 100, "match"); !strings.Contains(out[0], "900 of the run's 1000-step budget remain") {
		t.Fatalf("the reminder does not quote the remaining budget:\n%s", out[0])
	}
}

// An uncapped run has no runway to quote, and a turn past its soft budget
// (auto-resume) would be quoted a negative one — both must stay quiet about it.
func TestStepReminder_OmitsTheBudgetWhenThereIsNone(t *testing.T) {
	t.Setenv(stepReminderIntervalEnv, "")
	for _, budget := range []int{math.MaxInt, 0, 50} {
		sr := newStepReminder("", budget)
		out := stepRun(sr, 100, "match")
		if strings.Contains(out[0], "budget remain") {
			t.Errorf("budget %d quoted a runway it does not have:\n%s", budget, out[0])
		}
	}
}

// The text has to name the case it exists for — a loop that repeats itself — and
// hand the judgement back: wrapping up is optional, not mandatory.
func TestStepReminder_TextAsksForAJudgement(t *testing.T) {
	text := stepCheckReminder(100, 1000, 100)
	for _, want := range []string{
		"100 steps",
		"repeats work you have already done",
		"wrap up now",
		"still making progress, carry on",
		"every 100 steps",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("reminder does not mention %q:\n%s", want, text)
		}
	}
}

// The prompt tells the agent that a reminder appended to a tool result is a
// directive. The step check is the exception, and an agent that reads it as a
// directive would abandon half-finished work at step 100 — so the carve-out is
// pinned in the same section that states the rule.
func TestCompactContextPrompt_CarvesOutTheStepCheck(t *testing.T) {
	p := compactContextPrompt()
	for _, want := range []string{"Step check", "deliberately not a directive", "has stopped making progress"} {
		if !strings.Contains(p, want) {
			t.Errorf("compactContextPrompt is missing %q", want)
		}
	}
}

// TestRunLoop_StepReminderReachesTheModel is the wiring test: the reminder is
// only worth anything if it survives into the request the provider actually
// receives. The loop rebuilds the model-facing history from the store on every
// step, so a reminder that was not written into the stored tool output would be
// silently dropped — invisible to every unit test above.
func TestRunLoop_StepReminderReachesTheModel(t *testing.T) {
	reqs := runStepReminderScript(t, 14)
	if got := countMarker(reqs[len(reqs)-1], stepCheckMarker); got == 0 {
		t.Fatal("no step reminder reached the model after 14 tool rounds")
	}
	// The early requests must be clean: nudging before the turn has run long
	// enough is noise, not guidance.
	if got := countMarker(reqs[1], stepCheckMarker); got != 0 {
		t.Errorf("%d step reminders in the second request, before the interval", got)
	}
}

// runStepReminderScript drives a turn of `rounds` tool rounds with the reminder
// interval at its floor and returns every request made.
func runStepReminderScript(t *testing.T, rounds int) [][]provider.ModelMessage {
	t.Helper()
	t.Setenv(stepReminderIntervalEnv, "10")
	// Park the read-pressure triggers far past anything this script does, so a
	// reminder in the request can only have come from the step counter.
	t.Setenv(readPressureThresholdEnv, "1000000")
	t.Setenv(resendCostMultipleEnv, "1000")

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
