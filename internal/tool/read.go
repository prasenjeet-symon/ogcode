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
// unchanged. EndLine is a pointer so an explicitly given value stays distinct
// from an omitted one — an explicit end_line must name a real line, while an
// omitted one runs to the end of the file.
type readRange struct {
	StartLine int  `json:"start_line"`
	EndLine   *int `json:"end_line"`
}

// readRequest is one file's read with the file already resolved away: the
// single-file form and one files entry carry exactly the same window fields.
type readRequest struct {
	Offset int
	Limit  int
	Ranges []readRange
}

// readEntry is one element of the files array.
type readEntry struct {
	Path   string      `json:"path"`
	Offset int         `json:"offset"`
	Limit  int         `json:"limit"`
	Ranges []readRange `json:"ranges"`
	// A window pair at the entry level is the shape a caller reaches for after
	// reading the single-file form. It is decoded only to be refused: left
	// alone, json.Unmarshal would ignore it and the entry would read the whole
	// file with no sign the window was dropped.
	StartLine *int `json:"start_line"`
	EndLine   *int `json:"end_line"`
}

// readBookmark is the revision a content read saw, kept until the bytes are
// actually part of the result. A map, a directory listing, a binary notice or
// an error has none: none of them put the file's bytes in the caller's context.
type readBookmark struct {
	key  string
	seen int64
}

// readOutcome is one file's contribution to a read result: title names the
// file in the result, body is the content the caller sees, truncated says
// whether the content was cut, and stamp is the revision a content read saw,
// recorded once the bytes are actually part of the result.
type readOutcome struct {
	title     string
	body      string
	truncated bool
	stamp     *readBookmark
}

// lineNumberSep divides the line number from the line's own text. It must not
// be a whitespace character: see the numbering loop below.
const lineNumberSep = "|"

// rangedReadThreshold is the file length past which an unranged read returns the
// file's map instead of its contents.
//
// Below it a whole-file read is a few kilobytes and cheaper than the two calls
// it would take to narrow one down, so the interception would cost more than it
// saves. Above it, pulling the whole file is the behaviour file_map exists to
// replace. It sizes the bare whole-file case only: any explicit window — a
// range, a positive offset, or a limit of any size — reads its window whatever
// the file's length, so the threshold never qualifies a limit.
const rangedReadThreshold = 200

// rangedReadTrailer is appended when a read leaves part of the file unread. It
// names the multi-window form at the moment a caller is deciding whether to
// read again — the point where one call per window is easiest to avoid.
const rangedReadTrailer = `Need more of this file? Ask for every window at once with ranges=[{"start_line":10,"end_line":20},{"start_line":80,"end_line":95}].`

