package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/memfile"
	"github.com/prasenjeet-symon/ogcode/internal/project"
)

// memoryMapBudget caps the rendered map so a project with a long history can't
// flood the recall agent's context. Past the cap, remaining files are listed by
// date+title without their outline — enough to know they exist and read them
// directly if needed.
const memoryMapBudget = 40 * 1024

// MemoryMapTool is the index over a project's per-turn markdown memory — the
// memory analogue of codebase_map. It lists the turn summaries (newest first)
// with each file's heading outline, so the read-only recall agent can pick the
// right file by date/title/section and then read only the lines it needs. Scope
// (project vs a single session) comes from the recall context, never from the
// model.
type MemoryMapTool struct {
	Store *memfile.Store
}

// NewMemoryMapTool constructs a MemoryMapTool over the project-local index.
func NewMemoryMapTool(store *memfile.Store) MemoryMapTool {
	return MemoryMapTool{Store: store}
}

func (t MemoryMapTool) ID() string { return "memory_map" }

func (t MemoryMapTool) Description() string {
	return "Index of this project's persistent memory: dated markdown summaries of past turns, newest first, each with its heading outline and line ranges. Call this FIRST when recalling past work — read the outline to find the relevant summary, then use file_map and read(path, start_line, end_line) to read only that section instead of whole files. Filenames and dates are chronological, so use them to reason about what is recent versus old."
}

func (t MemoryMapTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {}
	}`)
}

func (t MemoryMapTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	if t.Store == nil {
		return Result{Title: "Memory Map", Output: "Persistent turn memory is not available in this environment."}, nil
	}

	scope, haveScope := RecallScopeFromContext(ctx)
	projectID := scope.ProjectID
	if projectID == "" {
		projectID = project.Resolve(tctx.SessionDir)
	}

	var (
		entries []*memfile.Entry
		err     error
		label   string
	)
	if haveScope && scope.Scope == "session" && scope.SessionID != "" {
		entries, err = t.Store.ListBySession(scope.SessionID)
		label = "this conversation"
	} else {
		entries, err = t.Store.ListByProject(projectID)
		label = "this project"
	}
	if err != nil {
		return Result{Title: "Memory Map", Output: "Memory index lookup failed: " + err.Error()}, nil
	}
	if len(entries) == 0 {
		return Result{Title: "Memory Map", Output: "No past turns are recorded in " + label + "'s memory yet."}, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d turn summaries in %s, newest first.\n", len(entries), label)
	b.WriteString("Read a summary's section with: read(path, start_line=N, end_line=M). Map the file first with file_map if you need a finer outline.\n\n")

	truncated := false
	for i, e := range entries {
		block := renderMapEntry(e)
		if b.Len()+len(block) > memoryMapBudget && i > 0 {
			fmt.Fprintf(&b, "… %d older summaries not shown (map budget reached). Narrow by date or read a specific file directly.\n", len(entries)-i)
			truncated = true
			break
		}
		b.WriteString(block)
	}

	return Result{Title: "Memory Map", Output: strings.TrimRight(b.String(), "\n") + "\n", Truncated: truncated}, nil
}

// renderMapEntry renders one summary: its date and title, its path, and its
// heading outline indented beneath.
func renderMapEntry(e *memfile.Entry) string {
	var b strings.Builder
	title := e.Title
	if title == "" {
		title = "(untitled turn)"
	}
	fmt.Fprintf(&b, "[%s] %s\n", formatMillis(e.CreatedAt), title)
	fmt.Fprintf(&b, "  %s\n", e.Path)
	if strings.TrimSpace(e.Outline) != "" {
		for _, line := range strings.Split(e.Outline, "\n") {
			fmt.Fprintf(&b, "    %s\n", line)
		}
	}
	b.WriteString("\n")
	return b.String()
}

func formatMillis(ms int64) string {
	if ms == 0 {
		return "undated"
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04 UTC")
}
