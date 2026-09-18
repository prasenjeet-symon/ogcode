package tool

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/project"
)

// ProjectMemoryRecallTool answers a question from the project's persistent
// memory — every past conversation in this workspace — by delegating to the
// read-only recall sub-agent over the dated markdown turn summaries. The
// per-conversation route is MemoryRecallTool; this one is always project-wide.
type ProjectMemoryRecallTool struct {
	Recall  RecallFunc
	Barrier RecallBarrier
}

func NewProjectMemoryRecallTool(recall RecallFunc, barrier RecallBarrier) ProjectMemoryRecallTool {
	return ProjectMemoryRecallTool{Recall: recall, Barrier: barrier}
}

func (t ProjectMemoryRecallTool) ID() string { return "project_memory_recall" }

func (t ProjectMemoryRecallTool) Description() string {
	return "Search this project's persistent memory across ALL past sessions in the workspace, not just the current conversation. Use it for questions about work done earlier in this codebase: why a decision was made, how something was implemented before, what was tried and rejected, when a convention was introduced. A read-only sub-agent reads the dated turn summaries and returns a brief, synthesized answer, preferring the most recent when they disagree. For the current conversation only, use memory_recall instead."
}

func (t ProjectMemoryRecallTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["question"],
		"properties": {
			"question": {
				"type": "string",
				"description": "A clear, specific question to look up across the project's history."
			}
		}
	}`)
}

func (t ProjectMemoryRecallTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var params struct {
		Question string `json:"question"`
		Scope    string `json:"scope"`
	}
	if err := DecodeArgs(args, &params); err != nil {
		return Result{}, err
	}
	if params.Question == "" {
		return Result{Title: "Project Memory Recall", Output: "No question provided."}, nil
	}
	if t.Recall == nil {
		return Result{Title: "Project Memory Recall", Output: "Memory is not enabled."}, nil
	}

	projectID := project.Resolve(tctx.SessionDir)
	if projectID == "" {
		return Result{Title: "Project Memory Recall", Output: "No project directory resolved for this session."}, nil
	}

	// The tool has no scope parameter: it always searches the whole project. A
	// "scope" argument the model still sends (older habit) errors with guidance
	// rather than being honored — per-conversation recall is memory_recall's
	// job. One consistent rule retrains the habit; the retry succeeds.
	if strings.TrimSpace(params.Scope) != "" {
		return Result{Title: "Project Memory Recall", Output: "project_memory_recall has no scope parameter — it always searches the whole project. Omit \"scope\"; for the current conversation use memory_recall."}, nil
	}

	// Wait for any in-flight summary write for this project, then delegate.
	if t.Barrier != nil {
		t.Barrier.Wait(projectID)
	}
	title := "Project Memory Recall"
	slog.Info("project_memory_recall delegating to recall agent",
		"question", params.Question, "project", projectID, "scope", "project", "session", tctx.SessionID)
	answer, err := t.Recall(ctx, params.Question, "project", "", tctx.SessionDir, tctx.Model)
	if err != nil {
		return Result{Title: title, Output: "Memory recall failed: " + err.Error() + "\nThis is not the same as memory being empty — retry, or proceed without it."}, nil
	}
	if strings.TrimSpace(answer) == "" {
		return Result{Title: title, Output: "No relevant past context found in this project's memory."}, nil
	}
	return Result{Title: title, Output: answer}, nil
}
