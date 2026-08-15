package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// TaskFunc runs a read-only sub-agent session for a delegated investigation and
// returns its final written answer. Implemented by agent.LoopRunner.RunTaskSession
// and wired in from server.go/cli to avoid the tool→agent import cycle.
type TaskFunc func(ctx context.Context, description, prompt, dir, model string) (string, error)

// taskTimeout bounds the total time a delegated sub-agent may run so a
// misbehaving child can't burn tokens forever. Read-only investigations read and
// search a lot of files (and may deep_search the web), so this is generous.
const taskTimeout = 300 * time.Second

// TaskTool lets a coding/planning agent delegate a focused, self-contained
// investigation to an autonomous read-only sub-agent. The sub-agent explores the
// codebase (read/glob/grep/codebase_map) and, if needed, the web (deep_search),
// then returns a concise written answer as the tool result. It cannot edit files
// or run shell commands, and it cannot spawn further sub-agents (depth-1).
type TaskTool struct {
	Run TaskFunc
}

func (TaskTool) ID() string { return "task" }

func (TaskTool) Description() string {
	return "Delegate a focused, self-contained investigation to an autonomous read-only sub-agent, and get back a written answer. " +
		"The sub-agent explores the codebase (read, glob, grep, codebase map) and can research the web, working independently from a clean context. " +
		"Use it to offload or parallelize well-scoped questions — e.g. \"find every place feature X is wired up and summarize how it works\", \"research library Y's API and report the relevant calls\", \"trace where value Z originates\". " +
		"Give it a complete, standalone prompt: it does NOT see your conversation. It returns findings only — it cannot edit files or run shell commands. " +
		"Prefer it for read-heavy exploration that would otherwise fill your own context; do NOT use it for changes (make edits yourself)."
}

func (TaskTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"description": {
				"type": "string",
				"description": "A short (3-6 word) label for the investigation, e.g. \"trace auth middleware\""
			},
			"prompt": {
				"type": "string",
				"description": "The complete, standalone task for the sub-agent. It cannot see this conversation, so include all needed context: what to investigate, where to look, and exactly what to report back."
			}
		},
		"required": ["description", "prompt"]
	}`)
}

func (t TaskTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var input struct {
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return Result{}, fmt.Errorf("parse args: %w", err)
	}
	if input.Prompt == "" {
		return Result{Output: "prompt is required — give the sub-agent a complete, standalone task description"}, nil
	}
	if t.Run == nil {
		return Result{Output: "the task sub-agent is not available in this environment"}, nil
	}

	title := input.Description
	if title == "" {
		title = "task"
	}

	// Bound the sub-agent with its own timeout so it doesn't run unbounded under
	// the parent loop. Cancellation still propagates: taskCtx derives from the
	// parent tool-execution context, so a parent abort/guidance-cancel stops it.
	taskCtx, cancel := context.WithTimeout(ctx, taskTimeout)
	defer cancel()

	answer, err := t.Run(taskCtx, input.Description, input.Prompt, tctx.SessionDir, tctx.Model)
	if err != nil {
		return Result{Title: title, Output: fmt.Sprintf("Sub-agent error: %s", err)}, nil
	}
	return Result{
		Title:  title,
		Output: answer,
	}, nil
}
