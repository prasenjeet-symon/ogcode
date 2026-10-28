package agent

import "testing"

// The schedule tool arms a one-shot reminder, and both of its delivery paths are
// build-only: the per-session catch-up hook at the next turn start requires
// agentID == "build" (loop.go), and the server's reminder poller scans only the
// server's own directory — a reminder armed by any headless agent's session is
// claimed due and then delivered never. schedule rides buildAgentTools alone.
func TestScheduleIsOnlyTheBuildAgent(t *testing.T) {
	build := GetAgent("build")
	if !build.HasTool("schedule") {
		t.Error("the build agent must still carry schedule: its session is the only kind that receives a reminder")
	}
	if !build.canSchedule() {
		t.Error("BuildAgent.canSchedule() = false; the gate must pass for the build agent")
	}
	for _, name := range []string{"subagent", "memory-recall", "note", "index", "search"} {
		a := GetAgent(name)
		if a.HasTool("schedule") {
			t.Errorf("agent %q carries schedule, but neither reminder path reaches its session; the tool must not be offered to it", name)
		}
		if a.canSchedule() {
			t.Errorf("agent %q passes canSchedule; only the interactive build agent's session can receive a reminder", name)
		}
	}
}
