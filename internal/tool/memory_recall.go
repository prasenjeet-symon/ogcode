package tool

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/prasenjeet-symon/ogcode/internal/memory"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
)

// MemoryRecallTool lets the LLM query the agentic knowledge graph on demand.
// Synthesis uses the session's currently selected model: the tool resolves the
// provider from the model ID via the registry and builds a per-call chat client.
type MemoryRecallTool struct {
	Memory   *memory.Memory
	Registry *provider.Registry
}

func NewMemoryRecallTool(mem *memory.Memory, registry *provider.Registry) MemoryRecallTool {
	return MemoryRecallTool{Memory: mem, Registry: registry}
}

func (t MemoryRecallTool) ID() string { return "memory_recall" }
func (t MemoryRecallTool) Description() string {
	return "Search the agentic memory graph for past facts, context, and prior reasoning relevant to a specific question. Use this when you need precise historical details (e.g., exact config values, file paths, decisions made earlier) that may be summarized too coarsely in <prior_context>."
}

func (t MemoryRecallTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["question"],
		"properties": {
			"question": {
				"type": "string",
				"description": "A clear, specific question to look up in the memory graph."
			}
		}
	}`)
}

func (t MemoryRecallTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	if t.Memory == nil || !t.Memory.Enabled() {
		return Result{Title: "Memory Recall", Output: "Agentic memory is not enabled."}, nil
	}

	var params struct {
		Question string `json:"question"`
	}
	if err := DecodeArgs(args, &params); err != nil {
		return Result{}, err
	}
	if params.Question == "" {
		return Result{Title: "Memory Recall", Output: "No question provided."}, nil
	}

	slog.Info("memory_recall tool invoked", "question", params.Question, "session", tctx.SessionID)

	// Build a synthesis client from the session's selected model so recall
	// uses the same LLM the user is chatting with. Falls back to nil (raw
	// tree, no synthesis) when the model can't be resolved.
	var chat memory.ChatClient
	if t.Registry != nil && tctx.Model != "" {
		if p := t.Registry.ResolveProvider(tctx.Model); p != nil {
			chat = memory.NewChatClient(p, tctx.Model)
		}
	}

	recall, err := t.Memory.RecallMemory(ctx, string(tctx.SessionID), params.Question, chat)
	if err != nil {
		// Distinct from an empty result on purpose: "nothing found" would tell
		// the model memory holds nothing on this subject, when in fact the
		// lookup failed and the answer may well be in there.
		return Result{Title: "Memory Recall", Output: "Memory lookup failed: " + err.Error() + "\nThis is not the same as memory being empty — retry, or proceed without it."}, nil
	}
	if recall == "" {
		return Result{Title: "Memory Recall", Output: "No relevant past context found in memory."}, nil
	}

	return Result{Title: "Memory Recall", Output: recall}, nil
}
