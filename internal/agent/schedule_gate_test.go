package agent

import "testing"

// The schedule tool arms a one-shot reminder, and both of its delivery paths are
// build-only: the per-session catch-up hook at the next turn start requires
// agentID == "build" (loop.go), and the server's reminder poller scans only the
// server's own directory. TaskAgent shared BuildAgent's toolset while running in
// a disposable worktree under <project>/.ogcode/worktrees, so a reminder it
// armed was claimed due and then delivered never — and nothing prunes the
// abandoned rows either. schedule now rides buildAgentTools alone.
func TestScheduleIsOnlyTheBuildAgent(t *testing.T) {
	build := GetAgent("build")
	if !build.HasTool("schedule") {
		t.Error("the build agent must still carry schedule: its session is the only kind that receives a reminder")
	}
	if !build.canSchedule() {
		t.Error("BuildAgent.canSchedule() = false; the gate must pass for the build agent")
	}
	for _, name := range []string{"task", "subagent", "memory-recall", "plan", "breakdown", "note", "index", "search"} {
		a := GetAgent(name)
		if a.HasTool("schedule") {
			t.Errorf("agent %q carries schedule, but neither reminder path reaches its session; the tool must not be offered to it", name)
		}
		if a.canSchedule() {
			t.Errorf("agent %q passes canSchedule; only the interactive build agent's session can receive a reminder", name)
		}
	}
}

// The split must not cost the task agent anything else: codingAgentTools stays
// the shared toolset with schedule absent, and every tool still in it is on the
// task agent's list. Re-adding schedule to the shared list would silently hand
// the tool back to every worktree session, so this pins the shared list clean.
func TestScheduleLeftCodingAgentToolsShared(t *testing.T) {
	for _, id := range codingAgentTools {
		if id == "schedule" {
			t.Error("codingAgentTools carries schedule again; a task session in a worktree cannot receive its reminders — keep it in buildAgentTools (see canSchedule)")
			continue
		}
		if !TaskAgent.HasTool(id) {
			t.Errorf("the shared list holds %q but TaskAgent does not: the schedule split must not cost the task agent any other tool", id)
		}
	}
	if !BuildAgent.HasTool("schedule") {
		t.Error("buildAgentTools lost schedule")
	}
}
