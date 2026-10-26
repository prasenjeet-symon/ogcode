package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// resetIntentReminderSeen empties the package-level fired-set so a test does
// not inherit the sessions earlier tests reminded: the cadence is once per
// session for the life of the process, and the tests run in one process.
func resetIntentReminderSeen(t *testing.T) {
	t.Helper()
	intentReminderSeenMu.Lock()
	intentReminderSeen = make(map[session.SessionID]time.Time)
	intentReminderSeenMu.Unlock()
}

// intentReminderSeenGuarded copies the fired-set out under the lock, so the
// tests never touch the map directly.
func intentReminderSeenGuarded() map[session.SessionID]time.Time {
	intentReminderSeenMu.Lock()
	defer intentReminderSeenMu.Unlock()
	out := make(map[session.SessionID]time.Time, len(intentReminderSeen))
	for k, at := range intentReminderSeen {
		out[k] = at
	}
	return out
}

// TestIntentGateRegistrySweepExpiresStaleEntries pins the turn-end sweep: an
// entry unrefreshed past the TTL — a session that stopped showing up
// planless, a deleted conversation in practice — is dropped, while the entry
// the current stint just refreshed survives it, so housekeeping never
// re-nudges a live stint.
func TestIntentGateRegistrySweepExpiresStaleEntries(t *testing.T) {
	resetIntentReminderSeen(t)

	g := newIntentGate("sess", true)
	g.observe("read", &tool.Result{Output: "ok"})
	intentReminderSeenMu.Lock()
	intentReminderSeen["stale"] = time.Now().Add(-intentReminderSeenTTL - time.Hour)
	intentReminderSeenMu.Unlock()

	sweepIntentReminderSeen()

	seen := intentReminderSeenGuarded()
	if _, ok := seen["stale"]; ok {
		t.Error("the sweep kept an entry older than the TTL")
	}
	if _, ok := seen["sess"]; !ok {
		t.Error("the sweep dropped an entry the current stint just refreshed")
	}
}

// A turn that opens with a tool call instead of a plan gets exactly one
// reminder, appended to that call's own result — the freshest thing in context
// on the next step, and part of the stored output the model reads.
func TestIntentGateNudgesTheFirstCallWithoutAPlan(t *testing.T) {
	resetIntentReminderSeen(t)
	g := newIntentGate("sess", true)
	res := &tool.Result{Output: "mapped"}
	g.observe("codebase_map", res)

	if !strings.Contains(res.Output, "No plan was stated this turn") {
		t.Errorf("the first call did not carry the reminder: %q", res.Output)
	}
	if !strings.HasPrefix(res.Output, "mapped") {
		t.Error("the reminder replaced the tool's output instead of following it")
	}
	if ok, _ := res.Metadata["intentReminder"].(bool); !ok {
		t.Error("reminder not marked as harness-injected in metadata")
	}

	// One shot: a second call in the same turn is not scolded again.
	second := &tool.Result{Output: "read"}
	g.observe("read", second)
	if strings.Contains(second.Output, "No plan was stated") {
		t.Error("the reminder fired twice in one turn")
	}
}

// The reminder must not carry the old sequencing mandate: the plan may ride
// the first tool block alongside other calls now, so the text it asks the
// model to obey must not contradict the softened prompt.
func TestIntentGateReminderDropsTheStandaloneMandate(t *testing.T) {
	resetIntentReminderSeen(t)
	for _, gone := range []string{"on its own", "a block of its own", "carries no other"} {
		if strings.Contains(intentGateReminder, gone) {
			t.Errorf("intentGateReminder still carries the standalone mandate %q: %q", gone, intentGateReminder)
		}
	}
	if !strings.Contains(intentGateReminder, "may ride alongside other calls") {
		t.Errorf("intentGateReminder does not carry the softened contract: %q", intentGateReminder)
	}
}

// A plan stated anywhere in the turn settles it: the rule is "the first call",
// not "one reminder per call".
func TestIntentGateStaysQuietOnceAPlanIsSet(t *testing.T) {
	resetIntentReminderSeen(t)
	g := newIntentGate("sess", true)
	g.markPlanSet()

	res := &tool.Result{Output: "mapped"}
	g.observe("codebase_map", res)
	if strings.Contains(res.Output, "No plan was stated") {
		t.Error("a turn that stated a plan was still reminded")
	}
}

// Exploration is the case the rule exists for — a plan stated after it has
// already spent the match — so a map, a grep or a read draws the reminder
// exactly like a mutation does. update_intent itself never does: it is the call
// the reminder asks for. The cadence is once per SESSION, not once per turn:
// a fresh gate on a later turn of the same session stays quiet.
func TestIntentGateFiresOncePerSessionAcrossTurns(t *testing.T) {
	for _, name := range []string{"read", "grep", "glob", "codebase_map", "file_map", "check_syntax", "bash", "edit", "write", "mcp_server/tool"} {
		resetIntentReminderSeen(t)
		g := newIntentGate("sess", true)
		res := &tool.Result{Output: "ok"}
		g.observe(name, res)
		if !strings.Contains(res.Output, "No plan was stated") {
			t.Errorf("%s did not draw the reminder", name)
		}
	}

	resetIntentReminderSeen(t)
	g := newIntentGate("sess", true)
	res := &tool.Result{Output: "plan"}
	g.observe("update_intent", res)
	if strings.Contains(res.Output, "No plan was stated") {
		t.Error("update_intent itself drew the reminder")
	}
}

