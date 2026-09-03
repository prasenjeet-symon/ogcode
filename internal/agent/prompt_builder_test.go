package agent

import (
	"os/exec"
	"strings"
	"testing"
)

func TestMemoryMDPrompt_CanWrite(t *testing.T) {
	prompt := memoryMDPrompt(true, true, true)

	if !strings.Contains(prompt, "### How to maintain MEMORY.md") {
		t.Error("expected 'How to maintain' heading when canWriteFiles=true")
	}
	if !strings.Contains(prompt, "Use the edit tool for targeted updates") {
		t.Error("expected edit tool mention when canWriteFiles=true")
	}
	if !strings.Contains(prompt, "Use the write tool only") {
		t.Error("expected write tool mention when canWriteFiles=true")
	}
	if strings.Contains(prompt, "Do not modify MEMORY.md") {
		t.Error("did not expect the do-not-modify rule when canWriteFiles=true")
	}
}

func TestMemoryMDPrompt_ReadOnly(t *testing.T) {
	prompt := memoryMDPrompt(false, true, true)

	if !strings.Contains(prompt, "### How to use MEMORY.md") {
		t.Error("expected 'How to use' heading when canWriteFiles=false")
	}
	if strings.Contains(prompt, "Use the edit tool") {
		t.Error("did not expect edit tool mention when canWriteFiles=false")
	}
	if strings.Contains(prompt, "Use the write tool") {
		t.Error("did not expect write tool mention when canWriteFiles=false")
	}
	if !strings.Contains(prompt, "Do not modify MEMORY.md") {
		t.Error("expected the do-not-modify rule when canWriteFiles=false")
	}
}

func TestMemoryMDPrompt_CommonSections(t *testing.T) {
	// Both variants should include these common sections
	for _, canWrite := range []bool{true, false} {
		prompt := memoryMDPrompt(canWrite, true, true)
		for _, sub := range []string{
			"### Purpose",
			"### What belongs in MEMORY.md",
			"### What does NOT belong in MEMORY.md",
			"### How it differs from AGENT.md",
		} {
			if !strings.Contains(prompt, sub) {
				t.Errorf("expected section %q in prompt (canWrite=%v)", sub, canWrite)
			}
		}
	}
}

func TestMarkdownCapabilitiesPrompt(t *testing.T) {
	prompt := markdownCapabilitiesPrompt(true, false)
	if !strings.Contains(prompt, "Mermaid diagrams") {
		t.Error("expected Mermaid mention in markdown capabilities prompt")
	}
	if !strings.Contains(prompt, "LaTeX math") {
		t.Error("expected LaTeX math mention in markdown capabilities prompt")
	}
	if !strings.Contains(prompt, "LaTeX documents") {
		t.Error("expected LaTeX documents mention in markdown capabilities prompt")
	}
	if !strings.Contains(prompt, "latex_to_pdf") {
		t.Error("expected latex_to_pdf tool mention in markdown capabilities prompt")
	}
	if !strings.Contains(prompt, "HTML/CSS/JS") {
		t.Error("expected HTML/CSS/JS mention in markdown capabilities prompt")
	}
	if !strings.Contains(prompt, "sandboxed iframe") {
		t.Error("expected sandboxed iframe mention in markdown capabilities prompt")
	}
}

func TestSystemReminderPrompt(t *testing.T) {
	prompt := systemReminderPrompt()
	if !strings.Contains(prompt, "<system-reminder>") {
		t.Error("expected <system-reminder> tag in systemReminderPrompt")
	}
	if !strings.Contains(prompt, "</system-reminder>") {
		t.Error("expected closing </system-reminder> tag in systemReminderPrompt")
	}
	if !strings.Contains(prompt, "Current date:") {
		t.Error("expected 'Current date:' in systemReminderPrompt")
	}
}

func TestBuildSystemPrompt_NoCurrentDate(t *testing.T) {
	// The cacheable base — entry [0], the only block the provider marks with
	// cache_control — must NOT contain the current date. It is injected as a
	// separate system-reminder entry so the cached prefix stays byte-for-byte
	// identical across turns.
	prompt := buildSystemPromptEntries(BuildAgent, "/tmp/test", false, "", "", 1920, 1080, "", -1)[0]
	if strings.Contains(prompt, "Current date:") {
		t.Error("did not expect 'Current date:' in the base system prompt (it should be in a separate system-reminder entry)")
	}
	// Working directory and platform should still be present (they're static).
	if !strings.Contains(prompt, "Working directory:") {
		t.Error("expected 'Working directory:' in the base system prompt")
	}
	if !strings.Contains(prompt, "Platform:") {
		t.Error("expected 'Platform:' in the base system prompt")
	}
	// OS version and shell info are static per session — they belong in the
	// cacheable prefix, not in the per-turn system-reminder.
	if !strings.Contains(prompt, "OS:") {
		t.Error("expected 'OS:' line in the base system prompt")
	}
	if !strings.Contains(prompt, "Shell:") {
		t.Error("expected 'Shell:' line in the base system prompt")
	}
	if !strings.Contains(prompt, "POSIX-compatible shell") {
		t.Error("expected POSIX-compatible shell guidance in the base system prompt")
	}
}

