package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/codemap"
)

type ReadTool struct{}

// defaultReadLimit is the number of lines returned when the caller passes no
// limit. It bounds a single read so a large file doesn't flood the context;
// callers page through the rest with offset.
const defaultReadLimit = 2000

// rangedReadThreshold is the file length past which an unranged read returns the
// file's map instead of its contents.
//
// Below it a whole-file read is a few kilobytes and cheaper than the two calls
// it would take to narrow one down, so the interception would cost more than it
// saves. Above it, pulling the whole file is the behaviour file_map exists to
// replace.
const rangedReadThreshold = 200

func (ReadTool) ID() string { return "read" }
func (ReadTool) Description() string {
	return "Read file contents or list directory contents. To read one region of a file, pass start_line/end_line — 1-based, inclusive, and taking the ranges printed by file_map directly. IMPORTANT: reading a file longer than 200 lines without a range returns that file's map instead of its contents, so map first and read the range you need. If you truly need an entire long file, say so explicitly with start_line=1 and end_line set past its length. Use offset/limit to page through a file once you know its shape — a limit on its own is honoured only up to 200 lines, because it says how much to read but not which part."
}
func (ReadTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "File or directory path"},
			"start_line": {"type": "number", "description": "First line to read, 1-based and inclusive. Pass a range printed by file_map here as-is."},
			"end_line": {"type": "number", "description": "Last line to read, 1-based and inclusive. Requires start_line; defaults to start_line plus 2000 lines."},
			"offset": {"type": "number", "description": "Line offset to start reading from (0-based). For paging; ignored when start_line is set."},
			"limit": {"type": "number", "description": "Maximum number of lines to read (default 2000)"}
		},
		"required": ["path"]
	}`)
}

func (ReadTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var input struct {
		Path      string `json:"path"`
		Offset    int    `json:"offset"`
		Limit     int    `json:"limit"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return Result{}, fmt.Errorf("parse args: %w", err)
	}

	// Capture whether the caller asked for any window at all, before start_line
	// is folded into offset below — after that, start_line=1 and "no range" both
	// look like offset=0.
	//
	// A bare limit is sized, not placed: it says how much to take and never
	// which part, so it cannot stand in for a range on a file the caller has
	// not mapped. Honoured up to rangedReadThreshold — a window that small
	// costs no more context than a short file already does, and it keeps a
	// peek at a file's head working — but past that it is a whole-file read
	// wearing a different argument, and the map is what the caller needs
	// first. An offset is placed, so it stays a window on its own: paging
	// presupposes you already know the file's shape.
	rangeRequested := input.StartLine > 0 || input.EndLine > 0 || input.Offset > 0 ||
		(input.Limit > 0 && input.Limit <= rangedReadThreshold)

	// start_line/end_line are 1-based and inclusive: the same numbering the
	// file_map tool prints and that this tool's own output carries. offset is a
	// 0-based index, so translating a range by hand means subtracting one, and
	// getting that wrong returns a window shifted by a line rather than an
	// error — it quietly drops the declaration's first line and picks up a
	// stray one at the end. Accepting the 1-based form removes the step.
	if input.StartLine > 0 {
		input.Offset = input.StartLine - 1
		if input.EndLine >= input.StartLine {
			input.Limit = input.EndLine - input.StartLine + 1
		}
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

	if out, ok := mapInsteadOfWholeFile(path, totalLines, rangeRequested); ok {
		return out, nil
	}

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

// mapInsteadOfWholeFile intercepts a read that asks for a long file with no
// range and returns the file's structural map instead of its contents.
//
// Instructing the model to map before reading leaves it as a choice the model
// has to get right on every call, and one slip puts an entire file in context
// for the rest of the turn. Enforcing it here makes the workflow structural:
// the unranged read of a long file simply cannot succeed.
//
// It returns the map rather than an error because the map is exactly what the
// caller needs in order to ask again with a range — refusing would cost a round
// trip and teach nothing.
//
// Three cases fall through to a normal read. An explicit range, including
// offset/limit paging, is a deliberate request and is honoured, so
// read(path, start_line=1, end_line=N) stays the way to demand a whole file. A
// short file is cheaper read than mapped. And a file the mapper finds no
// declarations in — prose, data, an unsupported language whose heuristics
// caught nothing — has no map worth returning, so withholding its contents
// would strand the caller with nothing at all.
func mapInsteadOfWholeFile(path string, totalLines int, rangeRequested bool) (Result, bool) {
	if rangeRequested || totalLines <= rangedReadThreshold {
		return Result{}, false
	}

	fm, err := codemap.Outline(path)
	if err != nil || len(fm.Symbols) == 0 {
		return Result{}, false
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s is %d lines — too long to read in full. Its map is below.\n\n",
		filepath.Base(path), totalLines)
	b.WriteString(codemap.Render(fm))
	fmt.Fprintf(&b, "\nRead the part you need with read(path, start_line=N, end_line=M).\n"+
		"If you genuinely need the whole file, ask for it explicitly with start_line=1, end_line=%d.\n",
		totalLines)

	return Result{
		Title:     fmt.Sprintf("File Map / %s (%d symbols)", filepath.Base(path), len(fm.Symbols)),
		Output:    b.String(),
		Truncated: true,
	}, true
}