func (ReadTool) ID() string { return "read" }
func (ReadTool) Description() string {
	return "Read file contents or list directory contents. One file reads with path, optionally with ranges/offset/limit. Two or more files read together in ONE call with files=[{\"path\": ..., \"ranges\": [...]}, ...] — the shape a grep or file_map fan-out leaves you with, and never one call per file. Each line comes back as its line number, a '|', then the line exactly as it is in the file — so everything after the FIRST '|' is literal content, including its leading tabs or spaces, and that is what an edit anchor must copy. To read part of a file, pass ranges: a list of {start_line, end_line} objects, 1-based and inclusive, taking the ranges printed by file_map or the line numbers grep printed directly. One entry reads one region; several read every grep hit or file_map entry you need in one call, returned in the order given under a file:start-end heading with the same numbering. A single region is just ranges with one entry — there is no separate start_line/end_line form. ranges carries its own windows and cannot be combined with offset or limit. Each files entry takes the single-file form — {path, ranges} or {path} alone for a whole file or a directory listing — and every file comes back in the order given under a '== path ==' heading. path and files are exclusive, and a files entry carries its own windows instead of a top-level ranges/offset/limit. A file that fails inside files does not discard the others — its section names the error. IMPORTANT: reading a file longer than 200 lines with no window at all — no ranges, no offset, no limit — returns that file's map instead of its contents, so map first and read the range you need. If you truly need an entire long file, say so explicitly with a single range covering it, e.g. ranges=[{\"start_line\":1,\"end_line\":1234}]. Use offset/limit to page through a file once you know its shape: a bare offset is a placed window that pages raw content past the map, and a bare limit is honoured on its own at any size. The interception needs a file the mapper recognizes — long prose, data or config carries no symbols, so a bare whole-file read of one falls through to a plain read with no map and no notice."
}
func (ReadTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "File or directory path. One file: for several files in one call use files instead."},
			"files": {
				"type": "array",
				"minItems": 1,
				"description": "Several files in one call, read in the order given. Each entry is one {path, ranges} (or {path} alone for the whole file or a directory listing) and accepts the same fields as the single-file form. Use after a grep or file_map fan-out so one call returns every window. Cannot be combined with path or with a top-level ranges/offset/limit.",
				"items": {
					"type": "object",
					"properties": {
						"path": {"type": "string", "description": "File or directory path for this entry"},
						"ranges": {
							"type": "array",
							"description": "Line windows within this file, each {\"start_line\": N, \"end_line\": M} (1-based, inclusive). Same rules as the top-level ranges.",
							"items": {
								"type": "object",
								"properties": {
									"start_line": {"type": "integer", "description": "First line of this window, 1-based and inclusive. Defaults to 1 when only end_line is given."},
									"end_line": {"type": "integer", "description": "Last line of this window, 1-based and inclusive. If omitted, reads to the end of the file."}
								}
							}
						},
						"offset": {"type": "integer", "description": "Line offset to start reading from (0-based). For paging this file; cannot be combined with this entry's ranges."},
						"limit": {"type": "integer", "description": "Maximum number of lines to read from this file (default: every line)"}
					},
					"required": ["path"]
				}
			},
			"ranges": {
				"type": "array",
				"description": "Line windows to read, each {\"start_line\": N, \"end_line\": M} (1-based, inclusive). One entry reads one region; several read every grep hit or file_map entry you need in one call. This is the only form for a range. Send it alone: it cannot be combined with offset or limit.",
				"items": {
					"type": "object",
					"properties": {
						"start_line": {"type": "integer", "description": "First line of this window, 1-based and inclusive. Defaults to 1 when only end_line is given."},
						"end_line": {"type": "integer", "description": "Last line of this window, 1-based and inclusive. If omitted, reads to the end of the file."}
					}
				}
			},
			"offset": {"type": "integer", "description": "Line offset to start reading from (0-based). For paging; cannot be combined with ranges."},
			"limit": {"type": "integer", "description": "Maximum number of lines to read (default: every line)"}
		}
	}`)
}

func (ReadTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var input struct {
		Path   string      `json:"path"`
		Offset int         `json:"offset"`
		Limit  int         `json:"limit"`
		Ranges []readRange `json:"ranges"`
		// Files is a pointer so an explicitly empty array is distinct from an
		// absent one: "files": [] deserves a message naming it rather than the
		// generic "pass one of the two".
		Files *[]readEntry `json:"files"`
		// The top-level pair used to be a second spelling of a single window.
		// It is decoded only so a caller that still sends it is pointed at the
		// form that replaced it, rather than being ignored into a whole-file
		// read. A files entry carries the same guard below.
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

	// Trim once: validation and resolution below both see the same cleaned
	// path, so a stray space never turns resolution into a confusing
	// "stat  main.go" error instead of the file itself.
	input.Path = strings.TrimSpace(input.Path)
	hasPath := input.Path != ""
	hasFiles := input.Files != nil
	if hasPath && hasFiles {
		return Result{}, fmt.Errorf(`path and files are two ways to name what to read — pass path for one file, or files=[{"path": "...", "ranges": [...]}] for several, not both`)
	}
	if !hasPath && !hasFiles {
		return Result{}, fmt.Errorf(`pass path for one file, or files=[{"path": "...", "ranges": [...]}] for several files in one call — one of the two is required`)
	}

	if hasFiles {
		files := *input.Files
		if len(files) == 0 {
			return Result{}, fmt.Errorf(`files is empty — pass at least one {"path": "..."} entry, or use path for a single read`)
		}
		// A top-level window has no file to apply to once the files array names
		// them all. The entry is where a window belongs, and guessing which
		// file an outer one meant would silently read the wrong lines.
		if len(input.Ranges) > 0 || input.Offset != 0 || input.Limit != 0 {
			return Result{}, fmt.Errorf(`files entries carry their own ranges/offset/limit — a top-level window has no file to apply to; move it into the entry that needs it`)
		}
		for i, e := range files {
			prefix := fmt.Sprintf("files[%d]: ", i)
			// The same one-trim rule as the single-file path: this is the value
			// readFiles resolves, so the trimmed copy is the one checked and
			// carried onward.
			files[i].Path = strings.TrimSpace(e.Path)
			if files[i].Path == "" {
				return Result{}, fmt.Errorf("%spath is empty — every entry names the file it reads", prefix)
			}
			if e.StartLine != nil || e.EndLine != nil {
				return Result{}, fmt.Errorf(`%sstart_line/end_line are not accepted on an entry — pass the window as ranges: [{"start_line": N, "end_line": M}]; one entry reads one region, several read a fan-out`, prefix)
			}
			if err := validateReadWindows(e.Ranges, e.Offset, e.Limit, prefix); err != nil {
				return Result{}, err
			}
		}
		// Every argument error has been raised before this point, so a
		// malformed entry can never leave the earlier files half-read.
		return readFiles(ctx, files, tctx), nil
	}

	if err := validateReadWindows(input.Ranges, input.Offset, input.Limit, ""); err != nil {
		return Result{}, err
	}

	resolved := resolveReadPath(tctx.SessionDir, input.Path)
	out, err := readOne(resolved, filepath.Base(resolved), readRequest{
		Offset: input.Offset,
		Limit:  input.Limit,
		Ranges: input.Ranges,
	}, tctx)
	if err != nil {
		return Result{}, err
	}
	if out.stamp != nil {
		tctx.fileLog().Read(ctx, out.stamp.key, tctx.SessionID, out.stamp.seen)
	}
	return Result{
		Title:     out.title,
		Output:    out.body,
		Truncated: out.truncated,
		Uncapped:  true,
	}, nil
}

// validateReadWindows applies the one-shape rule for one file's window. ranges
// and offset/limit are two ways to place a window, so mixing them has no
// single reading: the caller meant one of them, and guessing which silently
// returns lines nobody asked for.
//
// prefix names the entry in a files call ("files[1]: ") and is empty for the
// single-file form, whose messages predate the array.
func validateReadWindows(ranges []readRange, offset, limit int, prefix string) error {
	// A negative offset or limit is a caller slip, not a window: offset counts
	// lines from the top of the file (0-based) and limit caps how many are
	// returned, so neither has a negative reading. Silently clamping or
	// ignoring one would run the call as a different read than the caller
	// asked for, with nothing to say so.
	if offset < 0 {
		return fmt.Errorf("%soffset %d is negative — offset is a 0-based line index counted from the top of the file; omit it (or pass 0) to start at the first line", prefix, offset)
	}
	if limit < 0 {
		return fmt.Errorf("%slimit %d is negative — limit caps how many lines are returned; omit it (or pass 0) to read every line", prefix, limit)
	}
	if len(ranges) > 0 && (offset > 0 || limit > 0) {
		return fmt.Errorf("%sranges cannot be combined with offset or limit — each range carries its own window; send ranges alone", prefix)
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
	for i, r := range ranges {
		start := r.StartLine
		if start <= 0 {
			start = 1
		}
		// An explicitly given end_line must name a real line: a value <= 0
		// once slipped past the inverted check below and read as "to the end
		// of the file" — a different read than the caller asked for. An
		// omitted one (nil) still runs to the end.
		if r.EndLine != nil && *r.EndLine <= 0 {
			return fmt.Errorf("%sranges[%d]: end_line %d is not a line number — end_line is 1-based and inclusive; omit it to read to the end of the file", prefix, i, *r.EndLine)
		}
		if r.EndLine != nil && *r.EndLine < start {
			return fmt.Errorf("%sranges[%d]: end_line %d is before start_line %d — the range is inverted and reads nothing; send it with start_line ≤ end_line", prefix, i, *r.EndLine, start)
		}
	}
	return nil
}

// resolveReadPath makes a caller's path absolute against the session directory,
// the way every file tool resolves one.
func resolveReadPath(dir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(dir, path)
}

// displayReadPath names a file in a multi-file section. Base names collide (two
// util.go in one project), so the section uses the path relative to the session
// directory when the file is inside it, and the absolute path otherwise.
func displayReadPath(dir, resolved string) string {
	if rel, err := filepath.Rel(dir, resolved); err == nil {
		if rel == "." {
			return filepath.Base(resolved)
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(resolved)
}

// readFiles answers a files=[...] call: one section per entry, in the order
// given. A file that fails becomes a section naming the error rather than
// failing the call, so one stale path cannot discard the content the caller
// asked for. Nothing here bounds the total: the caller named the files and gets
// them whole.
//
// Only entries whose bytes were returned stamp a bookmark. A map, a listing, a
// binary notice and an error are not content: their sections leave the read
// bookmark exactly where it was, so a later stale edit is still refused rather
// than certified by a read that never saw the bytes.
func readFiles(ctx context.Context, entries []readEntry, tctx Context) Result {
	var b strings.Builder
	truncated := false
	for i, e := range entries {
		resolved := resolveReadPath(tctx.SessionDir, e.Path)
		display := displayReadPath(tctx.SessionDir, resolved)
		header := fmt.Sprintf("== %s ==\n", display)
		out, err := readOne(resolved, display, readRequest{
			Offset: e.Offset,
			Limit:  e.Limit,
			Ranges: e.Ranges,
		}, tctx)
		if err != nil {
			out.body = err.Error()
			out.truncated = false
			out.stamp = nil
		}
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(header)
		b.WriteString(out.body)
		// A section with no body returned none of the file's bytes, so it
		// must not certify a revision the caller never saw.
		if out.stamp != nil && out.body != "" {
			tctx.fileLog().Read(ctx, out.stamp.key, tctx.SessionID, out.stamp.seen)
		}
		truncated = truncated || out.truncated
	}

	title := fmt.Sprintf("%d files", len(entries))
	if len(entries) == 1 {
		title = filepath.Base(entries[0].Path)
	}
	return Result{
		Title:     title,
		Output:    strings.TrimRight(b.String(), "\n"),
		Truncated: truncated,
		Uncapped:  true,
	}
}

// readOne answers one file. display is how the file is named in headings and
// messages: the base name for the single-file form, a path relative to the
// session directory inside a multi-file call.
func readOne(path, display string, req readRequest, tctx Context) (readOutcome, error) {
	out := readOutcome{title: filepath.Base(path)}

	info, err := os.Stat(path)
	if err != nil {
		// os.Stat's error already carries the path; wrapping it in another
		// "stat <path>" only repeats it in the section a files call returns.
		return out, err
	}

	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return out, fmt.Errorf("read dir: %w", err)
		}
		var lines []string
		for _, e := range entries {
			prefix := "  "
			if e.IsDir() {
				prefix = "D "
			}
			lines = append(lines, prefix+e.Name())
		}
		out.title = filepath.Base(path) + "/"
		out.body = strings.Join(lines, "\n")
		return out, nil
	}

	// Capture the revision this read is about to see BEFORE the bytes are read.
	// The order is deliberate and conservative: if a mutation lands between
	// the capture and the read, the bookmark ends up older than the content,
	// so the next edit may refuse and show a change this reader already has.
	// The opposite order could certify a bookmark newer than the bytes that
	// were actually returned — a missed conflict, which is the one failure
	// this gate must not have.
	log, key := tctx.fileLog(), tctx.fileKey(path)
	seen := log.Rev(key)

	data, err := os.ReadFile(path)
	if err != nil {
		return out, fmt.Errorf("read file: %w", err)
	}

	// A binary file has no line structure: splitting it on '\n' bytes that
	// occur incidentally in the data produces a flood of garbage "lines" with
	// fabricated line numbers instead of an error, and that garbage still
	// reaches the model. Catch it with the same heuristic file_map uses and
	// say so instead.
	if codemap.LooksBinary(data) {
		out.body = fmt.Sprintf(
			"%s is a binary file — it has no line structure to read as text. "+
				"For an image use view_image; for a PDF use pdf_index/read_pdf_page; for a DOCX use docx_index/read_docx_page.",
			display,
		)
		return out, nil
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

	// The map-vs-content branch runs before the bookmark below, and with the
	// exact predicate the branch itself applies: no window asked for, more
	// lines than the map threshold, and declarations the mapper finds. A read
	// that will answer with the map has shown the file's structure and none of
	// its bytes, so it must not be recorded here — a bookmark stamped at this
	// point would certify content that was never returned, and a change another
	// session made since the last real read would be blessed through the next
	// stale edit instead of refused by it. The branch is reused as is: the
	// stamp decides against what the bytes will actually be, with no second
	// spelling of the predicate to drift from.
	//
	// Any explicit window is honoured whatever its size: ranges name their
	// lines, a positive offset names a placed window, and a bare limit names
	// one too — it reads exactly the lines it counts, past the map or not.
	rangeRequested := len(req.Ranges) > 0 || req.Offset > 0 || req.Limit > 0
	if body, ok := mapInsteadOfWholeFile(path, display, totalLines, rangeRequested); ok {
		out.title = fmt.Sprintf("File Map / %s", filepath.Base(path))
		// The map answers with structure, not bytes, and comes back whole. The
		// result says so (Uncapped) so the loop's backstop leaves it alone: the
		// outline is the answer, and a clipped outline would silently hide the
		// declarations the caller is looking for.
		out.body = body
		return out, nil
	}

	// The file is now in the caller's context, so record it: from here on an
	// edit this session makes against an older revision is refused rather than
	// applied over whatever changed in between. Only a regular text file gets
	// here — a directory listing is not the file's content, a binary file was
	// answered without one, and a map-only read above returned without any of
	// the bytes the bookmark would claim were seen. The stamp is applied by the
	// caller once the section is actually part of the result.
	out.stamp = &readBookmark{key: key, seen: seen}

	// Empty output is ambiguous: it reads equally as "empty file", "window past
	// EOF" and "something went wrong", and a caller acting on the wrong reading
	// of it can reach for a destructive fix. Both empty-looking cases say what
	// they are instead of returning "".
	if totalLines == 0 {
		out.body = fmt.Sprintf("%s is an empty file (0 lines).", display)
		return out, nil
	}

	if len(req.Ranges) > 0 {
		out.body = renderRanges(display, lines, totalLines, req.Ranges)
		return out, nil
	}

	start := req.Offset
	// A window that begins past EOF happens honestly — line numbers go stale
	// whenever an edit shrinks the file — but it used to come back as empty
	// output with nothing to say why. Name the file's real extent so the next
	// call can be ranged correctly.
	if start >= totalLines {
		out.body = fmt.Sprintf("%s has only %d lines — the requested window starts at line %d, past the end of the file. Line numbers may have shifted since this file was last read; re-read with a range within 1-%d.",
			display, totalLines, start+1, totalLines)
		return out, nil
	}

	// With no limit the caller asked for the file, not a window of it: every
	// remaining line is returned. A read is never truncated.
	limit := req.Limit
	if limit <= 0 {
		limit = totalLines
	}
	end := start + limit
	if end > totalLines {
		end = totalLines
	}

	// Number the lines and return them exactly as they are in the file: no line
	// is shortened and none is dropped.
	numbered, shownEnd := numberLines(lines, start, end)

	output := strings.Join(numbered, "\n")
	truncated := shownEnd < totalLines
	if truncated {
		output += fmt.Sprintf("\n\n(Showing lines %d-%d of %d. Use offset=%d to continue.)",
			start+1, shownEnd, totalLines, shownEnd)
		output += "\n\n" + rangedReadTrailer
	}

	out.body = output
	out.truncated = truncated
	return out, nil
}

// numberLines renders lines[start:end] with a "number|text" prefix. shownEnd is
// the exclusive index it reached. No line is shortened and none is dropped: the
// caller asked for these lines and gets them exactly as they are in the file,
// which is what an edit anchor is copied from.
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
func numberLines(lines []string, start, end int) (rendered []string, shownEnd int) {
	for i := start; i < end; i++ {
		rendered = append(rendered, fmt.Sprintf("%6d%s%s", i+1, lineNumberSep, lines[i]))
	}
	return rendered, end
}

// renderRanges numbers several windows of one file into a single section.
//
// Each window opens with a file:start-end heading and keeps the same
// number|text line format as a single-window read, so any returned line still
// copies straight into an edit anchor. Every window is rendered in full: the
// fan-out is one deliberate call, and a short window would be a silent loss.
func renderRanges(name string, lines []string, totalLines int, ranges []readRange) string {
	var b strings.Builder
	for i, r := range ranges {
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
		// An omitted end_line runs to the end of the file: the caller asked for
		// the window and left the rest open.
		end := totalLines
		if r.EndLine != nil && *r.EndLine >= start {
			end = *r.EndLine
		}
		if end > totalLines {
			end = totalLines
		}
		rendered, shownEnd := numberLines(lines, start-1, end)
		fmt.Fprintf(&b, "%s:%d-%d\n%s\n", name, start, shownEnd, strings.Join(rendered, "\n"))
	}
	return strings.TrimRight(b.String(), "\n")
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
// Three cases fall through to a normal read. An explicit window — a range, a
// positive offset, or a limit of any size — is a deliberate request and is
// honoured, so read(path, ranges=[{"start_line":1,"end_line":N}]) stays the way
// to demand a whole file and a bare limit pages content past the map whatever
// its size. A short file is cheaper read than mapped. And a file the mapper finds no
// declarations in — prose, data, an unsupported language whose heuristics
// caught nothing — has no map worth returning, so withholding its contents
// would strand the caller with nothing at all.
func mapInsteadOfWholeFile(path, display string, totalLines int, rangeRequested bool) (string, bool) {
	if rangeRequested || totalLines <= rangedReadThreshold {
		return "", false
	}

	fm, err := codemap.Outline(path)
	if err != nil || len(fm.Symbols) == 0 {
		return "", false
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s is %d lines — too long to read in full. Its map is below.\n\n",
		display, totalLines)
	b.WriteString(codemap.Render(fm))
	fmt.Fprintf(&b, "\nRead the part you need with read(path, ranges=[{\"start_line\":N,\"end_line\":M}]), or every part together with read(path, ranges=[{\"start_line\":N,\"end_line\":M}, {\"start_line\":N2,\"end_line\":M2}]).\n"+
		"If you genuinely need the whole file, ask for it explicitly with a single range covering it: ranges=[{\"start_line\":1,\"end_line\":%d}].\n",
		totalLines)
	return b.String(), true
}