func TestBuildSystemPrompt_OSEnvStaticAcrossAgents(t *testing.T) {
	// OS/shell lines are derived from the host, not the agent — every agent
	// gets the same cacheable prefix lines, so the prompt stays cache-stable
	// regardless of which agent runs.
	detectedOSEnv = nil // force re-detection
	buildPrompt := buildSystemPrompt(BuildAgent, "/tmp/test", false, "", "", 0, 0)
	planPrompt := buildSystemPrompt(PlanAgent, "/tmp/test", false, "", "", 0, 0)
	notePrompt := buildSystemPrompt(NoteAgent, "/tmp/test", false, "", "", 0, 0)

	for name, p := range map[string]string{"build": buildPrompt, "plan": planPrompt, "note": notePrompt} {
		if !strings.Contains(p, "OS:") {
			t.Errorf("%s prompt: expected 'OS:' line", name)
		}
		if !strings.Contains(p, "Shell:") {
			t.Errorf("%s prompt: expected 'Shell:' line", name)
		}
		if !strings.Contains(p, "sh -c") {
			t.Errorf("%s prompt: expected 'sh -c' mention", name)
		}
	}
	// All three should share the identical OS/Shell block since it's host-derived.
	buildOS := substringAfter(buildPrompt, "\nOS:")
	planOS := substringAfter(planPrompt, "\nOS:")
	noteOS := substringAfter(notePrompt, "\nOS:")
	if buildOS != planOS || planOS != noteOS {
		t.Error("expected identical OS/Shell block across all agents (host-derived, not agent-derived)")
	}
}

func TestOSEnvPrompt_CachedAndNonEmpty(t *testing.T) {
	// Reset cache so the first call performs detection.
	detectedOSEnv = nil
	info := getOSEnv()
	if info.OSVersion == "" {
		t.Error("expected non-empty OSVersion from getOSEnv")
	}
	// The prompt must mention POSIX sh so the agent avoids bashisms.
	p := osEnvPrompt(true)
	if !strings.Contains(p, "POSIX-compatible shell") {
		t.Error("expected POSIX-compatible shell guidance in osEnvPrompt")
	}
	if !strings.Contains(p, "sh -c") {
		t.Error("expected 'sh -c' mention in osEnvPrompt")
	}
	if !strings.Contains(p, "OS:") {
		t.Error("expected 'OS:' line in osEnvPrompt")
	}
	if !strings.Contains(p, "Shell:") {
		t.Error("expected 'Shell:' line in osEnvPrompt")
	}
}

// substringAfter returns the portion of s after the first occurrence of sep,
// including everything up to the next newline-boundary block. Used to compare
// the OS/Shell block across agent prompts.
func substringAfter(s, sep string) string {
	idx := strings.Index(s, sep)
	if idx == -1 {
		return ""
	}
	rest := s[idx+len(sep):]
	// Trim to the first two lines (OS + Shell) so the comparison is stable.
	lines := strings.SplitN(rest, "\n", 3)
	if len(lines) > 2 {
		return strings.Join(lines[:2], "\n")
	}
	return strings.Join(lines, "\n")
}

func TestViewportPrompt(t *testing.T) {
	// With valid dimensions
	prompt := viewportPrompt(1920, 1080)
	if !strings.Contains(prompt, "1920") {
		t.Error("expected width 1920 in viewport prompt")
	}
	if !strings.Contains(prompt, "1080") {
		t.Error("expected height 1080 in viewport prompt")
	}
	if !strings.Contains(prompt, "Rendering viewport") {
		t.Error("expected 'Rendering viewport' heading")
	}
	if !strings.Contains(prompt, "responsive") {
		t.Error("expected responsive design guidance in viewport prompt")
	}

	// With zero dimensions (should return empty)
	prompt = viewportPrompt(0, 0)
	if prompt != "" {
		t.Error("expected empty prompt when dimensions are zero")
	}

	// With negative dimensions (should return empty)
	prompt = viewportPrompt(-1, 100)
	if prompt != "" {
		t.Error("expected empty prompt when dimensions are negative")
	}
}

func TestParallelToolCallsPrompt(t *testing.T) {
	prompt := parallelToolCallsPrompt(true, true)
	if !strings.Contains(prompt, "Parallel tool calls") {
		t.Error("expected 'Parallel tool calls' heading")
	}
	if !strings.Contains(prompt, "independent") {
		t.Error("expected 'independent' mention in parallel tool calls prompt")
	}
}

// The section has to do more than permit batching — permission was what the
// earlier wording gave, and a model reading it still explored one file per
// turn. It has to say batching is the default, give a test the model can apply
// before it has decided anything, and name the case where batching corrupts
// work rather than merely wasting a round trip.
func TestParallelToolCallsPrompt_PushesBatchingAsTheDefault(t *testing.T) {
	prompt := parallelToolCallsPrompt(true, true)

	for _, want := range []string{
		"Batching is the default", // the framing, not a permission
		"round trip",              // what a sequential call actually costs
		"same block",              // the concrete instruction
		"anti-pattern",            // the habit being corrected
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("parallel tool calls prompt missing %q", want)
		}
	}
}

