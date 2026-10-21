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
	for _, a := range []Agent{SubagentAgent, TaskAgent, PlanAgent, BreakdownAgent, NoteAgent, IndexAgent, SearchAgent, MemoryRecallAgent} {
		if a.canUpdateIntent() {
			t.Errorf("agent %q should not state a plan", a.ID)
		}
	}
}

// TestUpdateIntentPromptCarriesTheTimingContract pins the rules the feature
// rests on: the plan is the FIRST call of the turn — before codebase_map, before
// any read — because the matcher's answer has to arrive before the exploration
// it is meant to steer; the usual exploration path follows it (the map, then
// every file map in one block, then every ranged read in the next); the call
// stands in a block of its own so the plan lands before the work it describes;
// the plan the user is reading must be kept honest (a step is done or dropped,
// never left active after the agent has moved on); and the next call copies the
// list the tool echoed back rather than re-deriving one from memory.
func TestUpdateIntentPromptCarriesTheTimingContract(t *testing.T) {
	p := updateIntentPrompt()
	for _, want := range []string{
		"## Saying where you are heading",
		"update_intent",
		"**When to call it: first — before \"codebase_map\", before any read, before anything else in the turn.**",
		"spends that match on work that is done",
		"runs the matcher",
		"never gets the match at all",
		"Never call \"skill\" with no name",
		"Call it again only when the plan changes",
		"**Then explore as usual.**",
		"codebase_map",
		"**Call it on its own.**",
		"replaces the plan",
		"copy that list and change only what moved",
		"dropped",
		"Do not also narrate",
		"the status is what the user reads",
		"before the message that ends the turn",
		"with every step's real status",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("updateIntentPrompt is missing %q", want)
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
