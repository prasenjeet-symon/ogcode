package agent

import (
	"log/slog"

	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// intentGate is the enforcement point behind update_intent's timing rule. The
// prompt asks the agent to open the turn with a plan, before any exploration;
// an agent that starts with a map or a read would otherwise never be told
// again, because nothing below the prompt checks the order the calls arrive in.
//
// It watches the turn's tool results and appends a reminder to the FIRST tool
// call of a turn that has set no plan — exploration included, because a plan
// stated after orientation has already spent the matcher's answer on work that
// is done. It is deliberately a nudge, not a block: by the time the result
// exists the call has already run, and refusing it would not recover the match.
//
// The reminder rides that result because the model-facing history is rebuilt
// from the stored output on every later step — a note added outside that output
// is a note the model never sees. One shot per turn: a second reminder adds
// context without adding a decision, and a plan stated later in the turn
// silences it for good.
type intentGate struct {
	sessionID session.SessionID
	offered   bool
	set       bool
	nudged    bool
}

func newIntentGate(sessionID session.SessionID, offered bool) *intentGate {
	return &intentGate{sessionID: sessionID, offered: offered}
}

// markPlanSet records that the turn has a plan, so no reminder is owed. It is
// called for a successful update_intent in the same block before any of that
// block's other calls are judged.
func (g *intentGate) markPlanSet() {
	if g == nil {
		return
	}
	g.set = true
}

// observe appends the reminder to the first tool call's result when the turn
// has no plan yet. It must run before the result is written to the store, for
// the same reason readPressure.observe must: later steps rebuild the request
// from the stored output.
func (g *intentGate) observe(toolName string, res *tool.Result) {
	if g == nil || res == nil || res.Denied || !g.offered || g.set || g.nudged {
		return
	}
	// update_intent is the call the reminder asks for; a rejected one already
	// carries its own reason, so a second message would only repeat it.
	if toolName == "update_intent" {
		return
	}
	res.Output += intentGateReminder
	if res.Metadata == nil {
		res.Metadata = map[string]any{}
	}
	// Marks the appended text as harness-injected rather than something the
	// tool produced, so the UI can tell them apart.
	res.Metadata["intentReminder"] = true
	g.nudged = true
	slog.Info("intent reminder attached to tool result", "session", g.sessionID, "tool", toolName)
}

// intentGateReminder is a directive, not a report: the model reads it in the
// block it is about to act on, so it says what to do next.
const intentGateReminder = `

**No plan was stated this turn.** A tool call has already run without one — the user cannot see where this turn is heading, and the matcher that names the skills and MCP tools fitted to your steps never saw a plan. Call update_intent on its own in the next block with the full list, then carry on: mark what you have already done as done, and list what remains.`