// The runtime serializes same-file mutations behind a per-path mutex
// (internal/tool/pathlock.go): the second edit re-reads fresh content after the
// first applies, and an overlapping anchor fails cleanly ("old_string not
// found") instead of corrupting the file. So batching disjoint edits to one
// file is safe and the prompt must say so — the old blanket prohibition was
// stricter than the runtime and cost a round trip for nothing. The prompt still
// has to describe what actually breaks: write-vs-edit on one file, whose order
// is unspecified, and the clean-failure message the model should re-issue from.
func TestParallelToolCallsPrompt_SameFileEditGuidance(t *testing.T) {
	prompt := parallelToolCallsPrompt(true, true)

	for _, want := range []string{
		"serializes mutations to the same path",    // the runtime contract the permission rests on
		"old_string not found",                     // the clean-failure message and how to recover
		"Never batch a \"write\" with an \"edit\"", // the one combination still forbidden
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if !strings.Contains(prompt, "old_string") {
		t.Error("prompt does not say what anchors the same-file edits")
	}
}

// The half about edits is offered only to agents that can edit. A read-only
// agent told to sequence its edits carefully has been handed an instruction it
// cannot act on, and the examples in the shared body have to name tools every
// code-facing agent actually holds — otherwise the guidance points at something
// ForAgent never offers, which fails silently.
func TestParallelToolCallsPrompt_ReadOnlyVariantOmitsEditAdvice(t *testing.T) {
	readOnly := parallelToolCallsPrompt(false, true)

	for _, unwanted := range []string{"old_string", "check_syntax", "edits to the same file"} {
		if strings.Contains(readOnly, unwanted) {
			t.Errorf("read-only variant mentions %q, which those agents cannot use", unwanted)
		}
	}
	// It still has to carry the part that applies to everyone.
	if !strings.Contains(readOnly, "Batching is the default") {
		t.Error("read-only variant lost the batching guidance itself")
	}
}

// The advice is only true because the loop really does run a block's calls
// concurrently. If that ever changed, the prompt would be telling every agent
// something false — so the two are pinned together.
func TestParallelToolCallsPrompt_MatchesWhatTheLoopDoes(t *testing.T) {
	// executeReadyToolCalls spawns one goroutine per ready call and waits for
	// all of them; there is no serialization by tool type. This test asserts the
	// prompt is offered to every agent that can act on it.
	for _, a := range codeFacingAgents() {
		if !strings.Contains(a.System, "Parallel tool calls") {
			t.Errorf("%s: prompt does not carry the parallel tool calls section", a.Name)
		}
	}
}

func TestNoPackageManagerDirsPrompt(t *testing.T) {
	prompt := noPackageManagerDirsPrompt()
	if !strings.Contains(prompt, "node_modules") {
		t.Error("expected 'node_modules' mention in no-package-manager-dirs prompt")
	}
	if !strings.Contains(prompt, "third-party code") {
		t.Error("expected 'third-party code' mention")
	}
}

func TestProjectNotesPrompt_CanWrite(t *testing.T) {
	prompt := projectNotesPrompt(true)

	if !strings.Contains(prompt, "## Project notes") {
		t.Error("expected 'Project notes' heading when canWriteFiles=true")
	}
	if !strings.Contains(prompt, ".ogcode/notes/*.md") {
		t.Error("expected '.ogcode/notes/*.md' mention when canWriteFiles=true")
	}
	if !strings.Contains(prompt, "managed exclusively by the NoteAgent") {
		t.Error("expected NoteAgent restriction when canWriteFiles=true")
	}
	if !strings.Contains(prompt, "Do not create, modify, or delete any files in .ogcode/notes/") {
		t.Error("expected explicit read-only restriction for notes dir when canWriteFiles=true")
	}
	if !strings.Contains(prompt, "You may only read notes") {
		t.Error("expected read-only permission wording when canWriteFiles=true")
	}
}

func TestProjectNotesPrompt_ReadOnly(t *testing.T) {
	prompt := projectNotesPrompt(false)

	if !strings.Contains(prompt, "## Project notes") {
		t.Error("expected 'Project notes' heading when canWriteFiles=false")
	}
	if !strings.Contains(prompt, ".ogcode/notes/*.md") {
		t.Error("expected '.ogcode/notes/*.md' mention when canWriteFiles=false")
	}
	// Read-only agents should NOT see the NoteAgent restriction since they can't write anyway
	if strings.Contains(prompt, "managed exclusively by the NoteAgent") {
		t.Error("did not expect NoteAgent restriction when canWriteFiles=false")
	}
	if strings.Contains(prompt, "Do not create, modify, or delete") {
		t.Error("did not expect write restriction wording when canWriteFiles=false (they can't write at all)")
	}
}

func TestProjectNotesPrompt_CommonSections(t *testing.T) {
	// Both variants should include common guidance
	for _, canWrite := range []bool{true, false} {
		prompt := projectNotesPrompt(canWrite)
		for _, sub := range []string{
			"## Project notes",
			".ogcode/notes/",
			"don't repeat what is already documented",
		} {
			if !strings.Contains(prompt, sub) {
				t.Errorf("expected %q in projectNotesPrompt (canWrite=%v)", sub, canWrite)
			}
		}
	}
}

func TestBuildAgent_HasExpectedTools(t *testing.T) {
	if !BuildAgent.HasTool("write") {
		t.Error("BuildAgent should have write tool")
	}
	if !BuildAgent.HasTool("edit") {
		t.Error("BuildAgent should have edit tool")
	}
	if !BuildAgent.HasTool("memory_recall") {
		t.Error("BuildAgent should have memory_recall tool")
	}
	if !BuildAgent.HasTool("project_memory_recall") {
		t.Error("BuildAgent should have project_memory_recall tool")
	}
}

func TestPlanAgent_HasExpectedTools(t *testing.T) {
	if PlanAgent.HasTool("write") {
		t.Error("PlanAgent should not have write tool")
	}
	if PlanAgent.HasTool("edit") {
		t.Error("PlanAgent should not have edit tool")
	}
	if !PlanAgent.HasTool("memory_recall") {
		t.Error("PlanAgent should have memory_recall tool")
	}
	if !PlanAgent.HasTool("project_memory_recall") {
		t.Error("PlanAgent should have project_memory_recall tool")
	}
	if !PlanAgent.HasTool("read") {
		t.Error("PlanAgent should have read tool")
	}
}

func TestNoteAgent_HasExpectedTools(t *testing.T) {
	if NoteAgent.HasTool("write") {
		t.Error("NoteAgent should not have write tool")
	}
	if NoteAgent.HasTool("edit") {
		t.Error("NoteAgent should not have edit tool")
	}
	if NoteAgent.HasTool("memory_recall") {
		t.Error("NoteAgent should not have memory_recall tool (single-iteration agent)")
	}
	if NoteAgent.HasTool("project_memory_recall") {
		t.Error("NoteAgent should not have project_memory_recall tool (single-iteration agent)")
	}
	if !NoteAgent.HasTool("codebase_map") {
		t.Error("NoteAgent should have codebase_map tool")
	}
	if !NoteAgent.HasTool("deep_search") {
		t.Error("NoteAgent should have deep_search tool")
	}
	if !NoteAgent.HasTool("read") {
		t.Error("NoteAgent should have read tool")
	}
}

func TestBreakdownAgent_HasExpectedTools(t *testing.T) {
	if BreakdownAgent.HasTool("write") {
		t.Error("BreakdownAgent should not have write tool")
	}
	if BreakdownAgent.HasTool("edit") {
		t.Error("BreakdownAgent should not have edit tool")
	}
	if BreakdownAgent.HasTool("memory_recall") {
		t.Error("BreakdownAgent should not have memory_recall tool (single-iteration agent)")
	}
	if !BreakdownAgent.HasTool("submit_task_breakdown") {
		t.Error("BreakdownAgent should have submit_task_breakdown tool")
	}
}

func TestBuildAgent_SystemPrompt_ContainsSharedSections(t *testing.T) {
	// Verify that BuildAgent's system prompt includes the shared prompt sections
	if !strings.Contains(BuildAgent.System, "Parallel tool calls") {
		t.Error("BuildAgent system prompt should reference parallel tool calls section")
	}
	if !strings.Contains(BuildAgent.System, "Error recovery") {
		t.Error("BuildAgent system prompt should include error recovery section")
	}
	if !strings.Contains(BuildAgent.System, "Project notes") {
		t.Error("BuildAgent system prompt should mention project notes")
	}
	// BuildAgent has write/edit tools, so its project notes section must include
	// the read-only restriction for .ogcode/notes/
	if !strings.Contains(BuildAgent.System, "managed exclusively by the NoteAgent") {
		t.Error("BuildAgent system prompt should include NoteAgent restriction for notes directory")
	}
	if !strings.Contains(BuildAgent.System, "Do not create, modify, or delete any files in .ogcode/notes/") {
		t.Error("BuildAgent system prompt should include read-only restriction for notes directory")
	}
}

func TestBreakdownAgent_SystemPrompt_ContainsNotes(t *testing.T) {
	// Verify BreakdownAgent mentions project notes and a per-task verification step.
	if !strings.Contains(BreakdownAgent.System, "Read project notes") {
		t.Error("BreakdownAgent should mention reading project notes")
	}
	if !strings.Contains(BreakdownAgent.System, "verification step") {
		t.Error("BreakdownAgent should require a per-task verification step")
	}
	// The worked example must not reference a fictional internal API.
	if strings.Contains(BreakdownAgent.System, "PromptBuilder") {
		t.Error("BreakdownAgent example should not reference the non-existent PromptBuilder type")
	}
}

// TestBuildSystemPrompt_FinalInstructionLast verifies an agent's FinalInstruction
// is pinned to the very end of the assembled prompt (after viewport and every
// other dynamic section), and that agents without one get no stray trailer.
func TestBuildSystemPrompt_FinalInstructionLast(t *testing.T) {
	if NoteAgent.FinalInstruction == "" {
		t.Fatal("NoteAgent should define a FinalInstruction")
	}
	// Viewport dims are provided so a dynamic section is appended before the
	// final instruction — proving it really is last.
	p := buildSystemPrompt(NoteAgent, "/tmp/proj", false, "", "", 1920, 1080)
	if !strings.HasSuffix(p, NoteAgent.FinalInstruction) {
		t.Error("NoteAgent FinalInstruction should be the final content of the assembled prompt")
	}
	if BuildAgent.FinalInstruction != "" {
		t.Error("BuildAgent should not define a FinalInstruction")
	}
	bp := buildSystemPrompt(BuildAgent, "/tmp/proj", false, "", "", 0, 0)
	if strings.HasSuffix(bp, "Reminder:") {
		t.Error("BuildAgent prompt should not gain a stray final reminder")
	}
}

func TestGetAgent(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"plan", "plan"},
		{"breakdown", "breakdown"},
		{"note", "note"},
		{"build", "build"},
		{"task", "task"},
		{"unknown", "build"}, // default
		{"", "build"},        // default
	}
	for _, tc := range tests {
		agent := GetAgent(tc.name)
		if agent.ID != tc.expected {
			t.Errorf("GetAgent(%q) = %q, want %q", tc.name, agent.ID, tc.expected)
		}
	}
}

