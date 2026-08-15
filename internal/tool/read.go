package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ReadTool struct{}

// defaultReadLimit is the number of lines returned when the caller passes no
// limit. It bounds a single read so a large file doesn't flood the context;
// callers page through the rest with offset.
const defaultReadLimit = 2000

func (ReadTool) ID() string { return "read" }
func (ReadTool) Description() string {
	return "Read file contents or list directory contents. Reads up to 2000 lines by default; use offset/limit to page through larger files."
}
func (ReadTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "File or directory path"},
			"offset": {"type": "number", "description": "Line offset to start reading from (0-based)"},
			"limit": {"type": "number", "description": "Maximum number of lines to read (default 2000)"}
		},
		"required": ["path"]
	}`)
}

func (ReadTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var input struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return Result{}, fmt.Errorf("parse args: %w", err)
	}

	path := input.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(tctx.SessionDir, path)
	}

	info, err := os.Stat(path)
	if err != nil {
		return Result{}, fmt.Errorf("stat %s: %w", path, err)
	}

	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return Result{}, fmt.Errorf("read dir: %w", err)
		}
		var lines []string
		for _, e := range entries {
			prefix := "  "
			if e.IsDir() {
				prefix = "D "
			}
			lines = append(lines, prefix+e.Name())
		}
		return Result{
			Title:  filepath.Base(path) + "/",
			Output: strings.Join(lines, "\n"),
		}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("read file: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	totalLines := len(lines)

	start := input.Offset
	if start < 0 {
		start = 0
	}
	if start > totalLines {
		start = totalLines
	}

	// Default to a bounded window when the caller gives no limit, so reading a
	// large file doesn't dump the whole thing into the model context (and re-send
	// it on every subsequent step of the turn). The model can page with offset.
	limit := input.Limit
	if limit <= 0 {
		limit = defaultReadLimit
	}
	end := start + limit
	if end > totalLines {
		end = totalLines
	}

	// Add line numbers, capping any single very long line and the total byte
	// size so one read can't blow the budget on its own.
	var numbered []string
	byteCount := 0
	truncatedByBytes := false
	i := start
	for ; i < end; i++ {
		ln := lines[i]
		if len(ln) > MaxLineLength {
			ln = ln[:MaxLineLength] + lineTruncatedSuffix
		}
		entry := fmt.Sprintf("%6d\t%s", i+1, ln)
		// Stop before exceeding the byte cap, but always emit at least one line.
		if byteCount+len(entry) > MaxToolOutputBytes && len(numbered) > 0 {
			truncatedByBytes = true
			break
		}
		numbered = append(numbered, entry)
		byteCount += len(entry) + 1 // +1 for the joining newline
	}
	shownEnd := i // exclusive index of the last line shown + 1

	output := strings.Join(numbered, "\n")
	truncated := false
	switch {
	case truncatedByBytes:
		output += fmt.Sprintf("\n\n(Output capped at %d KB. Showing lines %d-%d of %d. Use offset=%d to continue.)",
			MaxToolOutputBytes/1024, start+1, shownEnd, totalLines, shownEnd)
		truncated = true
	case shownEnd < totalLines:
		output += fmt.Sprintf("\n\n(Showing lines %d-%d of %d. Use offset=%d to continue.)",
			start+1, shownEnd, totalLines, shownEnd)
		truncated = true
	}

	return Result{
		Title:     filepath.Base(path),
		Output:    output,
		Truncated: truncated,
	}, nil
}