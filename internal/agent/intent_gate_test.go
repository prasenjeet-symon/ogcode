package agent

import (
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// A turn that opens with a tool call instead of a plan gets exactly one
// reminder, appended to that call's own result — the freshest thing in context
// on the next step, and part of the stored output the model reads.
func TestIntentGateNudgesTheFirstCallWithoutAPlan(t *testing.T) {
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

// A plan stated anywhere in the turn settles it: the rule is "the first call",
// not "one reminder per call".
func TestIntentGateStaysQuietOnceAPlanIsSet(t *testing.T) {
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
// the reminder asks for.
func TestIntentGateFiresOnEveryCallExceptUpdateIntent(t *testing.T) {
	for _, name := range []string{"read", "grep", "glob", "codebase_map", "file_map", "check_syntax", "bash", "edit", "write", "mcp_server/tool"} {
		g := newIntentGate("sess", true)
		res := &tool.Result{Output: "ok"}
		g.observe(name, res)
		if !strings.Contains(res.Output, "No plan was stated") {
			t.Errorf("%s did not draw the reminder", name)
		}
	}

	g := newIntentGate("sess", true)
	res := &tool.Result{Output: "plan"}
	g.observe("update_intent", res)
	if strings.Contains(res.Output, "No plan was stated") {
		t.Error("update_intent itself drew the reminder")
	}
}

// The gate exists only where update_intent is on the wire. A headless or
// read-only agent has no way to answer the reminder, so it must never see one.
func TestIntentGateIsSilentWhenTheToolIsNotOffered(t *testing.T) {
	g := newIntentGate("sess", false)
	res := &tool.Result{Output: "mapped"}
	g.observe("codebase_map", res)
	if strings.Contains(res.Output, "No plan was stated") {
		t.Error("an agent without update_intent was reminded to call it")
	}
}

// A denied call never ran, so it does not spend the turn's one reminder.
func TestIntentGateIgnoresDeniedCalls(t *testing.T) {
	g := newIntentGate("sess", true)
	res := &tool.Result{Output: "denied", Denied: true}
	g.observe("edit", res)
	if strings.Contains(res.Output, "No plan was stated") {
		t.Error("a denied edit drew the reminder")
	}
}
