package agent

import (
	"sync"
	"testing"
)

// A turn a user asked for reports its resources exactly once: the loop opens the
// measurement where the turn starts and closes it as the turn returns, so the
// figure covers the turn rather than one sampling tick.
func TestRunLoop_ReportsTurnResourcesForAUserTurn(t *testing.T) {
	var mu sync.Mutex
	var calls []string

	lr, sessionID, _ := autoIndexFixture(t, autoIndexCase{sessionType: "build"}, nil)
	lr.TurnResources = func(agentID string) func() {
		mu.Lock()
		calls = append(calls, "begin:"+agentID)
		mu.Unlock()
		return func() {
			mu.Lock()
			calls = append(calls, "end:"+agentID)
			mu.Unlock()
		}
	}

	runOneTurn(t, lr, sessionID, "build")

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 || calls[0] != "begin:build" || calls[1] != "end:build" {
		t.Fatalf("TurnResources calls = %v, want [begin:build end:build]", calls)
	}
}

// A nested loop — the index agent, a sub-agent, a memory recall — runs inside a
// turn the user already asked for, so reporting it too would charge one user
// turn several times over.
func TestRunLoop_DoesNotReportNestedAgents(t *testing.T) {
	for _, agentName := range []string{"index", "subagent", "memory-recall", "breakdown", "search"} {
		t.Run(agentName, func(t *testing.T) {
			called := false
			lr, sessionID, _ := autoIndexFixture(t, autoIndexCase{sessionType: "build"}, nil)
			lr.TurnResources = func(string) func() {
				called = true
				return func() {}
			}

			runOneTurn(t, lr, sessionID, agentName)

			if called {
				t.Fatalf("TurnResources fired for the %s agent; it runs inside another turn", agentName)
			}
		})
	}
}

// A nil callback is the CLI and worker default and must leave the loop as it was.
func TestRunLoop_NoTurnResourcesIsHarmless(t *testing.T) {
	lr, sessionID, _ := autoIndexFixture(t, autoIndexCase{sessionType: "build"}, nil)
	runOneTurn(t, lr, sessionID, "build")
}