// TestBuildVsTaskAgent_Framing locks in the fix that BuildAgent (interactive
// Build Mode) and TaskAgent (headless worktree execution) share tools but frame
// commits and the source-of-truth differently. The interactive agent must NOT
// tell the developer their uncommitted work will be lost or that it must commit.
func TestBuildVsTaskAgent_Framing(t *testing.T) {
	// Same capabilities.
	if len(BuildAgent.Tools) != len(TaskAgent.Tools) {
		t.Fatalf("BuildAgent and TaskAgent should share the same toolset")
	}

	// Task-only framing must appear in TaskAgent and NOT in BuildAgent.
	taskOnly := []string{
		"executing a single implementation task in a dedicated git worktree",
		"You MUST commit",
		"uncommitted changes will be lost after the task completes",
		"Read the task description carefully",
	}
	for _, s := range taskOnly {
		if !strings.Contains(TaskAgent.System, s) {
			t.Errorf("TaskAgent.System should contain %q", s)
		}
		if strings.Contains(BuildAgent.System, s) {
			t.Errorf("BuildAgent.System (interactive) must NOT contain task-only framing %q", s)
		}
	}

	// Interactive-only framing must appear in BuildAgent and NOT in TaskAgent.
	interactiveOnly := []string{
		"Do not commit unless asked",
		"nothing is lost by staying uncommitted",
	}
	for _, s := range interactiveOnly {
		if !strings.Contains(BuildAgent.System, s) {
			t.Errorf("BuildAgent.System (interactive) should contain %q", s)
		}
		if strings.Contains(TaskAgent.System, s) {
			t.Errorf("TaskAgent.System must NOT contain interactive-only framing %q", s)
		}
	}

	// Shared sections must be present in both.
	for _, s := range []string{"Parallel tool calls", "Error recovery", "Project notes"} {
		if !strings.Contains(BuildAgent.System, s) {
			t.Errorf("BuildAgent.System should contain shared section %q", s)
		}
		if !strings.Contains(TaskAgent.System, s) {
			t.Errorf("TaskAgent.System should contain shared section %q", s)
		}
	}
}

