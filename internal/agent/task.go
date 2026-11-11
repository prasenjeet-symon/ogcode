package agent

import "github.com/prasenjeet-symon/ogcode/internal/provider"

// taskForAgent maps an agent name onto the task kind the provider sends to the
// router, so an index pass and a user's turn can be served by different models.
// It names the same agents as GetAgent, and an unknown name falls back to the
// build agent's kind for the same reason GetAgent falls back to it.
func taskForAgent(name string) provider.Task {
	switch name {
	case "index":
		return provider.TaskIndex
	case "note":
		return provider.TaskNote
	case "search":
		return provider.TaskSearch
	case "subagent":
		return provider.TaskSubagent
	case "memory-recall":
		return provider.TaskRecall
	default:
		return provider.TaskTurn
	}
}
