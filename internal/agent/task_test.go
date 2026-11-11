package agent

import (
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
)

// TestTaskForAgent pins the agent name → task kind mapping the loop sends to
// the router. It must name the same agents as GetAgent.
func TestTaskForAgent(t *testing.T) {
	tests := []struct {
		agent string
		want  provider.Task
	}{
		{"index", provider.TaskIndex},
		{"note", provider.TaskNote},
		{"search", provider.TaskSearch},
		{"subagent", provider.TaskSubagent},
		{"memory-recall", provider.TaskRecall},
		{"build", provider.TaskTurn},
		{"unknown-agent", provider.TaskTurn},
		{"", provider.TaskTurn},
	}
	for _, tt := range tests {
		if got := taskForAgent(tt.agent); got != tt.want {
			t.Errorf("taskForAgent(%q) = %q, want %q", tt.agent, got, tt.want)
		}
	}
}

// TestTaskForAgentCoversKnownAgents fails when a new agent is added to GetAgent
// without a task kind, which would silently route its work as a user turn.
func TestTaskForAgentCoversKnownAgents(t *testing.T) {
	for _, name := range []string{"index", "note", "search", "subagent", "memory-recall"} {
		if got := taskForAgent(name); got == provider.TaskTurn {
			t.Errorf("agent %q has no task kind; add it to taskForAgent", name)
		}
	}
}