func TestProjectIndexPrompt(t *testing.T) {
	// Build role — ends with "make changes"
	prompt := projectIndexPrompt("build", true, true)

	for _, sub := range []string{
		"Mandatory: Use Project Index Before Exploration",
		"codebase_map",
		"MANDATORY FIRST STEP",
		"has not been indexed", // the empty-index escape hatch
		"glob and grep",        // ...and what to do instead
		"subdir",
		"Then make changes",
	} {
		if !strings.Contains(prompt, sub) {
			t.Errorf("expected %q in projectIndexPrompt(build)", sub)
		}
	}

	// Plan role — read-only, ends with "produce your plan" instead of "make changes"
	planPrompt := projectIndexPrompt("plan", true, true)
	if !strings.Contains(planPrompt, "Then produce your plan") {
		t.Error("expected plan role workflow to end with 'Then produce your plan'")
	}
	if strings.Contains(planPrompt, "Then make changes") {
		t.Error("did not expect 'make changes' in plan role workflow (read-only agent)")
	}

	// Note role — read-only, ends with "produce your note"
	notePrompt := projectIndexPrompt("note", true, true)
	if !strings.Contains(notePrompt, "Then produce your note") {
		t.Error("expected note role workflow to end with 'Then produce your note'")
	}
	if strings.Contains(notePrompt, "Then make changes") {
		t.Error("did not expect 'make changes' in note role workflow (read-only agent)")
	}
}

func TestBuildAgent_SystemPrompt_ContainsProjectIndex(t *testing.T) {
	// BuildAgent should contain the project index prompt since it has the codebase_map tool
	if !strings.Contains(BuildAgent.System, "Mandatory: Use Project Index Before Exploration") {
		t.Error("BuildAgent system prompt should include project index section")
	}
	if !strings.Contains(BuildAgent.System, "codebase_map") {
		t.Error("BuildAgent system prompt should reference codebase_map tool")
	}
}

func TestPlanAgent_SystemPrompt_ContainsProjectIndex(t *testing.T) {
	// PlanAgent should contain the project index prompt since it has the codebase_map tool
	if !strings.Contains(PlanAgent.System, "Mandatory: Use Project Index Before Exploration") {
		t.Error("PlanAgent system prompt should include project index section")
	}
	if !strings.Contains(PlanAgent.System, "codebase_map") {
		t.Error("PlanAgent system prompt should reference codebase_map tool")
	}
	// The plan agent is read-only — its workflow must not instruct it to make changes
	if strings.Contains(PlanAgent.System, "Then make changes") {
		t.Error("PlanAgent system prompt should not include 'make changes' workflow step (read-only agent)")
	}
	if !strings.Contains(PlanAgent.System, "Then produce your plan") {
		t.Error("PlanAgent system prompt should include 'Then produce your plan' workflow step")
	}
	// The plan agent's own start-of-session steps must reinforce codebase_map (not just read/glob/grep)
	if !strings.Contains(PlanAgent.System, "Start with **codebase_map**") {
		t.Error("PlanAgent step 2 should explicitly start with codebase_map")
	}
}

