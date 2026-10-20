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

// readRange is one window of a multi-range read. Its field names match the
// single-window parameters, so a range printed by file_map copies across
// unchanged.
type readRange struct {
	StartLine int `json:"start_line"`
	EndLine   int `json:"end_line"`
}

// lineNumberSep divides the line number from the line's own text. It must not
// be a whitespace character: see the numbering loop below.
const lineNumberSep = "|"

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

// rangedReadTrailer is appended when a read leaves part of the file unread. It
// names the multi-window form at the moment a caller is deciding whether to
// read again — the point where one call per window is easiest to avoid.
const rangedReadTrailer = `Need more of this file? Ask for every window at once with ranges=[{"start_line":10,"end_line":20},{"start_line":80,"end_line":95}].`

func (ReadTool) ID() string { return "read" }
func (ReadTool) Description() string {
	return "Read file contents or list directory contents. Each line comes back as its line number, a '|', then the line exactly as it is in the file — so everything after the FIRST '|' is literal content, including its leading tabs or spaces, and that is what an edit anchor must copy. To read part of a file, pass ranges: a list of {start_line, end_line} objects, 1-based and inclusive, taking the ranges printed by file_map directly. One entry reads one region; several read every grep hit or file_map entry you need in one call, returned in the order given under a file:start-end heading with the same numbering. A single region is just ranges with one entry — there is no separate start_line/end_line form. ranges carries its own windows and cannot be combined with offset or limit. IMPORTANT: reading a file longer than 200 lines without a range returns that file's map instead of its contents, so map first and read the range you need. If you truly need an entire long file, say so explicitly with a single range covering it, e.g. ranges=[{\"start_line\":1,\"end_line\":1234}]. Use offset/limit to page through a file once you know its shape — a limit on its own is honoured only up to 200 lines, because it says how much to read but not which part."
}
func (ReadTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "File or directory path"},
			"ranges": {
				"type": "array",
				"description": "Line windows to read, each {\"start_line\": N, \"end_line\": M} (1-based, inclusive). One entry reads one region; several read every grep hit or file_map entry in one call. This is the only form for a range. Send it alone: it cannot be combined with offset or limit.",
				"items": {
					"type": "object",
					"properties": {
						"start_line": {"type": "integer", "description": "First line of this window, 1-based and inclusive. Defaults to 1 when only end_line is given."},
						"end_line": {"type": "integer", "description": "Last line of this window, 1-based and inclusive. If omitted, reads to the default window length."}
					}
				}
			},
			"offset": {"type": "integer", "description": "Line offset to start reading from (0-based). For paging; cannot be combined with ranges."},
			"limit": {"type": "integer", "description": "Maximum number of lines to read (default 2000)"}
		},
		"required": ["path"]
	}`)
}

func (ReadTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var input struct {
		Path   string      `json:"path"`
		Offset int         `json:"offset"`
		Limit  int         `json:"limit"`
		Ranges []readRange `json:"ranges"`
		// The top-level pair used to be a second spelling of a single window.
		// It is decoded only so a caller that still sends it is pointed at the
		// form that replaced it, rather than being ignored into a whole-file
		// read.
		StartLine *int `json:"start_line"`
		EndLine   *int `json:"end_line"`
	}
	if err := DecodeArgs(args, &input); err != nil {
		return Result{}, fmt.Errorf("parse args: %w", err)
	}

	// Windows have exactly one shape: ranges, a list of {start_line, end_line}
	// objects. The top-level pair used to be a second spelling of the same
	// idea, which left a caller two ways to say one thing and a wrong one
	// silently reading different lines. It is refused loudly rather than
	// ignored — an unseen start_line reads the file from the top and an unseen
	// end_line reads past the line the caller meant, both with nothing to say
	// so.
	if input.StartLine != nil || input.EndLine != nil {
		return Result{}, fmt.Errorf(`start_line/end_line are not accepted at the top level — pass each window as ranges: [{"start_line": N, "end_line": M}]; one entry reads one region, several read a fan-out`)
	}

	// A bare limit is sized, not placed: it says how much to take and never
	// which part, so it cannot stand in for a range on a file the caller has
	// not mapped. Honoured up to rangedReadThreshold — a window that small
	// costs no more context than a short file already does, and it keeps a
	// peek at a file's head working — but past that it is a whole-file read
	// wearing a different argument, and the map is what the caller needs
	// first. An offset is placed, so it stays a window on its own: paging
	// presupposes you already know the file's shape.
	//
	// ranges and offset/limit are two ways to place a window, so mixing them
	// has no single reading: the caller meant one of them, and guessing which
	// silently returns lines nobody asked for.
	if len(input.Ranges) > 0 && (input.Offset > 0 || input.Limit > 0) {
		return Result{}, fmt.Errorf("ranges cannot be combined with offset or limit — each range carries its own window; send ranges alone")
	}

	// A range is 1-based and inclusive: the same numbering the file_map tool
	// prints and that this tool's own output carries. offset is a 0-based
	// index, so translating a range by hand means subtracting one, and getting
	// that wrong returns a window shifted by a line rather than an error — it
	// quietly drops the declaration's first line and picks up a stray one at
	// the end. Accepting the 1-based form removes the step.
	//
	// An inverted range is always a caller slip — no amount of file drift
	// produces one. Name the offending entry so a long list is fixable without
	// bisecting it.
	for i, r := range input.Ranges {
		start := r.StartLine
		if start <= 0 {
			start = 1
		}
		if r.EndLine > 0 && r.EndLine < start {
			return Result{}, fmt.Errorf("ranges[%d]: end_line %d is before start_line %d — the range is inverted and reads nothing; send it with start_line ≤ end_line", i, r.EndLine, start)
		}
	}

	rangeRequested := len(input.Ranges) > 0 || input.Offset > 0 ||
		(input.Limit > 0 && input.Limit <= rangedReadThreshold)

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

	// A binary file has no line structure: splitting it on '\n' bytes that
	// occur incidentally in the data produces a flood of garbage "lines" with
	// fabricated line numbers instead of an error, and that garbage still
	// reaches the model. Catch it with the same heuristic file_map uses and
	// say so instead.
	if codemap.LooksBinary(data) {
		return Result{
			Title: filepath.Base(path),
			Output: fmt.Sprintf(
				"%s is a binary file — it has no line structure to read as text. "+
					"For an image use view_image; for a PDF use pdf_index/read_pdf_page; for a DOCX use docx_index/read_docx_page.",
				filepath.Base(path),
			),
		}, nil
	}

	// Split into lines without counting a phantom trailing line for a file
	// that ends with a newline (the common case): strings.Split appends an
	// empty final element for that trailing newline, which would otherwise
	// inflate the reported total by one and disagree with codemap's line
	// count for the same file.
	var lines []string
	if len(data) > 0 {
		lines = strings.Split(string(data), "\n")
		if data[len(data)-1] == '\n' {
			lines = lines[:len(lines)-1]
		}
	}
	totalLines := len(lines)

	// Empty output is ambiguous: it reads equally as "empty file", "window past
	// EOF" and "something went wrong", and a caller acting on the wrong reading
	// of it can reach for a destructive fix. Both empty-looking cases say what
	// they are instead of returning "".
	if totalLines == 0 {
		return Result{
			Title:  filepath.Base(path),
			Output: fmt.Sprintf("%s is an empty file (0 lines).", filepath.Base(path)),
		}, nil
	}

	if out, ok := mapInsteadOfWholeFile(path, totalLines, rangeRequested); ok {
		return out, nil
	}

	if len(input.Ranges) > 0 {
		return renderRanges(filepath.Base(path), lines, totalLines, input.Ranges), nil
	}

	start := input.Offset
	if start < 0 {
		start = 0
	}
	// A window that begins past EOF happens honestly — line numbers go stale
	// whenever an edit shrinks the file — but it used to come back as empty
	// output with nothing to say why. Name the file's real extent so the next
	// call can be ranged correctly.
	if start >= totalLines {
		return Result{
			Title: filepath.Base(path),
			Output: fmt.Sprintf("%s has only %d lines — the requested window starts at line %d, past the end of the file. Line numbers may have shifted since this file was last read; re-read with a range within 1-%d.",
				filepath.Base(path), totalLines, start+1, totalLines),
		}, nil
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
	numbered, shownEnd, _, truncatedByBytes := numberLines(lines, start, end, MaxToolOutputBytes)

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
	if truncated {
		output += "\n\n" + rangedReadTrailer
	}

	return Result{
		Title:     filepath.Base(path),
		Output:    output,
		Truncated: truncated,
	}, nil
}

// numberLines renders lines[start:end] with a "number|text" prefix, stopping
// before budget bytes are exceeded but always emitting at least one line.
// shownEnd is the exclusive index it reached and capped reports the early stop.
//
// The separator is deliberately not whitespace. This output is what a caller
// copies from to build an "edit" anchor, and a tab separator made that
// impossible to do reliably in tab-indented languages: a once-indented line
// of Go rendered as the number followed by TWO adjacent tabs, one belonging
// to the prefix and one to the code, with nothing to say where the boundary
// was. Strip one too many and the anchor is right while its indentation is
// wrong — which "edit" can only report as text that is not in the file.
// Deeper nesting made it worse, four tabs for three levels. A visible
// separator makes the rule exact: content begins at the character after it.
func numberLines(lines []string, start, end, budget int) (rendered []string, shownEnd, used int, capped bool) {
	i := start
	for ; i < end; i++ {
		ln, _ := truncateLongLine(lines[i])
		entry := fmt.Sprintf("%6d%s%s", i+1, lineNumberSep, ln)
		// Stop before exceeding the byte cap, but always emit at least one line.
		if used+len(entry) > budget && len(rendered) > 0 {
			capped = true
			break
		}
		rendered = append(rendered, entry)
		used += len(entry) + 1 // +1 for the joining newline
	}
	return rendered, i, used, capped
}

// renderRanges numbers several windows of one file into a single result.
//
// Each window opens with a file:start-end heading and keeps the same
// number|text line format as a single-window read, so any returned line still
// copies straight into an edit anchor. The byte cap spans the whole call: one
// window cannot spend the budget of the next, and a window that got no room is
// named instead of silently dropped.
func renderRanges(name string, lines []string, totalLines int, ranges []readRange) Result {
	var b strings.Builder
	remaining := MaxToolOutputBytes
	truncated := false
	for i, r := range ranges {
		if remaining <= 0 {
			fmt.Fprintf(&b, "\n(%d more range(s) not shown — the output cap was reached; ask for them in a follow-up call.)\n", len(ranges)-i)
			truncated = true
			break
		}
		start := r.StartLine
		if start <= 0 {
			start = 1
		}
		if i > 0 {
			b.WriteString("\n")
		}
		if start > totalLines {
			fmt.Fprintf(&b, "%s:%d: past the end of the file — %s has only %d lines, so line numbers may have shifted since it was last read.\n",
				name, start, name, totalLines)
			continue
		}
		// An omitted end_line gets the same bounded window a single-window read
		// defaults to, not the rest of the file: one sloppy range should not
		// pull a whole long file into the context.
		end := start + defaultReadLimit - 1
		if r.EndLine >= start {
			end = r.EndLine
		}
		if end > totalLines {
			end = totalLines
		}
		rendered, shownEnd, used, capped := numberLines(lines, start-1, end, remaining)
		remaining -= used
		fmt.Fprintf(&b, "%s:%d-%d\n%s\n", name, start, shownEnd, strings.Join(rendered, "\n"))
		if capped {
			fmt.Fprintf(&b, "(%s:%d-%d was cut at the output cap after line %d; ask for the rest as another range.)\n",
				name, start, end, shownEnd)
			truncated = true
		}
	}
	return Result{
		Title:     name,
		Output:    strings.TrimRight(b.String(), "\n"),
		Truncated: truncated,
	}
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
// read(path, ranges=[{"start_line":1,"end_line":N}]) stays the way to demand a
// whole file. A short file is cheaper read than mapped. And a file the mapper finds no
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
	fmt.Fprintf(&b, "\nRead the part you need with read(path, ranges=[{\"start_line\":N,\"end_line\":M}]), or every part together with read(path, ranges=[{\"start_line\":N,\"end_line\":M}, {\"start_line\":N2,\"end_line\":M2}]).\n"+
		"If you genuinely need the whole file, ask for it explicitly with a single range covering it: ranges=[{\"start_line\":1,\"end_line\":%d}].\n",
		totalLines)

	return Result{
		Title:     fmt.Sprintf("File Map / %s", filepath.Base(path)),
		Output:    b.String(),
		Truncated: true,
	}, true
}
