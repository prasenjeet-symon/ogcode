package agent

import (
	"fmt"
	"log/slog"
	"math"

	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// stepReminderIntervalEnv names how many steps of one turn pass between
// step-count reminders, overriding stepReminderIntervalDefault.
const stepReminderIntervalEnv = "OGCODE_STEP_REMINDER_INTERVAL"

const (
	// stepReminderIntervalDefault is how many steps of a turn run before the
	// agent is asked to check whether it is still getting anywhere: 100. A turn
	// that has taken this many steps is no longer a short task with a few
	// detours; it is the length at which a loop starts repeating itself — the
	// same file re-read, the same failing command re-run — while the result the
	// user is waiting for never arrives. Override with
	// OGCODE_STEP_REMINDER_INTERVAL.
	stepReminderIntervalDefault = 100
	// stepReminderIntervalMin/Max clamp a configured interval so a mistyped
	// magnitude cannot nudge the agent every step or park the reminder past any
	// turn.
	stepReminderIntervalMin = 10
	stepReminderIntervalMax = 10000
)

// stepReminder is the step-count counterpart to readPressure. Read pressure asks
// the agent to compact what it has read; this asks a different question — is this
// turn still making progress at all? The two are independent: a turn can be
// cheap and circular (re-running one command) or expensive and productive.
//
// It counts the turn's steps, not its tokens, because length is the only signal
// that survives every kind of stuck loop: a repetitive edit, a failing test
// retried with the same arguments, a search that keeps returning the same page.
// At each interval it appends a reminder to the first successful tool result of
// the step, which is the last moment the model reads something before deciding
// what to do next.
//
// Unlike every other reminder the loop attaches, this one is deliberately
// advisory. Compacting is always safe; stopping is not — a turn cut short at 100
// steps can leave a repository half-edited. So the text asks the agent to judge:
// wrap up if the task is done or the loop is circular, carry on if it is still
// progressing, and say which one it chose. A directive here would turn a nudge
// into a way to abandon work.
type stepReminder struct {
	// sessionID is carried for the log line, so a firing reminder is
	// attributable to the turn it fired in.
	sessionID session.SessionID
	// interval is how many steps pass between reminders.
	interval int
	// budget is the turn's soft step budget, quoted in the reminder when it is
	// finite and not yet exhausted, so a turn nearing its ceiling knows how
	// little runway is left. math.MaxInt means uncapped.
	budget int

	// nextAt is the step number the next reminder becomes due at. It advances by
	// interval after each crossing.
	nextAt int
	// step is the step currently in flight, quoted in the reminder text.
	step int
	// pending is a reminder that has become due but found no successful tool
	// result to ride yet. It survives into the next step rather than being
	// dropped: the steps most likely to have no attach point are the ones whose
	// calls were all denied, which is not a reason to stop reminding.
	pending bool
	// nudged is set once a reminder has ridden a result of this step, so a block
	// of parallel calls carries it exactly once.
	nudged bool
}

// newStepReminder builds a tracker for one turn. budget is the turn's soft step
// budget (maxSteps), used only for the text; a step count is what arms it.
func newStepReminder(sessionID session.SessionID, budget int) *stepReminder {
	interval := envInt(stepReminderIntervalEnv, stepReminderIntervalDefault,
		stepReminderIntervalMin, stepReminderIntervalMax)
	return &stepReminder{
		sessionID: sessionID,
		interval:  interval,
		budget:    budget,
		nextAt:    interval,
	}
}

// beginStep records the step the loop is about to run, clearing the one-per-step
// flag and marking the reminder due on the interval's step.
func (sr *stepReminder) beginStep(step int) {
	if sr == nil {
		return
	}
	sr.step = step
	sr.nudged = false
	if step >= sr.nextAt {
		sr.pending = true
		sr.nextAt += sr.interval
	}
}

// observe appends the pending reminder to a successful tool result. Any tool
// qualifies — a write or an edit as much as a read — because a mutating loop that
// keeps producing the same edit is exactly the case this exists for.
//
// It mutates res before the result is written to the store, like every other
// reminder: the model-facing history is rebuilt from the stored output on each
// step, so text added afterwards is text the model never sees.
func (sr *stepReminder) observe(toolName string, res *tool.Result) {
	if sr == nil || res == nil || res.Denied || !sr.pending || sr.nudged {
		return
	}
	res.Output += stepCheckReminder(sr.step, sr.budget, sr.interval)
	if res.Metadata == nil {
		res.Metadata = map[string]any{}
	}
	// Marks the appended text as harness-injected rather than something the tool
	// produced, so the UI can tell them apart.
	res.Metadata["stepReminder"] = true
	slog.Info("step reminder attached to tool result", "session", sr.sessionID,
		"step", sr.step, "tool", toolName, "interval", sr.interval, "budget", sr.budget)
	sr.pending = false
	sr.nudged = true
}

// stepCheckReminder is the text. It reports the steps taken so far — the agent
// should be able to check the claim, not just be told the turn is long — names
// the repetitive case as the reason it fires, and hands the judgement back to the
// agent with the two outcomes named. The budget clause is omitted when the turn
// is uncapped or already past its soft budget (auto-resume), where quoting a
// remainder would be arithmetic the agent cannot act on.
func stepCheckReminder(steps, budget, interval int) string {
	runway := ""
	if budget > 0 && budget < math.MaxInt && steps < budget {
		runway = fmt.Sprintf(" — %d of the run's %d-step budget remain", budget-steps, budget)
	}
	return fmt.Sprintf("\n\n<system-reminder>\n"+
		"Step check: this turn has taken %d steps%s. Long turns drift — the same file "+
		"re-read, the same command run again with the same arguments, a fix attempted a "+
		"third time — and the answer the user is waiting for never arrives. A step that "+
		"repeats work you have already done is not progress.\n\n"+
		"Before the next call, decide which is true. If the task is done, or as done as "+
		"this turn can make it, wrap up now: stop calling tools, write the answer, and say "+
		"what changed, what you verified, and what is still open. If you are repeating "+
		"yourself, or blocked in a way another step will not fix, wrap up the same way and "+
		"say plainly what is unfinished and why. If you are still making progress, carry "+
		"on — but state in one line what remains. This check returns every %d steps until "+
		"the turn ends.\n"+
		"</system-reminder>", steps, runway, interval)
}