func TestNoteAgent_SystemPrompt_ContainsProjectIndex(t *testing.T) {
	// NoteAgent should contain the project index prompt since it has the codebase_map tool
	if !strings.Contains(NoteAgent.System, "Mandatory: Use Project Index Before Exploration") {
		t.Error("NoteAgent system prompt should include project index section")
	}
	if !strings.Contains(NoteAgent.System, "codebase_map") {
		t.Error("NoteAgent system prompt should reference codebase_map tool")
	}
}

func TestLatexInfoPrompt_WithLatex(t *testing.T) {
	// Reset cached result so we re-detect
	detectedLatexEnv = nil
	prompt := latexInfoPrompt()

	// If pdflatex is available on the test system, check that the prompt includes useful info
	if _, err := exec.LookPath("pdflatex"); err == nil {
		if prompt == "" {
			t.Error("expected non-empty latexInfoPrompt when pdflatex is available")
		}
		if !strings.Contains(prompt, "LaTeX environment") {
			t.Error("expected 'LaTeX environment' heading in latexInfoPrompt")
		}
		if !strings.Contains(prompt, "pdflatex is available") {
			t.Error("expected 'pdflatex is available' in latexInfoPrompt")
		}
		if !strings.Contains(prompt, "Version") {
			t.Error("expected 'Version' in latexInfoPrompt")
		}
		if !strings.Contains(prompt, "compatible") {
			t.Error("expected compatibility guidance in latexInfoPrompt")
		}
		// Should include standard doc classes
		if !strings.Contains(prompt, "article") {
			t.Error("expected 'article' doc class in latexInfoPrompt")
		}
	} else {
		// pdflatex not installed — prompt should be empty
		if prompt != "" {
			t.Error("expected empty latexInfoPrompt when pdflatex is not available")
		}
	}
}

func TestLatexInfoPrompt_WithoutLatex(t *testing.T) {
	// Force the cached env to simulate no pdflatex
	detectedLatexEnv = &latexEnv{Available: false}
	prompt := latexInfoPrompt()
	if prompt != "" {
		t.Errorf("expected empty prompt when pdflatex not available, got: %q", prompt)
	}
	// Reset for other tests
	detectedLatexEnv = nil
}

func TestBuildSystemPrompt_InjectsLatexInfo(t *testing.T) {
	// Reset cached result
	detectedLatexEnv = nil

	// BuildAgent has latex_to_pdf tool — should get LaTeX info injected
	if _, err := exec.LookPath("pdflatex"); err == nil {
		prompt := buildSystemPrompt(BuildAgent, "/tmp/test", false, "", "", 1920, 1080)
		if !strings.Contains(prompt, "LaTeX environment") {
			t.Error("expected LaTeX environment section in BuildAgent prompt when pdflatex is available")
		}
	}

	// PlanAgent does NOT have latex_to_pdf tool — should NOT get LaTeX info
	prompt := buildSystemPrompt(PlanAgent, "/tmp/test", false, "", "", 1920, 1080)
	if strings.Contains(prompt, "LaTeX environment") {
		t.Error("did NOT expect LaTeX environment section in PlanAgent prompt (no latex_to_pdf tool)")
	}

	// Reset for other tests
	detectedLatexEnv = nil
}

// TestBuildSystemPromptEntries_CacheablePrefixIsViewportInvariant pins the
// invariant that regressed when the viewport was appended to the base prompt:
// entry [0] is the only block providers mark cacheable, and the browser resends
// its window size with every prompt, so a resize must not change [0] by one byte.
func TestBuildSystemPromptEntries_CacheablePrefixIsViewportInvariant(t *testing.T) {
	desktop := buildSystemPromptEntries(BuildAgent, "/tmp/proj", true, "", "", 1920, 1080, "", -1)
	laptop := buildSystemPromptEntries(BuildAgent, "/tmp/proj", true, "", "", 1280, 720, "", -1)
	none := buildSystemPromptEntries(BuildAgent, "/tmp/proj", true, "", "", 0, 0, "", -1)

	if desktop[0] != laptop[0] {
		t.Error("resizing the window changed the cacheable prefix — the tools+system cache is invalidated on every resize")
	}
	if desktop[0] != none[0] {
		t.Error("cacheable prefix differs between a viewport-carrying request and one without")
	}
	if strings.Contains(desktop[0], "Rendering viewport") {
		t.Error("viewport leaked into the cacheable prefix")
	}
	if strings.Contains(desktop[0], "Current date:") {
		t.Error("date leaked into the cacheable prefix")
	}

	// The viewport must still reach the model, just in a later entry.
	rest := strings.Join(desktop[1:], "\n\n")
	if !strings.Contains(rest, "1920") || !strings.Contains(rest, "Rendering viewport") {
		t.Errorf("viewport is missing from the dynamic entries: %q", rest)
	}
	if len(none) != len(desktop)-1 {
		t.Errorf("expected no viewport entry when dimensions are absent: got %d entries, want %d", len(none), len(desktop)-1)
	}
}

