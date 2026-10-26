package agent

import (
	"strings"
	"testing"
)

// The browser tools drive the user's real Chrome, and the extension grants
// every tab by a tick in its side panel that only the user can flip. The gate
// is therefore about accountability, not reachability: only the interactive
// build agent (and a teammate running as "build") can be held to a grant it
// used. TaskAgent shares BuildAgent's toolset, so the list split alone cannot
// carry this — the pin is the gate (canControlBrowser) plus the shared list
// staying clean. The loop's per-turn append is what turns the gate into the
// actual offer; the board/teammate appends it mirrors are covered by the
// server's session-loop tests, and this pins the agent-id seam the same way
// TestScheduleIsOnlyTheBuildAgent pins its split.
func TestBrowserOpsAreOnlyTheBuildAgent(t *testing.T) {
	build := GetAgent("build")
	if !build.canControlBrowser() {
		t.Error("BuildAgent.canControlBrowser() = false; the gate must pass for the build agent")
	}
	for _, name := range []string{"task", "subagent", "memory-recall", "plan", "breakdown", "note", "index", "search"} {
		a := GetAgent(name)
		if a.canControlBrowser() {
			t.Errorf("agent %q passes canControlBrowser; only the interactive build agent can be held to the tab grants it uses", name)
		}
	}
	for _, id := range codingAgentTools {
		if strings.HasPrefix(id, "browser_") {
			t.Errorf("codingAgentTools carries %q; a task session runs headless in a disposable worktree and must never reach the user's Chrome", id)
		}
	}
}

// TestMatcherTurnKeepsTheBrowserSentinelOnly pins the sentinel rationale: a
// matcher turn on which the user never describes a browser step still has to
// be able to ask whether the bridge is paired, so browser_status rides the
// wire even though every other takeover op waits for the family to be
// offered.
func TestMatcherTurnKeepsTheBrowserSentinelOnly(t *testing.T) {
	tt := newTurnToolset([]string{"grep", "browser_*", "browser_status"}, true)
	if got := strings.Join(tt.wireIDs(), ","); got != "grep,browser_status" {
		t.Fatalf("wire = %q, want grep,browser_status", got)
	}

	before := tt.version
	tt.offer("browser_click")
	if got := strings.Join(tt.wireIDs(), ","); got != "grep,browser_status,browser_click" {
		t.Errorf("wire after offer = %q, want grep,browser_status,browser_click", got)
	}
	if tt.version == before {
		t.Error("offer did not bump the toolset version")
	}
}
