package agent

import (
	"strings"
	"testing"
)

// The agents that change files end the workflow diagram on verification: act
// on the syntax report write and edit already give, check_syntax only for
// files changed another way, then the build and the tests, then running the
// change the way it is used. The diagram is the part a model follows most
// literally, so a workflow that stopped at "make changes" taught it to stop
// there too — and one that stops at the tests never checks that the parts work
// together.
func TestWorkflowEndsOnVerificationForAgentsThatWrite(t *testing.T) {
	build := projectIndexPrompt("build", true, true)
	for _, want := range []string{
		"→ Then make changes",
		"SYNTAX ERROR",
		"→ check_syntax(path)",
		"→ build, run the linter if one is configured, then the tests",
		"→ run it the way it is used",
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
		for _, unwanted := range []string{"check_syntax", "run the tests", "Then make changes", "the way it is used"} {
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
		workflow := p[strings.Index(p, "### Workflow"):strings.Index(p, "### Map a file before reading it")]
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

// The atomic-edit contract is stated once, in Parallel tool calls, and Hard
// rules points at it instead of restating a second, drifting version.
func TestHardRulesStateAtomicEditsOnce(t *testing.T) {
	for _, a := range []Agent{BuildAgent, TaskAgent} {
		if n := strings.Count(a.System, "cannot half-apply a refactor"); n != 1 {
			t.Errorf("%s: states the atomic-edit rule %d times, want once", a.Name, n)
		}
		hard := a.System[strings.Index(a.System, "## Hard rules"):]
		if !strings.Contains(hard, "The full contract, including the one forbidden pair, is in **Parallel tool calls**") {
			t.Errorf("%s: Hard rules does not point at the full edit contract", a.Name)
		}
		if strings.Contains(hard, `Never batch a "write" with an "edit"`) {
			t.Errorf("%s: Hard rules restates the write-vs-edit rule", a.Name)
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

// The build agent is the orchestrator: it decides what the work is, how many
// teammates it takes and what each is briefed to do, and hands the streams out
// rather than taking one itself. A teammate buys exactly two things — wall-clock,
// when independent streams run side by side, and tokens, when a wide exploration
// is billed to the child's context instead of a parent history re-sent on every
// step — so the section states both drivers, makes the side-by-side default
// explicit, sizes the fan-out, says what the parent does with its own time, and
// names the cases that still want one agent.
//
// It also carries the one rail nothing else enforces: a completion is reported
// into a RUNNING turn and no turn starts on its own, so the parent must not end
// its turn with a teammate still working. The role comes before the mechanics,
// and only the build agent gets any of it: teammate and board are build-only
// (canSpawnTeammate, canUseBoard).
func TestBoardPromptPrefersTheSplit(t *testing.T) {
	p := boardPrompt()
	for _, want := range []string{
		"You orchestrate; teammates do the work",
		"Running the streams side by side is the default, not an upgrade",
		"A stream's clock starts when you spawn it",
		"one child reporting the conclusion",
		"Hand the implementation to a teammate",
		"**Speed**",
		"**Tokens**",
		"**How many, and which.**",
		"One teammate per stream that can run without the others",
		"take every stream, not just the obvious one",
		"the serial schedule the batch existed to avoid",
		"Spawn the batch in one tool block",
		"a teammate briefed to implement one stream implements it",
		"**What you do with your own time.**",
		"Never run serially what could run together",
		"do not end the turn while a teammate you spawned is still working",
		"Stay with one agent only when",
		"`task` is cheaper and read-only",
		"park a long investigation off your turn's critical path",
		"right after `update_intent` states your plan",
		"the plan goes first — before the fan-out",
		"may ride the first tool block",
		"`teammate stop <handle>`",
		"nothing is cancelled, and the finished report is delivered to you at your next turn's start",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("board prompt missing %q", want)
		}
	}
	if role, mechanics := strings.Index(p, "You orchestrate"), strings.Index(p, "`teammate spawn` starts"); role < 0 || mechanics < 0 || role > mechanics {
		t.Errorf("board prompt states the mechanics before the role (role at %d, spawn at %d)", role, mechanics)
	}
	// The framings this prompt used to have: the parent as one of the workers,
	// spawning as the thing you do when you run short of room, and the plan
	// mandate that forced update_intent to stand alone in its own block ahead of
	// any other first-block call. None may return.
	for _, stale := range []string{"Reach for a teammate first", "take a stream yourself", "as the opening move of the turn", "the plan goes first, and alone"} {
		if strings.Contains(p, stale) {
			t.Errorf("board prompt still frames the parent as one of the workers (%q)", stale)
		}
	}

	if !strings.Contains(BuildAgent.System, "You orchestrate") {
		t.Error("BuildAgent prompt lost the orchestrator role")
	}
	if !strings.Contains(BuildAgent.System, "how many teammates it takes and which stream each one gets") {
		t.Error("BuildAgent prompt does not point at the fan-out from the planning step")
	}
	for _, a := range []Agent{TaskAgent, PlanAgent, BreakdownAgent, NoteAgent, SubagentAgent} {
		if strings.Contains(a.System, "teammate") {
			t.Errorf("%s: cannot spawn a teammate, so the section is noise in its prompt", a.Name)
		}
	}
}

// Two "first calls of the turn" coexist in the build prompt: the diagram named
// codebase_map the mandatory first exploration step while the update_intent
// section called the plan first, and a literal reader of the diagram alone
// started with the map and never stated a plan. The diagram carries the bridge
// itself, phrased as a fact about plan-stating agents — an instruction to call
// the tool would be a call a plan-less agent cannot follow. A read wave rides
// the same reconciliation: a read routinely reveals another file, and mapping
// that file may share the read block — map-before-read holds file by file.
func TestProjectIndexPromptBridgesThePlanAndCarriesRevealedMaps(t *testing.T) {
	for _, role := range []string{"build", "plan", "note", "breakdown", "subagent"} {
		p := projectIndexPrompt(role, true, true)
		workflow := p[strings.Index(p, "### Workflow"):strings.Index(p, "### Map a file before reading it")]

		for _, want := range []string{
			"update_intent comes before it, for agents that state a plan",
			"map-before-read holds file by file, not block by block",
		} {
			if !strings.Contains(workflow, want) {
				t.Errorf("%s workflow missing %q", role, want)
			}
		}
		if !strings.Contains(p, "read wave may also carry \"file_map\" calls") {
			t.Errorf("%s: loop paragraph lost the revealed-map wave", role)
		}
	}

	// The parallel-batching bullet teaches the same wave to the writer's list.
	if !strings.Contains(parallelToolCallsPrompt(true, true), "read wave may carry \"file_map\" calls") {
		t.Error("batching bullet does not allow file_map calls riding a read wave")
	}
}

// A read fan-out over several files should be one call now that "read" takes a
// files array. The diagram is where the read step is taught, so it names the
// form there; leaving it as one call per path would keep the round trips the
// array exists to remove.
func TestProjectIndexPromptNamesTheMultiFileReadInTheWorkflow(t *testing.T) {
	p := projectIndexPrompt("build", true, true)
	workflow := p[strings.Index(p, "### Workflow"):strings.Index(p, "### Map a file before reading it")]

	for _, want := range []string{`files=[{"path"`, "in the NEXT block"} {
		if !strings.Contains(workflow, want) {
			t.Errorf("workflow missing %q", want)
		}
	}
}

// A search that already printed the file and line has located the code, so a
// map first is a round trip bought twice — the exception must sit beside the
// map-first rule, or the rule reads as a tax on every read. A file nobody has
// located still gets mapped, and a read answered with a map already is one.
func TestWorkflowLetsASearchStandInForTheMap(t *testing.T) {
	p := projectIndexPrompt("build", true, true)
	maps := p[strings.Index(p, "### Map a file before reading it"):]

	for _, want := range []string{
		`A grep that already printed "path:line: text" has answered where in the file to look`,
		"map only the files whose shape is still unknown",
		"never map the same file a second time",
	} {
		if !strings.Contains(maps, want) {
			t.Errorf("map section missing %q", want)
		}
	}
}

// A wave that knows several windows across several files has no reason to spend
// a call per file: the files array is that one call. The workflow is where the
// read step is learned, so the rule sits next to the range example.
func TestWorkflowSpendsOneReadCallPerWave(t *testing.T) {
	p := projectIndexPrompt("build", true, true)
	maps := p[strings.Index(p, "### Map a file before reading it"):]

	for _, want := range []string{
		"**One read call per wave.**",
		`read(files=[{"path": "a.go", "ranges": [...]}, {"path": "b.go", "ranges": [...]}])`,
		"two or more are never two calls",
	} {
		if !strings.Contains(maps, want) {
			t.Errorf("map section missing %q", want)
		}
	}
}