// TestBuildSystemPromptEntries_FinalInstructionIsLastEntry guards the pinning
// that moving the viewport could have broken: an output-only agent's format
// constraint must stay the last thing the model reads.
func TestBuildSystemPromptEntries_FinalInstructionIsLastEntry(t *testing.T) {
	entries := buildSystemPromptEntries(NoteAgent, "/tmp/proj", true, "", "", 1920, 1080, "", -1)
	if got := entries[len(entries)-1]; got != NoteAgent.FinalInstruction {
		t.Errorf("last entry = %q, want the agent's FinalInstruction", got)
	}
	// An agent without one must not gain an empty trailing entry.
	for _, e := range buildSystemPromptEntries(BuildAgent, "/tmp/proj", true, "", "", 1920, 1080, "", -1) {
		if strings.TrimSpace(e) == "" {
			t.Error("assembled entries contain an empty block")
		}
	}
}

// The same-file edit guidance is stated twice on purpose: once where batching
// is explained, and once in Hard rules. It is the only piece of batching advice
// whose cost when wrong is a corrupted file rather than a wasted round trip,
// and Hard rules is where an agent looks for what it must not do — 60% into a
// prompt, inside a section about efficiency, is not.
//
// Only the write-capable agents carry it, because only they can commit the
// mistake.
func TestCodingAgentHardRules_SameFileEditBatching(t *testing.T) {
	for _, a := range []Agent{BuildAgent, TaskAgent} {
		hard := a.System[strings.Index(a.System, "## Hard rules"):]
		for _, want := range []string{
			"Same-file \"edit\" calls may batch",       // the permission, stated in Hard rules too
			"serializes mutations to the same path",    // why the permission is safe
			"Never batch a \"write\" with an \"edit\"", // the combination still forbidden
		} {
			if !strings.Contains(hard, want) {
				t.Errorf("%s: Hard rules missing %q", a.Name, want)
			}
		}
	}
	for _, a := range []Agent{PlanAgent, BreakdownAgent, NoteAgent, SubagentAgent} {
		if strings.Contains(a.System, "serializes mutations to the same path") {
			t.Errorf("%s: cannot edit, so the rule is noise in its prompt", a.Name)
		}
	}
}

// A skill body is instructions, and it arrives through a tool. The
// instruction-source boundary says everything a tool returns is data and must
// not be obeyed, so without an explicit carve-out the two rules contradict each
// other: either the model refuses the skill it was just told to load, or it
// learns that "data, not instructions" bends when convenient — and that is the
// rule least able to afford being read as negotiable.
func TestUntrustedContentPrompt_NamesSkillsAsTheOneException(t *testing.T) {
	withSkill := untrustedContentPrompt(true, true, true)
	if !strings.Contains(withSkill, "One exception, and only one") {
		t.Error("boundary does not carve out skills for an agent holding the skill tool")
	}
	if !strings.Contains(withSkill, `"skill" tool`) {
		t.Error("carve-out does not name the tool it applies to")
	}
	// The exception has to stay bounded, or it becomes a way in.
	for _, want := range []string{"reaches beyond its own subject", "downloaded from a configured URL"} {
		if !strings.Contains(withSkill, want) {
			t.Errorf("carve-out missing its limit: %q", want)
		}
	}

	// An agent without the tool must not be told about the exception at all —
	// it can only widen what that agent will accept from a tool result.
	if strings.Contains(untrustedContentPrompt(true, true, false), "One exception") {
		t.Error("agent without the skill tool was given the skill carve-out")
	}
}

// Search and Index are not project-scoped, and a project may simply have no
// AGENT.md. Naming it as a source the agent should be following describes
// something that is not in its prompt.
func TestUntrustedContentPrompt_OnlyClaimsAgentMDWhenPresent(t *testing.T) {
	if !strings.Contains(untrustedContentPrompt(false, true, false), "project's own AGENT.md") {
		t.Error("expected AGENT.md named as a source when it is in the prompt")
	}
	if strings.Contains(untrustedContentPrompt(false, false, false), "AGENT.md") {
		t.Error("named AGENT.md as an instruction source when none was supplied")
	}
}

// The markdown section is built once at package init, so it cannot know whether
// this client reported a viewport. It used to end by pointing at "the viewport
// dimensions provided below" — a section that only exists when one was
// reported, leaving the model chasing a block that was never in the prompt.
func TestMarkdownCapabilities_DoesNotPointAtAnAbsentViewport(t *testing.T) {
	if strings.Contains(markdownCapabilitiesPrompt(true, false), "viewport dimensions provided below") {
		t.Error("markdown section still points at a viewport section that may not exist")
	}
	// The guidance itself must survive, in the block that only renders with the
	// numbers it refers to.
	vp := viewportPrompt(1440, 900)
	if !strings.Contains(vp, "responsive") || !strings.Contains(vp, "HTML") {
		t.Error("viewport section lost the responsive-design guidance")
	}

	// End to end: a client that reports no viewport gets neither.
	bare := strings.Join(buildSystemPromptEntries(BuildAgent, "/proj", false, "", "", 0, 0, "", -1), "\n\n")
	if strings.Contains(bare, "viewport dimensions provided below") {
		t.Error("assembled prompt references viewport dimensions that were never supplied")
	}
}