// Once fired, the session is never reminded again in this process — the
// registry is keyed by session ID and survives the per-turn gate
// construction, so a later planless turn of the same session stays quiet.
// Stating a plan clears the entry (markPlanSet keeps the registry bounded to
// sessions that were nudged and never answered), after which a later
// planless turn can draw the reminder once more.
func TestIntentGateRemindsASessionOncePerPlanlessStint(t *testing.T) {
	resetIntentReminderSeen(t)
	g := newIntentGate("sess", true)
	first := &tool.Result{Output: "mapped"}
	g.observe("codebase_map", first)
	if !strings.Contains(first.Output, "No plan was stated") {
		t.Fatal("the first planless call did not draw the reminder")
	}

	// A later turn, no plan: new gate, same session — quiet.
	second := &tool.Result{Output: "read"}
	newIntentGate("sess", true).observe("read", second)
	if strings.Contains(second.Output, "No plan was stated") {
		t.Error("the reminder fired again on a later turn of the same session")
	}
	if got := intentReminderSeenGuarded(); len(got) != 1 {
		t.Errorf("fired-set = %v, want exactly the one fired session", got)
	}

	// Stating the plan clears the entry so the map stays bounded — the mark
	// the session earned by never planning is not permanent once it answers.
	newIntentGate("sess", true).markPlanSet()
	if got := intentReminderSeenGuarded(); len(got) != 0 {
		t.Errorf("markPlanSet left the fired-set populated: %v", got)
	}

	// Only the session's own plan clears it: a different session's plan set
	// must leave this session's fired-mark standing.
	next := newIntentGate("other", true)
	next.observe("codebase_map", &tool.Result{Output: "mapped"})
	newIntentGate("sess", true).markPlanSet()
	// sess is clear now, other is fired. Confirm the marks are per-session.
	if got := intentReminderSeenGuarded(); len(got) != 1 {
		t.Errorf("fired-set = %v, want only the fired session", got)
	}
}

// A reminder is the session's own: one session being fired (or planning)
// says nothing about another's eligibility. Session b's planless stint after
// it stated a plan re-arms and draws a reminder of its own, while a fired
// session stays quiet whatever the other sessions did meanwhile.
func TestIntentGateFiredSetIsPerSession(t *testing.T) {
	resetIntentReminderSeen(t)
	fired := newIntentGate("a", true)
	first := &tool.Result{Output: "ok"}
	fired.observe("grep", first)
	if !strings.Contains(first.Output, "No plan was stated") {
		t.Fatal("session a's planless call did not draw the reminder")
	}

	// Session b stated a plan in an earlier turn; the planless turn after it
	// is a fresh stint, and the reminder it draws is b's alone.
	newIntentGate("b", true).markPlanSet()
	reminded := &tool.Result{Output: "ok"}
	newIntentGate("b", true).observe("read", reminded)
	if !strings.Contains(reminded.Output, "No plan was stated") {
		t.Error("session b's planless stint did not draw its own reminder")
	}

	// Once fired, session a stays quiet — a later planless call of the same
	// session draws nothing, whatever other sessions did meanwhile.
	again := &tool.Result{Output: "ok"}
	newIntentGate("a", true).observe("grep", again)
	if strings.Contains(again.Output, "No plan was stated") {
		t.Error("session a was reminded a second time in-process")
	}
	got := intentReminderSeenGuarded()
	if len(got) != 2 {
		t.Errorf("fired-set = %v, want both sessions marked", got)
	}
}

// The gate exists only where update_intent is on the wire. A headless or
// read-only agent has no way to answer the reminder, so it must never see one.
func TestIntentGateIsSilentWhenTheToolIsNotOffered(t *testing.T) {
	resetIntentReminderSeen(t)
	g := newIntentGate("sess", false)
	res := &tool.Result{Output: "mapped"}
	g.observe("codebase_map", res)
	if strings.Contains(res.Output, "No plan was stated") {
		t.Error("an agent without update_intent was reminded to call it")
	}
	// And a tool-less session must NOT have spent the session's once-per-
	// session reminder — an offered tool may still get it later.
	if got := intentReminderSeenGuarded(); len(got) != 0 {
		t.Errorf("a gate without the tool consumed the session's reminder: %v", got)
	}
}

// A denied call never ran, so it does not spend the turn's one reminder — nor
// the session's.
func TestIntentGateIgnoresDeniedCalls(t *testing.T) {
	resetIntentReminderSeen(t)
	g := newIntentGate("sess", true)
	res := &tool.Result{Output: "denied", Denied: true}
	g.observe("edit", res)
	if strings.Contains(res.Output, "No plan was stated") {
		t.Error("a denied edit drew the reminder")
	}
	if got := intentReminderSeenGuarded(); len(got) != 0 {
		t.Errorf("a denied call consumed the session's reminder: %v", got)
	}
}
