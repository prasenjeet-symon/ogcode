package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/project"
)

// ProjectMemoryRecallTool answers a question from the project's persistent
// memory — every past conversation in this workspace — by delegating to the
// read-only recall sub-agent over the dated markdown turn summaries. With
// scope "session" it restricts the search to the current conversation.
type ProjectMemoryRecallTool struct {
	Recall  RecallFunc
	Barrier RecallBarrier
}

func NewProjectMemoryRecallTool(recall RecallFunc, barrier RecallBarrier) ProjectMemoryRecallTool {
	return ProjectMemoryRecallTool{Recall: recall, Barrier: barrier}
}

func (t ProjectMemoryRecallTool) ID() string { return "project_memory_recall" }

func (t ProjectMemoryRecallTool) Description() string {
	return "Search this project's persistent memory across ALL past sessions in the workspace, not just the current conversation. Use it for questions about work done earlier in this codebase: why a decision was made, how something was implemented before, what was tried and rejected, when a convention was introduced. A read-only sub-agent reads the dated turn summaries and returns a brief, synthesized answer, preferring the most recent when they disagree. Set scope to \"session\" to search only the current conversation."
}

func (t ProjectMemoryRecallTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["question"],
		"properties": {
			"question": {
				"type": "string",
				"description": "A clear, specific question to look up across the project's history."
			},
			"scope": {
				"type": "string",
				"enum": ["project", "session"],
				"description": "Optional, defaults to \"project\" (every past session in this workspace). Use \"session\" to search only the current conversation."
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

	// Scope defaults to the whole project. "session" restricts to the current
	// conversation; the session ID comes from the tool context, never the model.
	scope := strings.ToLower(strings.TrimSpace(params.Scope))
	var onlySession string
	switch scope {
	case "", "project":
		scope = "project"
	case "session":
		onlySession = string(tctx.SessionID)
	default:
		return Result{Title: "Project Memory Recall", Output: fmt.Sprintf("Unknown scope %q — use \"project\" or \"session\".", params.Scope)}, nil
	}

	// Wait for any in-flight summary write for this project, then delegate.
	if t.Barrier != nil {
		t.Barrier.Wait(projectID)
	}
	title := "Project Memory Recall"
	if onlySession != "" {
		title = "Session Memory Recall"
	}
	slog.Info("project_memory_recall delegating to recall agent",
		"question", params.Question, "project", projectID, "scope", scope, "session", tctx.SessionID)
	answer, err := t.Recall(ctx, params.Question, scope, onlySession, tctx.SessionDir, tctx.Model)
	if err != nil {
		return Result{Title: title, Output: "Memory recall failed: " + err.Error() + "\nThis is not the same as memory being empty — retry, or proceed without it."}, nil
	}
	if strings.TrimSpace(answer) == "" {
		where := "this project's memory"
		if onlySession != "" {
			where = "this session's memory"
		}
		return Result{Title: title, Output: "No relevant past context found in " + where + "."}, nil
	}
	return Result{Title: title, Output: answer}, nil
}