// Plan, Note and Breakdown hold bash while being read-only by policy, and
// nothing below the prompt enforces that — permission.go has no mode handling
// and bash_safety.go only blocks catastrophic commands. So the shell paragraph
// must not hand them the mutating uses.
func TestProjectIndexPrompt_ShellRuleMatchesWhetherTheAgentMayWrite(t *testing.T) {
	build := projectIndexPrompt("build", true, true)
	if !strings.Contains(build, "builds, tests, linters, formatters, git") {
		t.Error("write-capable agent lost the full shell guidance")
	}

	for _, role := range []string{"plan", "note", "breakdown"} {
		readOnly := projectIndexPrompt(role, true, true)
		if !strings.Contains(readOnly, "inspect, never to change") {
			t.Errorf("%s: shell paragraph does not restrict the shell to inspection", role)
		}
		if !strings.Contains(readOnly, `no "sed -i", no formatter, generator, or build step that rewrites sources`) {
			t.Errorf("%s: shell paragraph does not close the routes around read-only", role)
		}
		if strings.Contains(readOnly, "builds, tests, linters, formatters, git") {
			t.Errorf("%s: shell paragraph still suggests commands that rewrite files", role)
		}
	}

	// An agent with no shell is told nothing about one either way.
	if strings.Contains(projectIndexPrompt("subagent", false, true), "Not The Shell") {
		t.Error("shell-less agent was given the shell paragraph")
	}
}

// The recall tools are registered only when memory is initialised, and Note and
// Breakdown never hold them at all — but both are project-scoped, so both
// receive the MEMORY.md section. Describing a tool there sends the model after a
// call it will never be offered, which is the failure this file gates against
// everywhere else.
func TestMemoryMDPrompt_OnlyExplainsRecallToAgentsThatHaveIt(t *testing.T) {
	if !strings.Contains(memoryMDPrompt(true, true, true), "memory_recall") {
		t.Error("agent holding the recall tools lost the comparison that distinguishes them")
	}
	without := memoryMDPrompt(false, true, false)
	for _, unwanted := range []string{"memory_recall", "project_memory_recall", "<prior_context>"} {
		if strings.Contains(without, unwanted) {
			t.Errorf("MEMORY.md section names %q to an agent that cannot call it", unwanted)
		}
	}

	// End to end: Note is project-scoped and holds neither tool.
	note := strings.Join(buildSystemPromptEntries(NoteAgent, "/proj", true, "", "", 0, 0, "", -1), "\n\n")
	if strings.Contains(note, "memory_recall") {
		t.Error("assembled Note prompt names a recall tool it does not have")
	}
}

// The batching examples were written from file_map and glob, which every agent
// exploring a codebase holds. SearchAgent explores the web — web_search,
// fetch_page, read, grep — so those two bullets named calls it will never be
// offered.
func TestParallelToolCallsPrompt_ExamplesMatchTheAgentsTools(t *testing.T) {
	web := parallelToolCallsPrompt(false, false)
	for _, unwanted := range []string{"file_map", "glob"} {
		if strings.Contains(web, unwanted) {
			t.Errorf("web-facing variant names %q, which SearchAgent does not have", unwanted)
		}
	}
	for _, want := range []string{"web_search", "fetch_page", "Batching is the default"} {
		if !strings.Contains(web, want) {
			t.Errorf("web-facing variant missing %q", want)
		}
	}

	// End to end, against the agent that actually takes this branch.
	assembled := strings.Join(buildSystemPromptEntries(SearchAgent, "/proj", true, "", "", 0, 0, "", -1), "\n\n")
	for _, unwanted := range []string{"file_map", `"glob"`} {
		if strings.Contains(assembled, unwanted) {
			t.Errorf("assembled Search prompt names %q, which it cannot call", unwanted)
		}
	}
}

// codebase_map returns one directory level per call. An agent told only to
// "call codebase_map first" sees a handful of folder names, concludes the index
// is unhelpful and falls back to grep — having paid for the call and discarded
// the labels that were the answer. The prompt has to teach the descent.
func TestProjectIndexPromptTeachesTheDescent(t *testing.T) {
	for _, role := range []string{"build", "plan", "note", "breakdown", "subagent"} {
		prompt := projectIndexPrompt(role, true, true)

		for _, want := range []string{
			"one directory level per call", // what a single call actually returns
			"Navigate by the labels",       // how to choose where to go next
			`subdir`,                       // the mechanism
			"repeat until",                 // that it takes more than one call
		} {
			if !strings.Contains(prompt, want) {
				t.Errorf("projectIndexPrompt(%q) missing %q — the descent is not taught", role, want)
			}
		}

		// The workflow must show more than one map call, or it reads as
		// "map once, then read" and the descent never happens.
		if n := strings.Count(prompt, "codebase_map(subdir="); n < 2 {
			t.Errorf("projectIndexPrompt(%q) shows %d subdir calls in the workflow, want at least 2", role, n)
		}

		// Stale claims from the threshold design must not come back.
		for _, gone := range []string{"large folders are summarized", "labeled tree of the project"} {
			if strings.Contains(prompt, gone) {
				t.Errorf("projectIndexPrompt(%q) still claims %q, which no longer describes the tool", role, gone)
			}
		}
	}
}

// The index-status line is the first thing said about the tool in an indexed
// project, so it must not contradict the descent the main section teaches.
func TestIndexStatusPromptPointsAtTheDescent(t *testing.T) {
	live := indexStatusPrompt(237)
	if !strings.Contains(live, "subdir") {
		t.Errorf("indexed status line does not mention subdir: %q", live)
	}
	if strings.Contains(indexStatusPrompt(0), "descend") {
		t.Errorf("the empty-index line should not send the agent descending: %q", indexStatusPrompt(0))
	}
}
