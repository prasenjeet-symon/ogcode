package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// TestSubagentAgent_ReadOnlyDepth1 locks the two safety invariants of the task
// sub-agent: it is read-only (no write/edit/bash) and depth-1 (no `task` tool, so
// it cannot spawn further sub-agents), while its parents can delegate to it.
func TestSubagentAgent_ReadOnlyDepth1(t *testing.T) {
	for _, forbidden := range []string{"task", "write", "edit", "bash"} {
		if SubagentAgent.HasTool(forbidden) {
			t.Errorf("SubagentAgent must NOT have %q (read-only, depth-1 invariant)", forbidden)
		}
	}
	for _, want := range []string{"read", "glob", "grep", "codebase_map"} {
		if !SubagentAgent.HasTool(want) {
			t.Errorf("SubagentAgent should have investigation tool %q", want)
		}
	}
	for _, parent := range []Agent{BuildAgent, PlanAgent, TaskAgent} {
		if !parent.HasTool("task") {
			t.Errorf("%s agent should be able to delegate via the task tool", parent.ID)
		}
	}
	if got := GetAgent("subagent"); got.ID != "subagent" {
		t.Errorf("GetAgent(\"subagent\").ID = %q, want \"subagent\"", got.ID)
	}
}

// TestRunTaskSession_ReturnsFinalTextAndCleansUp drives RunTaskSession end-to-end
// against a mock provider that returns a final answer, and asserts the answer is
// returned and the ephemeral sub-agent session is deleted afterward.
func TestRunTaskSession_ReturnsFinalTextAndCleansUp(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := session.SetModelCapability(database, &session.ModelCapability{ModelID: "mock-model", SupportsImages: false, ProbedAt: session.Now()}); err != nil {
		t.Fatalf("set capability: %v", err)
	}

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	reg.Register(&toolRoundsProvider{rounds: 0}) // rounds:0 → first call returns final text

	dir := t.TempDir()
	lr := &LoopRunner{
		Store:    store,
		Bus:      bus.New(64),
		Registry: reg,
		Tools:    tool.NewRegistry(),
		Dir:      dir,
		MaxSteps: 20,
	}

	type res struct {
		answer string
		err    error
	}
	done := make(chan res, 1)
	go func() {
		a, e := lr.RunTaskSession(context.Background(), "investigate X", "find where X is used and report back", dir, "mock-model")
		done <- res{a, e}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("RunTaskSession error: %v", r.err)
		}
		if r.answer != "final answer" {
			t.Errorf("RunTaskSession answer = %q, want %q", r.answer, "final answer")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunTaskSession did not complete in time")
	}

	// The ephemeral sub-agent session must be cleaned up.
	sessions, err := store.List(dir)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected the ephemeral sub-agent session to be deleted, but %d session(s) remain", len(sessions))
	}
}
