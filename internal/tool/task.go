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
type TaskFunc func(ctx context.Context, description, prompt, dir, model, provider string) (string, error)

// BackgroundTaskFunc starts the same shape of investigation as a persistent
// child on the agent board and returns at once, instead of blocking until the
// child finishes. The child's answer is delivered to the caller by the board's
// completion report — pushed into the caller's running turn when one is live,
// or handed over at its next turn start — and can be collected early with
// `teammate wait`. Implemented by server.spawnBackgroundTask; nil in
// environments with no board (CLI), where background mode is refused with a note.
type BackgroundTaskFunc func(ctx context.Context, sessionID, description, prompt string) (BackgroundTaskResult, error)

// BackgroundTaskResult is what starting a background task hands back: where the
// child runs and how the caller will hear about it.
type BackgroundTaskResult struct {
	Handle    string
	SessionID string
	URL       string
	Notice    string
	Live      int
}

// taskTimeout bounds the total time a delegated sub-agent may run so a
// misbehaving child can't burn tokens forever. 0 means no wall-clock bound —
// the sub-agent inherits the main agent's timeout behaviour (none), and
// cancellation still propagates via the parent tool-execution context.
const taskTimeout = 0 * time.Second

// TaskTool lets a coding/planning agent delegate a focused, self-contained
// investigation to an autonomous read-only sub-agent. The sub-agent explores the
// codebase (read/glob/grep/codebase_map) and, if needed, the web (deep_search),
// then returns a concise written answer as the tool result. It cannot edit files
// or run shell commands, and it cannot spawn further sub-agents (depth-1).
//
// Synchronous is the default and the old behaviour: the call blocks until the
// child answers. With background mode (Spawn wired) the child keeps running
// after this call returns and reports through the board's completion pipeline,
// so the caller's turn is not parked behind a minutes-long investigation.
type TaskTool struct {
	Run TaskFunc
	// Spawn starts background: true investigations. nil (CLI, tests, embedders
	// without a board) makes background mode refuse with a note; sync runs keep
	// working unchanged.
	Spawn BackgroundTaskFunc
}

func (TaskTool) ID() string { return "task" }

func (TaskTool) Description() string {
	return "Delegate a focused, self-contained investigation to an autonomous read-only sub-agent, and get back a written answer. " +
		"The sub-agent explores the codebase (read, glob, grep, codebase map) and can research the web, working independently from a clean context. " +
		"Use it to offload or parallelize well-scoped questions — e.g. \"find every place feature X is wired up and summarize how it works\", \"research library Y's API and report the relevant calls\", \"trace where value Z originates\". " +
		"Give it a complete, standalone prompt: it does NOT see your conversation. It returns findings only — it cannot edit files or run shell commands. " +
		"Prefer it for read-heavy exploration that would otherwise fill your own context; do NOT use it for changes (make edits yourself). " +
		"By default the call is SYNCHRONOUS: it blocks your turn until the child's whole investigation is done, so batch it only with work that can wait that long. " +
		"Add \"background\": true instead when the investigation is long-running and does not gate your next step: the call returns a board handle at once and you keep working; " +
		"the child's answer is delivered as a completion report when it finishes (or collect it up front by waiting with the teammate tool), and if your turn ends first it waits for your next one."
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
			},
			"background": {
				"type": "boolean",
				"description": "Run it as a background child instead of blocking: this call returns its handle at once, you keep working, and the child's answer arrives later as a completion report. Default false (synchronous, blocking)."
			}
		},
		"required": ["description", "prompt"]
	}`)
}

func (t TaskTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var input struct {
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
		Background  bool   `json:"background"`
	}
	if err := DecodeArgs(args, &input); err != nil {
		return Result{}, fmt.Errorf("parse args: %w", err)
	}
	if input.Prompt == "" {
		return Result{Output: "prompt is required — give the sub-agent a complete, standalone task description"}, nil
	}

	title := input.Description
	if title == "" {
		title = "task"
	}

	if input.Background {
		return t.executeBackground(ctx, title, input.Prompt, string(tctx.SessionID))
	}

	if t.Run == nil {
		return Result{Output: "the task sub-agent is not available in this environment"}, nil
	}

	// Bound the sub-agent with its own timeout so it doesn't run unbounded under
	// the parent loop. Cancellation still propagates: taskCtx derives from the
	// parent tool-execution context, so a parent abort/guidance-cancel stops it.
	// A zero taskTimeout disables the wall-clock bound (matching the main agent)
	// while still inheriting parent cancellation through ctx.
	var taskCtx context.Context = ctx
	var cancel context.CancelFunc
	if taskTimeout > 0 {
		taskCtx, cancel = context.WithTimeout(ctx, taskTimeout)
	} else {
		taskCtx, cancel = ctx, context.CancelFunc(func() {})
	}
	defer cancel()

	answer, err := t.Run(taskCtx, input.Description, input.Prompt, tctx.SessionDir, tctx.Model, tctx.Provider)
	if err != nil {
		return Result{Title: title, Output: fmt.Sprintf("Sub-agent error: %s", err)}, nil
	}
	return Result{
		Title:  title,
		Output: answer,
	}, nil
}

// executeBackground starts the investigation as a board child and answers at
// once: the tool result is the launch report, not the child's answer — the
// answer arrives later through the board's completion report.
func (t TaskTool) executeBackground(ctx context.Context, title, prompt, sessionID string) (Result, error) {
	if t.Spawn == nil {
		return Result{Title: title, Output: "Background tasks are not available in this environment (no agent board). Run it synchronously by omitting \"background\"."}, nil
	}
	res, err := t.Spawn(ctx, sessionID, title, prompt)
	if err != nil {
		return Result{Title: title, Output: fmt.Sprintf("Background task failed to start: %s", err)}, nil
	}
	output := res.Notice
	if res.URL != "" {
		output += "\n\nTrack it on the board (board roster) or open " + res.URL + "."
	}
	output += fmt.Sprintf("\n\nThe answer is NOT in this result. Keep working in your own turn — the child's report is pushed to you when it finishes (or handed over at your next turn start, since you cannot ask for it mid-task); collect it early by blocking on `teammate wait %s` instead of doing nothing meanwhile.", res.Handle)
	return Result{Title: title, Output: output}, nil
}