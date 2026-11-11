package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// taskTestArgs builds a task invocation the way the model writes one.
func taskTestArgs(t *testing.T, description, prompt string, background bool) json.RawMessage {
	t.Helper()
	return taskArgsWithComplexity(t, description, prompt, background, "")
}

// taskArgsWithComplexity additionally states the spawner's prediction, the way
// a classifying model writes the call.
func taskArgsWithComplexity(t *testing.T, description, prompt string, background bool, complexity string) json.RawMessage {
	t.Helper()
	args := map[string]any{
		"description": description,
		"prompt":      prompt,
		"background":  background,
	}
	if complexity != "" {
		args["complexity"] = complexity
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return raw
}

func taskArgsWithWorkload(t *testing.T, description, prompt string, background bool, workload string) json.RawMessage {
	t.Helper()
	args := map[string]any{
		"description": description,
		"prompt":      prompt,
		"background":  background,
	}
	if workload != "" {
		args["workload"] = workload
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return raw
}

// TestTaskBackgroundRouting pins the background mode of the task tool: the call
// returns a launch report at once instead of blocking on a sub-agent, and in an
// environment with no agent board it refuses with a hint rather than hanging
// the parent's turn.
func TestTaskBackgroundRouting(t *testing.T) {
	tctx := Context{SessionID: session.SessionID("ses_parent")}

	t.Run("refuses without a spawn hook", func(t *testing.T) {
		ran := 0
		kt := TaskTool{
			Run: func(context.Context, string, string, string, string, string, provider.Complexity, provider.Workload) (string, error) {
				ran++
				return "should not run", nil
			},
		}
		res, err := kt.Execute(context.Background(), taskTestArgs(t, "research", "task text", true), tctx)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if ran != 0 {
			t.Fatal("the synchronous sub-agent ran for a background call")
		}
		if !strings.Contains(res.Output, "no agent board") || !strings.Contains(res.Output, "background") {
			t.Errorf("refusal does not explain the missing board or the escape hatch: %q", res.Output)
		}
	})

	t.Run("starts a child and returns its handle", func(t *testing.T) {
		ran := 0
		var gotSID, gotDesc, gotPrompt string
		kt := TaskTool{
			Run: func(context.Context, string, string, string, string, string, provider.Complexity, provider.Workload) (string, error) {
				ran++
				return "", nil
			},
			Spawn: func(_ context.Context, sessionID, description, prompt string, _ provider.Complexity, _ provider.Workload) (BackgroundTaskResult, error) {
				gotSID, gotDesc, gotPrompt = sessionID, description, prompt
				return BackgroundTaskResult{
					Handle: "task-1a2b", SessionID: "ses_child",
					URL: "/session/ses_child", Notice: "Started background task task-1a2b (1 working).", Live: 1,
				}, nil
			},
		}
		res, err := kt.Execute(context.Background(), taskTestArgs(t, "trace auth middleware", "find every middleware", true), tctx)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if ran != 0 {
			t.Fatal("the synchronous sub-agent ran for a background call")
		}
		if gotSID != "ses_parent" || gotDesc != "trace auth middleware" || gotPrompt != "find every middleware" {
			t.Fatalf("spawn hook got (%q, %q, %q), want the caller's session, description and prompt", gotSID, gotDesc, gotPrompt)
		}
		got := res.Output
		if !strings.Contains(got, "Started background task task-1a2b") ||
			!strings.Contains(got, "/session/ses_child") ||
			!strings.Contains(got, "NOT in this result") ||
			!strings.Contains(got, "teammate wait task-1a2b") {
			t.Errorf("launch report is missing the delivery contract: %q", got)
		}
	})

	t.Run("a spawn failure surfaces as the result", func(t *testing.T) {
		kt := TaskTool{
			Spawn: func(context.Context, string, string, string, provider.Complexity, provider.Workload) (BackgroundTaskResult, error) {
				return BackgroundTaskResult{}, fmt.Errorf("board is closed")
			},
		}
		res, err := kt.Execute(context.Background(), taskTestArgs(t, "research", "task text", true), tctx)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !strings.Contains(res.Output, "Background task failed to start") || !strings.Contains(res.Output, "board is closed") {
			t.Errorf("a spawn failure did not surface the reason: %q", res.Output)
		}
	})

	t.Run("background false stays synchronous", func(t *testing.T) {
		spawned := false
		kt := TaskTool{
			Run: func(context.Context, string, string, string, string, string, provider.Complexity, provider.Workload) (string, error) {
				return "the answer", nil
			},
			Spawn: func(context.Context, string, string, string, provider.Complexity, provider.Workload) (BackgroundTaskResult, error) {
				spawned = true
				return BackgroundTaskResult{}, nil
			},
		}
		res, err := kt.Execute(context.Background(), taskTestArgs(t, "research", "task text", false), tctx)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if spawned {
			t.Error("the spawn hook ran for a synchronous call")
		}
		if res.Output != "the answer" {
			t.Errorf("synchronous result = %q, want the sub-agent's answer", res.Output)
		}
	})

	t.Run("the spawner's prediction reaches the child hook", func(t *testing.T) {
		var syncGot, bgGot provider.Complexity
		kt := TaskTool{
			Run: func(_ context.Context, _, _, _, _, _ string, complexity provider.Complexity, _ provider.Workload) (string, error) {
				syncGot = complexity
				return "ok", nil
			},
			Spawn: func(_ context.Context, _, _, _ string, complexity provider.Complexity, _ provider.Workload) (BackgroundTaskResult, error) {
				bgGot = complexity
				return BackgroundTaskResult{Handle: "task-1a2b"}, nil
			},
		}
		if _, err := kt.Execute(context.Background(), taskArgsWithComplexity(t, "lookup", "find the caller", false, "easy"), tctx); err != nil {
			t.Fatalf("execute sync: %v", err)
		}
		if syncGot != provider.ComplexityEasy {
			t.Errorf("synchronous child got complexity %q, want %q", syncGot, provider.ComplexityEasy)
		}
		if _, err := kt.Execute(context.Background(), taskArgsWithComplexity(t, "refactor", "move the store", true, "hard"), tctx); err != nil {
			t.Fatalf("execute background: %v", err)
		}
		if bgGot != provider.ComplexityHard {
			t.Errorf("background child got complexity %q, want %q", bgGot, provider.ComplexityHard)
		}
		// Omitted classification is allowed and forwards nothing.
		syncGot = provider.ComplexityHard
		if _, err := kt.Execute(context.Background(), taskTestArgs(t, "lookup", "find the caller", false), tctx); err != nil {
			t.Fatalf("execute unclassified: %v", err)
		}
		if syncGot != "" {
			t.Errorf("an unclassified call forwarded complexity %q, want none", syncGot)
		}
	})

	t.Run("a misspelled class is bounced instead of routed wrongly", func(t *testing.T) {
		ran := 0
		kt := TaskTool{
			Run: func(context.Context, string, string, string, string, string, provider.Complexity, provider.Workload) (string, error) {
				ran++
				return "ok", nil
			},
		}
		res, err := kt.Execute(context.Background(), taskArgsWithComplexity(t, "lookup", "find the caller", false, "trivial"), tctx)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if ran != 0 {
			t.Error("the sub-agent ran with an unusable complexity class")
		}
		if !strings.Contains(res.Output, "easy, medium or hard") {
			t.Errorf("the bounce does not name the three classes: %q", res.Output)
		}
	})

	t.Run("the spawner's workload reaches the child hook", func(t *testing.T) {
		var syncGot, bgGot provider.Workload
		kt := TaskTool{
			Run: func(_ context.Context, _, _, _, _, _ string, _ provider.Complexity, workload provider.Workload) (string, error) {
				syncGot = workload
				return "ok", nil
			},
			Spawn: func(_ context.Context, _, _, _ string, _ provider.Complexity, workload provider.Workload) (BackgroundTaskResult, error) {
				bgGot = workload
				return BackgroundTaskResult{Handle: "task-1a2b"}, nil
			},
		}
		if _, err := kt.Execute(context.Background(), taskArgsWithWorkload(t, "lookup", "find the caller", false, "read-heavy"), tctx); err != nil {
			t.Fatalf("execute sync: %v", err)
		}
		if syncGot != provider.WorkloadReadHeavy {
			t.Errorf("synchronous child got workload %q, want %q", syncGot, provider.WorkloadReadHeavy)
		}
		if _, err := kt.Execute(context.Background(), taskArgsWithWorkload(t, "refactor", "move the store", true, "write-heavy"), tctx); err != nil {
			t.Fatalf("execute background: %v", err)
		}
		if bgGot != provider.WorkloadWriteHeavy {
			t.Errorf("background child got workload %q, want %q", bgGot, provider.WorkloadWriteHeavy)
		}
		// Omitted workload is allowed and forwards nothing.
		syncGot = provider.WorkloadWriteHeavy
		if _, err := kt.Execute(context.Background(), taskTestArgs(t, "lookup", "find the caller", false), tctx); err != nil {
			t.Fatalf("execute unclassified: %v", err)
		}
		if syncGot != "" {
			t.Errorf("an unclassified call forwarded workload %q, want none", syncGot)
		}
	})

	t.Run("a misspelled workload is bounced instead of routed wrongly", func(t *testing.T) {
		ran := 0
		kt := TaskTool{
			Run: func(context.Context, string, string, string, string, string, provider.Complexity, provider.Workload) (string, error) {
				ran++
				return "ok", nil
			},
		}
		res, err := kt.Execute(context.Background(), taskArgsWithWorkload(t, "lookup", "find the caller", false, "read_only"), tctx)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if ran != 0 {
			t.Error("the sub-agent ran with an unusable workload class")
		}
		if !strings.Contains(res.Output, "read-heavy or write-heavy") {
			t.Errorf("the bounce does not name the two workloads: %q", res.Output)
		}
	})
}
