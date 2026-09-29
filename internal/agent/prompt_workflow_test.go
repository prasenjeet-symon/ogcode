package agent

import (
	"strings"
	"testing"
)

// The agents that change files end the workflow diagram on verification: act
// on the syntax report write and edit already give, check_syntax only for
// files changed another way, then the build and the tests. The diagram is the
// part a model follows most literally, so a workflow that stopped at "make
// changes" taught it to stop there too.
func TestWorkflowEndsOnVerificationForAgentsThatWrite(t *testing.T) {
	build := projectIndexPrompt("build", true, true)
	for _, want := range []string{
		"→ Then make changes",
		"SYNTAX ERROR",
		"→ check_syntax(path)",
		"→ build, then run the tests",
	} {
		if !strings.Contains(build, want) {
			t.Errorf("build workflow missing %q", want)
		}
	}
	// check_syntax is not a step after every edit: write and edit already
	// report the syntax of what they change.
	if strings.Contains(build, "check_syntax on every") {
		t.Error("build workflow asks for check_syntax after every edit")
	}

	// The read-only roles end on their own product and are never pointed at a
	// tool they do not hold.
	for _, role := range []string{"plan", "note", "breakdown", "subagent"} {
		p := projectIndexPrompt(role, true, true)
		for _, unwanted := range []string{"check_syntax", "run the tests", "Then make changes"} {
			if strings.Contains(p, unwanted) {
				t.Errorf("%s workflow names %q, which it cannot do", role, unwanted)
			}
		}
	}
}

// The diagram is a pattern, not a script: its example calls must not name one
// project's folders, which a model working in another project copies as-is.
func TestWorkflowNamesNoProjectSpecificPaths(t *testing.T) {
	for _, role := range []string{"build", "plan", "note", "breakdown", "subagent"} {
		p := projectIndexPrompt(role, true, true)
		workflow := p[strings.Index(p, "### Workflow"):strings.Index(p, "## Mandatory: Map a File")]
		if strings.Contains(workflow, `subdir="internal`) {
			t.Errorf("%s workflow names ogcode's own folders:\n%s", role, workflow)
		}
		if !strings.Contains(workflow, `codebase_map(subdir="<folder>")`) {
			t.Errorf("%s workflow lost its placeholder descent:\n%s", role, workflow)
		}
	}
}

// Re-mapping after an edit is needed only before reading a range of that file
// again — an edit shifts the lines and does not report the new numbers — not
// after every edit, and only an agent that edits needs telling at all.
func TestRemapAfterEditIsConditionalAndForWritersOnly(t *testing.T) {
	build := projectIndexPrompt("build", true, true)
	if !strings.Contains(build, `Before you read a range from a file you have edited, call "file_map" on it again`) {
		t.Error("build prompt lost the re-map rule")
	}
	if strings.Contains(build, `After you edit a file, call "file_map" on it again`) {
		t.Error("build prompt still asks for a re-map after every edit")
	}
	for _, role := range []string{"plan", "note", "breakdown", "subagent"} {
		p := projectIndexPrompt(role, true, true)
		if strings.Contains(p, "you have edited") {
			t.Errorf("%s prompt tells a read-only agent about re-mapping its edits", role)
		}
		// What every agent still needs: the map is never stale.
		if !strings.Contains(p, "never stale") {
			t.Errorf("%s prompt lost the note that file_map is never stale", role)
		}
	}
}

// The atomic-edit rule is stated once in Hard rules, not twice in a row.
func TestHardRulesStateAtomicEditsOnce(t *testing.T) {
	for _, a := range []Agent{BuildAgent, TaskAgent} {
		hard := a.System[strings.Index(a.System, "## Hard rules"):]
		if n := strings.Count(hard, "leaves the file untouched instead of partly rewritten"); n != 1 {
			t.Errorf("%s: Hard rules state the atomic-edit rule %d times, want once", a.Name, n)
		}
	}
}

// The parallel-calls section states what the runtime guarantees: calls on one
// file run in the order written, calls on different files at once — and names
// the one thing it cannot order, a shell command that changes files. Reads,
// the calls that can always run together, are pushed toward one block for
// every code-facing agent.
func TestParallelCallsPromptStatesTheSameFileOrder(t *testing.T) {
	writer := parallelToolCallsPrompt(true, true)
	for _, want := range []string{
		"Calls on the same file run in the order you write them",
		"sees the edit",
		"The shell is the one thing the runtime cannot order",
		`joined with "&&"`,
		"Reading several parts of one file",
	} {
		if !strings.Contains(writer, want) {
			t.Errorf("writer prompt missing %q", want)
		}
	}
	for _, stale := range []string{"order is unspecified", "on every file you touched"} {
		if strings.Contains(writer, stale) {
			t.Errorf("writer prompt still says %q, which the runtime no longer does", stale)
		}
	}
	for _, a := range []Agent{BuildAgent, TaskAgent} {
		if strings.Contains(a.System, "order is unspecified") {
			t.Errorf("%s: prompt still says same-file order is unspecified", a.Name)
		}
	}

	readOnly := parallelToolCallsPrompt(false, true)
	if !strings.Contains(readOnly, "Reading several parts of one file") {
		t.Error("read-only prompt lost the push to batch reads")
	}
	if strings.Contains(readOnly, "The shell is the one thing") {
		t.Error("read-only prompt carries the file-changing shell advice meant for writers")
	}
}
