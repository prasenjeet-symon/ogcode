package agent

import (
	"log/slog"
	"sync"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// intentGate is the enforcement point behind update_intent's timing rule. The
// prompt asks the agent to state a plan before the first meaningful
// exploration or work of the turn; an agent that never states one would
// otherwise never be told, because nothing below the prompt checks it.
//
// It watches the turn's tool results and appends a reminder to the first tool
// call that runs without a plan — exploration included, because a plan stated
// after orientation has already spent the matcher's answer on work that is
// done. It is deliberately a nudge, not a block: by the time the result
// exists the call has already run, and refusing it would not recover the
// match.
//
// The reminder rides that result because the model-facing history is rebuilt
// from the stored output on every later step — a note added outside that
// output is a note the model never sees.
//
// The cadence is ONCE PER PLANDLESS STINT, not per turn: a gate is constructed
// fresh on every turn, so whether this session is owed a reminder lives in a
// package-level registry keyed by session ID. A session reminded once is not
// reminded again while it keeps going without a plan, however many such turns
// follow — a repeated nudge adds context without adding a decision.
// markPlanSet clears the session's entry, so a session that stated a plan and
// later has a planless stint re-arms and is nudged again; sweepIntentReminderSeen
// — run at turn end (loop.go) — ages out the entries whose session has had no
// planless turn since, typically deleted conversations, so the registry stays
// bounded for a long-lived process.
type intentGate struct {
	sessionID session.SessionID
	offered   bool
	set       bool
}

// intentReminderSeen records the sessions this process currently owes a
// reminder, so the once-per-stint cadence survives the gate being constructed
// fresh on every turn. The value is when the session last showed up planless
// — observe stamps every planless turn — so the sweep can drop the entries
// whose session has gone quiet without re-arming a live one. Guarded by
// intentReminderSeenMu.
var (
	intentReminderSeenMu sync.Mutex
	intentReminderSeen   = make(map[session.SessionID]time.Time)
)

// intentReminderSeenTTL is how long a fired-set entry may sit unrefreshed
// before the sweep drops it: far past any ordinary stint, so a live session's
// cadence — reminded once until it states a plan — is untouched, and only the
// sessions that stopped showing up planless (deleted conversations, sessions
// that finished) are the ones a sweep re-arms.
const intentReminderSeenTTL = 24 * time.Hour

// sweepIntentReminderSeen drops the fired-set entries older than the TTL. It
// runs at turn end, on the loop's writeTurnMemory/autoIndexTurn sites, where
// the other per-turn housekeeping hooks already live: one lock, one pass over
// a set that is normally a handful of entries, and an empty set costs nothing
// but the scan's early return.
func sweepIntentReminderSeen() {
	intentReminderSeenMu.Lock()
	defer intentReminderSeenMu.Unlock()
	if len(intentReminderSeen) == 0 {
		return
	}
	cutoff := time.Now().Add(-intentReminderSeenTTL)
	for id, at := range intentReminderSeen {
		if at.Before(cutoff) {
			delete(intentReminderSeen, id)
		}
	}
}

func newIntentGate(sessionID session.SessionID, offered bool) *intentGate {
	return &intentGate{sessionID: sessionID, offered: offered}
}

// markPlanSet records that the turn has a plan, so no reminder is owed, and
// clears the session's entry in the fired registry so the map stays bounded.
// It is called for a successful update_intent in the same block before any of
// that block's other calls are judged.
func (g *intentGate) markPlanSet() {
	if g == nil {
		return
	}
	g.set = true
	intentReminderSeenMu.Lock()
	delete(intentReminderSeen, g.sessionID)
	intentReminderSeenMu.Unlock()
}

// observe appends the reminder to the first tool call's result when the turn
// has no plan yet and this session has never been reminded in this process.
// It must run before the result is written to the store, for the same reason
// readPressure.observe must: later steps rebuild the request from the stored
// output.
func (g *intentGate) observe(toolName string, res *tool.Result) {
	if g == nil || res == nil || res.Denied || !g.offered || g.set {
		return
	}
	// update_intent is the call the reminder asks for; a rejected one already
	// carries its own reason, so a second message would only repeat it.
	if toolName == "update_intent" {
		return
	}
	intentReminderSeenMu.Lock()
	_, seen := intentReminderSeen[g.sessionID]
	intentReminderSeen[g.sessionID] = time.Now()
	intentReminderSeenMu.Unlock()
	if seen {
		return
	}
	res.Output += intentGateReminder
	if res.Metadata == nil {
		res.Metadata = map[string]any{}
	}
	// Marks the appended text as harness-injected rather than something the
	// tool produced, so the UI can tell them apart.
	res.Metadata["intentReminder"] = true
	slog.Info("intent reminder attached to tool result", "session", g.sessionID, "tool", toolName)
}

// intentGateReminder is a directive, not a report: the model reads it in the
// block it is about to act on, so it says what to do next.
const intentGateReminder = `

**No plan was stated this turn.** A tool call has already run without one — the user cannot see where this turn is heading, and the matcher that names the skills and MCP tools fitted to your steps never saw a plan. Call update_intent in the next block with the full list — it may ride alongside other calls — then carry on: mark what you have already done as done, and list what remains.`
