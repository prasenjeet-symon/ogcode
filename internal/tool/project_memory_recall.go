package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/memory"
	"github.com/prasenjeet-symon/ogcode/internal/project"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
)

// ProjectMemoryRecallTool queries the agentic knowledge graph across every
// conversation ever held in this workspace, where memory_recall only sees the
// current session. Synthesis uses the session's selected model, resolved per
// call from the registry — same contract as MemoryRecallTool.
type ProjectMemoryRecallTool struct {
	Memory   *memory.Memory
	Registry *provider.Registry
}

func NewProjectMemoryRecallTool(mem *memory.Memory, registry *provider.Registry) ProjectMemoryRecallTool {
	return ProjectMemoryRecallTool{Memory: mem, Registry: registry}
}

func (t ProjectMemoryRecallTool) ID() string { return "project_memory_recall" }

func (t ProjectMemoryRecallTool) Description() string {
	return "Search the agentic memory graph across ALL past sessions in this project, not just the current conversation. Use it for questions about work done earlier in this codebase: why a decision was made, how something was implemented before, what was tried and rejected, when a convention was introduced. Results are attributed to the conversation and date they came from, and conflicting facts are resolved in favour of the most recent. Set scope to \"session\" to run the same dated, attributed search over the current conversation only."
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
			"since_days": {
				"type": "integer",
				"description": "Optional. Only consider facts recorded in the last N days. Omit to search the entire project history."
			},
			"topic": {
				"type": "string",
				"description": "Optional. Restrict the search to a single topic name, exactly as it appears in the project map of an earlier recall result."
			},
			"scope": {
				"type": "string",
				"enum": ["project", "session"],
				"description": "Optional, defaults to \"project\" (every past session in this workspace). Use \"session\" to search only the current conversation while still getting dated, recency-ranked results."
			}
		}
	}`)
}

func (t ProjectMemoryRecallTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	if t.Memory == nil || !t.Memory.Enabled() {
		return Result{Title: "Project Memory Recall", Output: "Agentic memory is not enabled."}, nil
	}

	var params struct {
		Question  string `json:"question"`
		SinceDays int    `json:"since_days"`
		Topic     string `json:"topic"`
		Scope     string `json:"scope"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return Result{}, err
	}
	if params.Question == "" {
		return Result{Title: "Project Memory Recall", Output: "No question provided."}, nil
	}

	projectID := project.Resolve(tctx.SessionDir)
	if projectID == "" {
		return Result{Title: "Project Memory Recall", Output: "No project directory resolved for this session."}, nil
	}

	var since int64
	if params.SinceDays > 0 {
		since = time.Now().AddDate(0, 0, -params.SinceDays).UnixMilli()
	}

	// Scope "session" reuses the whole project pipeline against one conversation,
	// so the caller still gets dates, attribution and recency ranking. The session
	// ID comes from the tool context, never from the model — an agent cannot point
	// this at some other conversation.
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

	slog.Info("project_memory_recall tool invoked",
		"question", params.Question, "project", projectID, "scope", scope,
		"sinceDays", params.SinceDays, "session", tctx.SessionID)

	// Synthesis runs on the session's own model, so recall inherits whatever the
	// user selected rather than a server-wide default. A model that cannot be
	// resolved falls back to the raw assembled context (no synthesis).
	var chat memory.ChatClient
	if t.Registry != nil && tctx.Model != "" {
		if p := t.Registry.ResolveProvider(tctx.Model); p != nil {
			chat = memory.NewChatClient(p, tctx.Model)
		}
	}

	recall := t.Memory.RecallProjectMemory(ctx, memory.ProjectRecallRequest{
		ProjectID: projectID,
		Question:  params.Question,
		Since:     since,
		TopicName: params.Topic,
		SessionID: onlySession,
		Chat:      chat,
	})

	title := "Project Memory Recall"
	if onlySession != "" {
		title = "Session Memory Recall"
	}
	if recall == "" {
		where := "this project's memory"
		if onlySession != "" {
			where = "this session's memory"
		}
		return Result{Title: title, Output: "No relevant past context found in " + where + "."}, nil
	}

	return Result{Title: title, Output: recall}, nil
}
