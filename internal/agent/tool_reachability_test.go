package agent

import (
	"slices"
	"strings"
	"testing"
)

// codeFacingAgents are the agents that read a user's source tree. The Index
// agent (no read tools) and the Search agent (reads fetched web pages, not the
// repo) are deliberately excluded.
func codeFacingAgents() []Agent {
	return []Agent{BuildAgent, TaskAgent, PlanAgent, BreakdownAgent, NoteAgent, SubagentAgent}
}

// mandatoryPromptTools are tools the shared prompt sections tell an agent it
// MUST use.
//
// This is the invariant that a tool being registered in server.go does not
// establish. Registry.ForAgent sends the model only the tools an agent's Tools
// list names, so a tool can be fully built, registered and documented and still
// be unreachable — the model is never offered it, and the failure is silent:
// no error, just an agent that never calls it.
var mandatoryPromptTools = []string{"codebase_map", "file_map", "check_syntax"}

func TestAgents_PromptMandatedToolsAreReachable(t *testing.T) {
	for _, a := range codeFacingAgents() {
		for _, tool := range mandatoryPromptTools {
			if !strings.Contains(a.System, tool) {
				continue // this agent's prompt doesn't ask for it
			}
			if !slices.Contains(a.Tools, tool) {
				t.Errorf("%s: system prompt requires %q but Tools does not list it, "+
					"so ForAgent never offers it to the model", a.Name, tool)
			}
		}
	}
}

// The read/file_map pairing only works if both halves are present: file_map
// prints the ranges, read consumes them.
func TestAgents_FileMapAccompaniesRead(t *testing.T) {
	for _, a := range codeFacingAgents() {
		if !slices.Contains(a.Tools, "read") {
			t.Errorf("%s: expected a code-facing agent to have read", a.Name)
			continue
		}
		if !slices.Contains(a.Tools, "file_map") {
			t.Errorf("%s: has read but not file_map, so it can only pull whole files", a.Name)
		}
	}
}

// The Search agent reads fetched web pages rather than a source tree, so it is
// intentionally left without file_map. Pinned so the exclusion stays a decision
// rather than drifting into an oversight.
func TestSearchAgent_ExcludedFromFileMap(t *testing.T) {
	if slices.Contains(SearchAgent.Tools, "file_map") {
		t.Error("SearchAgent has file_map; it works on web pages, not the repo")
	}
}

// The prompt has to tell the agent how to spend the ranges file_map returns,
// including the one detail that silently corrupts a read when got wrong.
func TestProjectIndexPrompt_ExplainsFileMapRanges(t *testing.T) {
	prompt := projectIndexPrompt("build")

	for _, want := range []string{
		"file_map",
		"start_line",
		"end_line",
		"never convert it to \"offset\"", // the off-by-one this design exists to prevent
		"call \"file_map\" on it again",  // ranges in context go stale after an edit
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("project index prompt missing %q", want)
		}
	}
}

// An agent that can change a file can break it, and check_syntax is how it
// finds that out before it stacks more edits on the damage. Pinned as an
// invariant so a future write-capable agent cannot be added without it: the
// prompt tells the build agents to verify every edit, and an agent that was
// never offered the tool would silently skip that step.
func TestAgents_WriteCapableHaveCheckSyntax(t *testing.T) {
	for _, a := range codeFacingAgents() {
		if !slices.Contains(a.Tools, "write") && !slices.Contains(a.Tools, "edit") {
			continue
		}
		if !slices.Contains(a.Tools, "check_syntax") {
			t.Errorf("%s: can write or edit files but has no check_syntax, "+
				"so it cannot tell whether an edit left the file parseable", a.Name)
		}
	}
}
