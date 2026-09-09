package tool

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/project"
)

// MemoryRecallTool answers a question from the CURRENT session's persistent
// memory by delegating to the read-only recall sub-agent, which reads this
// session's dated markdown turn summaries. Barrier makes it wait for any
// in-flight background summary write to land before the lookup runs.
type MemoryRecallTool struct {
	Recall  RecallFunc
	Barrier RecallBarrier
}

func NewMemoryRecallTool(recall RecallFunc, barrier RecallBarrier) MemoryRecallTool {
	return MemoryRecallTool{Recall: recall, Barrier: barrier}
}

func (t MemoryRecallTool) ID() string { return "memory_recall" }
func (t MemoryRecallTool) Description() string {
	return "Recall facts, decisions, or details from earlier in THIS conversation. A read-only sub-agent searches this session's saved turn summaries and returns a brief, synthesized answer. Use it whenever the request refers to earlier work in this session that is no longer in view — do not guess."
}

func (t MemoryRecallTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["question"],
		"properties": {
			"question": {
				"type": "string",
				"description": "A clear, specific question to look up in this session's memory."
			}
		}
	}`)
}

func (t MemoryRecallTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var params struct {
		Question string `json:"question"`
	}
	if err := DecodeArgs(args, &params); err != nil {
		return Result{}, err
	}
	if params.Question == "" {
		return Result{Title: "Memory Recall", Output: "No question provided."}, nil
	}
	if t.Recall == nil {
		return Result{Title: "Memory Recall", Output: "Memory is not enabled."}, nil
	}

	// Wait for any in-flight summary write for this project so the lookup sees a
	// settled index, then delegate to the read-only recall sub-agent.
	if t.Barrier != nil {
		t.Barrier.Wait(project.Resolve(tctx.SessionDir))
	}
	slog.Info("memory_recall delegating to recall agent", "question", params.Question, "session", tctx.SessionID)
	answer, err := t.Recall(ctx, params.Question, "session", string(tctx.SessionID), tctx.SessionDir, tctx.Model)
	if err != nil {
		return Result{Title: "Memory Recall", Output: "Memory recall failed: " + err.Error() + "\nThis is not the same as memory being empty — retry, or proceed without it."}, nil
	}
	if strings.TrimSpace(answer) == "" {
		return Result{Title: "Memory Recall", Output: "No relevant past context found in this session's memory."}, nil
	}
	return Result{Title: "Memory Recall", Output: answer}, nil
}
