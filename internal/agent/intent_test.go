package agent

import (
	"strings"
	"testing"
)

// TestCanUpdateIntentIsOnlyTheBuildAgent pins the gate. The plan renders as a
// pill in the interactive session header, and the build session is the only one
// that runs the intent matcher the plan feeds. Every other agent — the sub-agent
// included — runs headless with no plan surface to reach, so a plan it wrote
// would go nowhere.
func TestCanUpdateIntentIsOnlyTheBuildAgent(t *testing.T) {
	for _, a := range []Agent{BuildAgent} {
		if !a.canUpdateIntent() {
			t.Errorf("agent %q should state a plan for the turn", a.ID)
		}
	}
	for _, a := range []Agent{SubagentAgent, NoteAgent, IndexAgent, SearchAgent, MemoryRecallAgent} {
		if a.canUpdateIntent() {
			t.Errorf("agent %q should not state a plan", a.ID)
		}
	}
}

// TestUpdateIntentPromptCarriesTheTimingContract pins the rules the feature
// rests on: the plan comes before the first meaningful exploration or work of
// the turn, and it may ride the first tool block alongside the calls it steers
// rather than stand alone; a status move is a plan change, so the repeat calls
// land while the work runs — active before a step's first block, done after
// its last — not banked for the end of the turn; the plan the user is reading
// must be kept honest (a step is done or dropped, never left active after the
// agent has moved on); nothing closes the plan and the closing status update is
// optional; and the next call copies the list the tool echoed back rather than
// re-deriving one from memory.
func TestUpdateIntentPromptCarriesTheTimingContract(t *testing.T) {
	p := updateIntentPrompt()
	for _, want := range []string{
		"## Saying where you are heading",
		"update_intent",
		"**When to call it: before the first meaningful exploration",
		"may ride the first tool block alongside its other calls",
		"spends that match on work that is done",
		"runs the matcher",
		"never gets the match at all",
		"Never call \"skill\" with no name",
		"Call it again only when the plan changes",
		"a status move is a plan change",
		"not banked for the end of the turn",
		"**Then explore as usual.**",
		"codebase_map",
		"replaces the plan",
		"copy that list and change only what moved",
		"dropped",
		"Do not also narrate",
		"the status is what the user reads",
		"**The plan does not survive the turn**",
		"welcome but not mandatory",
		"every step's real status",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("updateIntentPrompt is missing %q", want)
		}
	}
	// The standalone-block mandate and the mandatory closing call are gone:
	// the plan may ride the first tool block, and the closing status update is
	// the agent's choice.
	for _, gone := range []string{
		"Call it on its own",
		"carries no other call",
		"call update_intent one final time",
	} {
		if strings.Contains(p, gone) {
			t.Errorf("updateIntentPrompt still carries the old mandate %q", gone)
		}
	}
	// The deferred rule is gone: no read-only orientation ahead of the plan, and
	// no mutation-boundary or fifth-call fallback standing in for it.
	for _, gone := range []string{
		"before the first mutation",
		"by your fifth tool call",
		"may come before any plan",
	} {
		if strings.Contains(p, gone) {
			t.Errorf("updateIntentPrompt still carries the deferred rule %q", gone)
		}
	}
}

// TestUpdateIntentPromptIsPerTurnNotStatic pins that the section is appended per
// turn (gated on the tool being on the wire), like ask_user and compact_context,
// and never lands in the cached static system block — entry [0] must stay
// byte-identical for the whole session, for the prompt-cache breakpoint.
func TestUpdateIntentPromptIsPerTurnNotStatic(t *testing.T) {
	if strings.Contains(BuildAgent.System, "Saying where you are heading") {
		t.Error("the update_intent section leaked into the static system prompt")
	}
}
