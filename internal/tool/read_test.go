package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// numbered builds a file whose Nth line is literally "lineN", so a returned
// window can be checked against the lines it was asked for.
func numbered(t *testing.T, n int) string {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString("line")
		b.WriteString(strconv.Itoa(i))
		b.WriteString("\n")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	mustWriteFile(t, path, b.String())
	return path
}

func readWith(t *testing.T, path string, params map[string]any) string {
	t.Helper()
	params["path"] = path
	args, _ := json.Marshal(params)
	res, err := ReadTool{}.Execute(context.Background(), args, Context{SessionDir: filepath.Dir(path)})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return res.Output
}

// A range is 1-based and inclusive, matching what file_map prints. The
// equivalent offset is start_line-1; getting that wrong shifts the window by a
// line without erroring, which is exactly the failure the range form exists to
// prevent.
func TestReadTool_RangeIs1BasedInclusive(t *testing.T) {
	path := numbered(t, 20)

	out := readWith(t, path, map[string]any{"ranges": []map[string]any{{"start_line": 5, "end_line": 8}}})

	for _, want := range []string{"line5", "line6", "line7", "line8"} {
		if !strings.Contains(out, lineNumberSep+want) {
			t.Errorf("window missing %s:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{lineNumberSep + "line4", lineNumberSep + "line9"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("window included %s, so the range is off by one:\n%s", strings.TrimPrefix(unwanted, lineNumberSep), out)
		}
	}
}

// A single-line range is the degenerate case of the inclusive rule and is what
// file_map emits for one-line declarations.
func TestReadTool_SingleLineRange(t *testing.T) {
	out := readWith(t, numbered(t, 20), map[string]any{"ranges": []map[string]any{{"start_line": 12, "end_line": 12}}})

	if !strings.Contains(out, lineNumberSep+"line12") {
		t.Errorf("single-line range lost its line:\n%s", out)
	}
	if strings.Contains(out, lineNumberSep+"line11") || strings.Contains(out, lineNumberSep+"line13") {
		t.Errorf("single-line range returned neighbours:\n%s", out)
	}
}

// A range without end_line reads to the default limit, so a caller can open a
// file at a declaration without knowing where it ends.
func TestReadTool_RangeWithoutEndLine(t *testing.T) {
	out := readWith(t, numbered(t, 20), map[string]any{"ranges": []map[string]any{{"start_line": 18}}})

	if !strings.Contains(out, lineNumberSep+"line18") || !strings.Contains(out, lineNumberSep+"line20") {
		t.Errorf("open-ended range lost lines:\n%s", out)
	}
	if strings.Contains(out, lineNumberSep+"line17") {
		t.Errorf("open-ended range started too early:\n%s", out)
	}
}

// An end_line past EOF clamps instead of erroring: file_map ranges can go stale
// after an edit, and truncating the read is far kinder than failing it.
func TestReadTool_EndLineBeyondEOFClamps(t *testing.T) {
	out := readWith(t, numbered(t, 20), map[string]any{"ranges": []map[string]any{{"start_line": 19, "end_line": 500}}})

	if !strings.Contains(out, lineNumberSep+"line20") {
		t.Errorf("clamped read lost the last line:\n%s", out)
	}
}

// offset stays 0-based for paging. It must keep working alongside ranges, since
// the truncation notice tells the agent to continue with offset.
func TestReadTool_OffsetRemains0Based(t *testing.T) {
	out := readWith(t, numbered(t, 20), map[string]any{"offset": 5, "limit": 2})

	if !strings.Contains(out, lineNumberSep+"line6") || !strings.Contains(out, lineNumberSep+"line7") {
		t.Errorf("offset=5 limit=2 should yield lines 6-7:\n%s", out)
	}
}

// A single window used to have a second spelling: start_line/end_line at the
// top level. Two shapes for one idea meant a caller could learn one and get
// different lines from the other, and an ignored argument reads the wrong
// lines with nothing to say so. The top-level pair is now refused, naming the
// form that replaced it.
func TestReadTool_StartEndLineArgsAreRefused(t *testing.T) {
	path := numbered(t, 20)
	for name, params := range map[string]map[string]any{
		"both":       {"start_line": 10, "end_line": 3},
		"start_only": {"start_line": 5},
		"end_only":   {"end_line": 3},
	} {
		t.Run(name, func(t *testing.T) {
			params["path"] = path
			args, _ := json.Marshal(params)
			_, err := ReadTool{}.Execute(context.Background(), args, Context{SessionDir: filepath.Dir(path)})
			if err == nil {
				t.Fatal("the top-level start_line/end_line form was accepted")
			}
			for _, want := range []string{"ranges", "start_line"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error should mention %q, got: %v", want, err)
				}
			}
		})
	}
}

// longGoFile builds a Go file with n filler functions, comfortably past
// rangedReadThreshold.
func longGoFile(t *testing.T, n int) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("package demo\n\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "// Fn%d does a thing.\nfunc Fn%d() int {\n\treturn %d\n}\n\n", i, i, i)
	}
	path := filepath.Join(t.TempDir(), "big.go")
	mustWriteFile(t, path, b.String())
	return path
}

// Instructing the model to map first leaves it a choice; this makes it
// structural. An unranged read of a long file returns the map, so the whole
// file cannot land in context by accident.
func TestReadTool_LongFileWithoutRangeReturnsMap(t *testing.T) {
	path := longGoFile(t, 60)

	out := readWith(t, path, map[string]any{})

	if !strings.Contains(out, "too long to read in full") {
		t.Fatalf("unranged read of a long file returned contents, not the map:\n%s", truncate(out))
	}
	if !strings.Contains(out, "ranges=[{\"start_line\":N,\"end_line\":M}]") {
		t.Error("map response omits how to ask for a range")
	}
	if !strings.Contains(out, "ranges=[") {
		t.Error("map response omits the multi-window form")
	}
	// It must be a map, not the source.
	if strings.Contains(out, "return 7\n") {
		t.Error("map response leaked file contents")
	}
	if !strings.Contains(out, "func Fn7() int") {
		t.Errorf("map response missing declarations:\n%s", truncate(out))
	}
}

// The map branch is one of the two places a map reaches the model, and it is
// not bounded: an outline is the answer the caller asked for, so the whole
// outline arrives even when it runs past the size every other tool is capped at.
func TestReadTool_MapIsReturnedWhole(t *testing.T) {
	var b strings.Builder
	b.WriteString("package big\n\n")
	for i := 0; i < 450; i++ {
		fmt.Fprintf(&b, "// HandleThing%d processes a request and returns the payload for the caller to inspect.\n", i)
		fmt.Fprintf(&b, "func HandleThing%d(ctx context.Context, request *SomeRequestType, options ...OptionWithAVeryLongName) (*SomeResponseType, error) {\n\treturn nil, nil\n}\n\n", i)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "big.go")
	mustWriteFile(t, path, b.String())

	args, _ := json.Marshal(map[string]any{"path": path})
	res, err := ReadTool{}.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if !strings.Contains(res.Output, "too long to read in full") {
		t.Fatalf("expected the map branch:\n%s", truncate(res.Output))
	}
	if len(res.Output) <= MaxToolOutputBytes {
		t.Errorf("map is %d bytes — a whole map may run past the %d-byte cap other tools use", len(res.Output), MaxToolOutputBytes)
	}
	if strings.Contains(res.Output, "more line(s) not shown") {
		t.Errorf("the map was cut:\n%s", truncate(res.Output))
	}
	if res.Truncated {
		t.Error("a whole map is not a truncated read")
	}
	if !res.Uncapped {
		t.Error("the map result must opt out of the loop's backstop")
	}
}

// The interception must never block a caller who said what they wanted.
func TestReadTool_LongFileWithRangeIsHonoured(t *testing.T) {
	path := longGoFile(t, 60)

	cases := map[string]map[string]any{
		"single ranges entry": {"ranges": []map[string]any{{"start_line": 4, "end_line": 6}}},
		"offset paging":       {"offset": 3, "limit": 3},
		"explicit whole":      {"ranges": []map[string]any{{"start_line": 1, "end_line": 5000}}},
		"limit only":          {"limit": 10},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			out := readWith(t, path, params)
			if strings.Contains(out, "too long to read in full") {
				t.Errorf("explicit request was intercepted:\n%s", truncate(out))
			}
			if !strings.Contains(out, "func Fn") {
				t.Errorf("explicit request returned no contents:\n%s", truncate(out))
			}
		})
	}
}

// Short files are cheaper read than mapped, so they are untouched.
func TestReadTool_ShortFileUnaffected(t *testing.T) {
	out := readWith(t, longGoFile(t, 5), map[string]any{})

	if strings.Contains(out, "too long to read in full") {
		t.Error("a short file was intercepted")
	}
	if !strings.Contains(out, "return 0") {
		t.Error("short file did not return its contents")
	}
}

// A file with no mappable declarations has no map worth returning; withholding
// its contents would leave the caller with nothing at all.
func TestReadTool_LongUnmappableFileStillReads(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&b, "plain prose line %d with no declarations\n", i)
	}
	path := filepath.Join(t.TempDir(), "notes.txt")
	mustWriteFile(t, path, b.String())

	out := readWith(t, path, map[string]any{})

	if strings.Contains(out, "too long to read in full") {
		t.Error("intercepted a file that has no map to offer")
	}
	if !strings.Contains(out, "plain prose line 3") {
		t.Error("unmappable long file returned no contents")
	}
}

// An end_line without start_line inside a range used to be silently dropped,
// falling back to an unranged read from line 1 with no sign the argument was
// ignored. It now defaults start_line to 1, so the range is honoured as "from
// the top through end_line".
func TestReadTool_RangeEndLineWithoutStartLine(t *testing.T) {
	out := readWith(t, numbered(t, 20), map[string]any{"ranges": []map[string]any{{"end_line": 3}}})

	for _, want := range []string{lineNumberSep + "line1", lineNumberSep + "line2", lineNumberSep + "line3"} {
		if !strings.Contains(out, want) {
			t.Errorf("window missing %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, lineNumberSep+"line4") {
		t.Errorf("end_line was not honoured, window ran past it:\n%s", out)
	}
}

// A file's total line count must agree with codemap's, since file_map and
// read are meant to be used together against the same numbering. Both must
// treat a file ending in a newline as not having a trailing phantom line.
func TestReadTool_TrailingNewlineDoesNotInflateLineCount(t *testing.T) {
	path := numbered(t, 20) // ends with a trailing "\n" after line20

	// A small limit forces the truncation notice, which states the total.
	paged := readWith(t, path, map[string]any{"offset": 0, "limit": 5})
	if strings.Contains(paged, "of 21") {
		t.Errorf("trailing newline was counted as an extra line:\n%s", paged)
	}
	if !strings.Contains(paged, "of 20") {
		t.Errorf("expected a total of 20 lines:\n%s", paged)
	}

	// An unranged read of the whole (short) file must not include a phantom
	// line 21.
	full := readWith(t, path, map[string]any{})
	if strings.Contains(full, fmt.Sprintf("%6d%s", 21, lineNumberSep)) {
		t.Errorf("read returned a phantom trailing blank line 21:\n%s", full)
	}
}

// A binary file has no line structure. Splitting it on incidental '\n' bytes
// used to produce a flood of garbage "lines" with fabricated line numbers
// instead of an error; it must now be reported plainly instead.
func TestReadTool_BinaryFileReturnsNotice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "img.bin")
	data := []byte{0x89, 'P', 'N', 'G', 0x00, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0xff, 0x00}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	out := readWith(t, path, map[string]any{})

	if !strings.Contains(out, "binary file") {
		t.Errorf("binary file was not flagged as binary:\n%q", out)
	}
	if strings.Contains(out, "\x00") {
		t.Errorf("raw binary bytes leaked into tool output:\n%q", out)
	}
}

func truncate(s string) string {
	if len(s) <= 600 {
		return s
	}
	return s[:600] + "…"
}

// A bare limit is an explicit request: it names a window the caller chose, so
// it is honoured at any size. Until 2026-10-25 a bare limit past the map
// threshold was treated as the whole-file read it can degenerate into and was
// answered with the map, which left limit=250 on a 302-line file returning an
// outline instead of the lines it asked for. That cap went out with the byte
// caps when reads became uncapped; an unranged read (no window argument at
// all) still answers with the map — the sibling test above pins that half.
func TestReadTool_LargeBareLimitIsHonoured(t *testing.T) {
	path := longGoFile(t, 60)

	args, _ := json.Marshal(map[string]any{"path": path, "limit": 5000})
	res, err := ReadTool{}.Execute(context.Background(), args, Context{SessionDir: filepath.Dir(path)})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if strings.Contains(res.Output, "too long to read in full") {
		t.Errorf("a bare limit was intercepted by the map:\n%s", truncate(res.Output))
	}
	if !strings.Contains(res.Output, "return 7") {
		t.Errorf("bare limit returned no contents:\n%s", truncate(res.Output))
	}
	if res.Truncated {
		t.Error("a limit past the file's end reads the whole file and must not report truncation")
	}
}

// The boundary the cap used to draw is gone: bare limits below, at and past
// the threshold are all honoured as windows, and neither side of it answers
// with the map. Until 2026-10-25 a bare limit past 200 was denied its window
// because it "said how much to read but not which part"; that distinction was
// dropped with the byte caps.
func TestReadTool_BareLimitBoundary(t *testing.T) {
	path := longGoFile(t, 60)

	for _, limit := range []int{rangedReadThreshold - 1, rangedReadThreshold, rangedReadThreshold + 1} {
		out := readWith(t, path, map[string]any{"limit": limit})
		if strings.Contains(out, "too long to read in full") {
			t.Errorf("limit=%d: a bare limit was intercepted by the map:\n%s", limit, truncate(out))
		}
		if !strings.Contains(out, "func Fn") {
			t.Errorf("limit=%d: bare limit returned no contents:\n%s", limit, truncate(out))
		}
	}
}

// The flipped bug, pinned end to end: a bare limit on a file past the map
// threshold returns CONTENT — the plain content title, exactly limit rendered
// lines, and the truncation footer, because a window that leaves lines unread
// says so. offset+limit paging above the threshold is honoured the same way.
func TestReadTool_BareLimitPagesContentPastTheThreshold(t *testing.T) {
	path := longGoFile(t, 60) // 302 lines, past rangedReadThreshold

	args, _ := json.Marshal(map[string]any{"path": path, "limit": 250})
	res, err := ReadTool{}.Execute(context.Background(), args, Context{SessionDir: filepath.Dir(path)})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if res.Title != "big.go" {
		t.Errorf("title = %q, want the plain content title, not a map", res.Title)
	}
	if strings.Contains(res.Output, "too long to read in full") {
		t.Fatalf("limit=250 was intercepted by the map:\n%s", truncate(res.Output))
	}
	// Every rendered content line carries the number|text separator at column 7;
	// the footer and the trailer do not.
	shown := 0
	for _, line := range strings.Split(res.Output, "\n") {
		if len(line) > 6 && line[6] == lineNumberSep[0] {
			shown++
		}
	}
	if shown != 250 {
		t.Errorf("limit=250 rendered %d lines, want exactly 250", shown)
	}
	if !strings.Contains(res.Output, "Showing lines 1-250 of") ||
		!strings.Contains(res.Output, "Use offset=250 to continue.") {
		t.Errorf("truncation footer for the unread lines missing:\n%s", truncate(res.Output))
	}
	if !res.Truncated {
		t.Error("a window that leaves lines unread must report truncation")
	}
	if !res.Uncapped {
		t.Error("a read must opt out of the loop's backstop")
	}

	// offset+limit paging works past the threshold too: line 251 onward.
	paged := readWith(t, path, map[string]any{"offset": 250, "limit": 100})
	if strings.Contains(paged, "too long to read in full") {
		t.Fatalf("offset+limit paging was intercepted by the map:\n%s", truncate(paged))
	}
	if !strings.Contains(paged, fmt.Sprintf("%6d%s", 251, lineNumberSep)) {
		t.Errorf("offset=250 must open at line 251:\n%s", truncate(paged))
	}
	if !strings.Contains(paged, "func Fn59") {
		t.Errorf("offset=250 limit=100 stopped short of the file's end:\n%s", truncate(paged))
	}
	if strings.Contains(paged, "Showing lines") {
		t.Errorf("a window that reaches EOF must not report truncation:\n%s", truncate(paged))
	}
	if strings.Contains(paged, fmt.Sprintf("%6d%s", 250, lineNumberSep)) {
		t.Errorf("offset=250 re-showed line 250 (offset is 0-based):\n%s", truncate(paged))
	}
}

// A negative offset or limit used to be silently ignored — offset clamped to
// 0, limit read as "every line" — so the call ran as a different read than the
// caller asked for with nothing to say so. Each is now refused, naming the
// argument, in the single-file form, inside files entries, and at the top
// level beside files (whose window check only ever saw positives).
func TestReadTool_NegativeWindowArgsAreRefused(t *testing.T) {
	dir := t.TempDir()
	writeNumberedLines(t, filepath.Join(dir, "f.txt"), 20)

	cases := map[string]string{
		"offset":            `{"path":"f.txt","offset":-1}`,
		"limit":             `{"path":"f.txt","limit":-1}`,
		"offset and limit":  `{"path":"f.txt","offset":-5,"limit":-3}`,
		"files entry":       `{"files":[{"path":"f.txt","offset":-2}]}`,
		"files entry limit": `{"files":[{"path":"f.txt","limit":-2}]}`,
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ReadTool{}.Execute(context.Background(), json.RawMessage(args), Context{SessionDir: dir})
			if err == nil {
				t.Fatal("a negative window argument was accepted")
			}
			if !strings.Contains(err.Error(), "negative") {
				t.Errorf("error should name the negative argument, got: %v", err)
			}
		})
	}

	// A top-level negative window beside files used to slip past the check
	// that refuses a top-level window, because the check only saw positives.
	topLevel, _ := json.Marshal(map[string]any{
		"files":  []map[string]any{{"path": "f.txt"}},
		"offset": -4,
	})
	_, err := ReadTool{}.Execute(context.Background(), topLevel, Context{SessionDir: dir})
	if err == nil {
		t.Fatal("a top-level window beside files was accepted")
	}
	if !strings.Contains(err.Error(), "files entries carry their own") {
		t.Errorf("the top-level window should be refused as such, got: %v", err)
	}
}

// An explicitly given end_line must name a real line. A value <= 0 once
// slipped past the inverted-range check and read as "to the end of the file"
// — silent, and the opposite of what the caller sent. Each value is now
// refused, in the single-file form and inside a files entry; an omitted
// end_line still runs to the end of the file (TestReadTool_RangeWithoutEndLine).
func TestReadTool_ExplicitEndLineZeroOrNegativeIsRefused(t *testing.T) {
	dir := t.TempDir()
	writeNumberedLines(t, filepath.Join(dir, "f.txt"), 20)

	cases := map[string]string{
		"end_line zero":     `{"path":"f.txt","ranges":[{"start_line":2,"end_line":0}]}`,
		"end_line negative": `{"path":"f.txt","ranges":[{"start_line":2,"end_line":-1}]}`,
		"end_line only":     `{"path":"f.txt","ranges":[{"end_line":-3}]}`,
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ReadTool{}.Execute(context.Background(), json.RawMessage(args), Context{SessionDir: dir})
			if err == nil {
				t.Fatal("a non-positive end_line was accepted")
			}
			if !strings.Contains(err.Error(), "end_line") {
				t.Errorf("error should name end_line, got: %v", err)
			}
		})
	}

	// The same refusal inside a files entry.
	entry := `{"files":[{"path":"f.txt","ranges":[{"start_line":1,"end_line":-2}]}]}`
	_, err := ReadTool{}.Execute(context.Background(), json.RawMessage(entry), Context{SessionDir: dir})
	if err == nil {
		t.Fatal("a files entry accepted a non-positive end_line")
	}
	if !strings.Contains(err.Error(), "end_line") {
		t.Errorf("error should name end_line, got: %v", err)
	}
}

// A path surrounded by whitespace is trimmed once at entry: the same cleaned
// value is checked and resolved, so the call reads the file instead of dying
// as a confusing double-space stat error ("stat  f.txt ...").
func TestReadTool_SurroundedPathIsTrimmedOnce(t *testing.T) {
	dir := t.TempDir()
	writeNumberedLines(t, filepath.Join(dir, "f.txt"), 3)

	res, err := ReadTool{}.Execute(context.Background(), json.RawMessage(`{"path":" f.txt "}`), Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("trimmed path: %v", err)
	}
	if res.Title != "f.txt" || res.Output == "" {
		t.Errorf("title = %q, output = %q, want the file read via its trimmed name", res.Title, res.Output)
	}

	// The files form gets the same one-trim treatment.
	res, err = ReadTool{}.Execute(context.Background(), json.RawMessage(`{"files":[{"path":" f.txt "}]}`), Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("files entry trimmed path: %v", err)
	}
	if !strings.Contains(res.Output, "== f.txt ==") {
		t.Errorf("output = %q, want the f.txt section header", res.Output)
	}
}

// Paging is placed, not merely sized, so an offset keeps working on its own: a
// caller who passes one has already seen enough of the file to know where to
// resume. Narrowing the limit hatch must not close this.
func TestReadTool_OffsetStillPagesWithoutARange(t *testing.T) {
	path := longGoFile(t, 60)

	out := readWith(t, path, map[string]any{"offset": 100})

	if strings.Contains(out, "too long to read in full") {
		t.Errorf("offset paging was intercepted:\n%s", truncate(out))
	}
	if !strings.Contains(out, "func Fn") {
		t.Errorf("offset paging returned no contents:\n%s", truncate(out))
	}
}

// The description has to state the window rule, or a caller passing limit=5000
// gets the map back with no way to tell whether that was the rule or a
// malfunction. Since 2026-10-25 the rule reads the other way: a bare limit of
// any size is honoured as a window, and the map answers only a bare whole-file
// read. The stale claim about the 200-line cap must be gone too.
func TestReadDescription_StatesTheWindowRule(t *testing.T) {
	desc := ReadTool{}.Description()
	for _, want := range []string{"no window at all", "a bare offset is a placed window", "a bare limit is honoured on its own at any size", "falls through to a plain read", "with no map and no notice"} {
		if !strings.Contains(desc, want) {
			t.Errorf("read description missing %q; the window rule is unstated", want)
		}
	}
	if strings.Contains(desc, "only up to 200") {
		t.Error("description still claims a bare limit is honoured only up to 200 lines")
	}
}

// TestReadTool_OutputRoundTripsIntoEdit is the property that matters about the
// numbering format: what a caller strips off the front must be unambiguous, so
// the text it copies is byte-identical to what is in the file.
//
// With a tab separator it was not. A once-indented line of Go came back as the
// number followed by two adjacent tabs — one the separator, one the code's own
// indentation — with nothing to mark the boundary, and three levels of nesting
// made it four. Stripping one too many produced an anchor that was right in
// every respect except its leading whitespace, which "edit" can only report as
// text that is not in the file. This test reads a tab-indented Go file, takes
// the content exactly as the format promises, and requires the edit to apply.
func TestReadTool_OutputRoundTripsIntoEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sqlite.go")
	src := "func open() error {\n" +
		"\tif _, err := d.Exec(\"PRAGMA foreign_keys = ON\"); err != nil {\n" +
		"\t\td.Close()\n" +
		"\t\treturn fmt.Errorf(\"pragma foreign_keys: %w\", err)\n" +
		"\t}\n" +
		"}\n"
	mustWriteFile(t, path, src)

	out := readWith(t, path, map[string]any{})

	// Take lines 3-4 the way the format says to: everything after the FIRST
	// separator is literal content.
	var anchor []string
	for _, rendered := range strings.Split(out, "\n") {
		i := strings.Index(rendered, lineNumberSep)
		if i < 0 {
			continue
		}
		content := rendered[i+len(lineNumberSep):]
		if strings.Contains(content, "d.Close()") || strings.Contains(content, "pragma foreign_keys") {
			anchor = append(anchor, content)
		}
	}
	if len(anchor) != 2 {
		t.Fatalf("expected to recover 2 lines, got %d from:\n%s", len(anchor), out)
	}

	// Both lines are two tabs deep in the source; the recovered text must say so.
	for _, line := range anchor {
		if !strings.HasPrefix(line, "\t\t") {
			t.Errorf("recovered line lost its indentation: %q", line)
		}
	}

	// The real proof: it works as an edit anchor.
	old := strings.Join(anchor, "\n")
	if _, err := editWith(t, dir, map[string]any{
		"path": path, "old_string": old, "new_string": "\t\treturn nil",
	}); err != nil {
		t.Fatalf("text copied straight out of read did not match the file: %v", err)
	}
}

// Empty output is ambiguous — it reads equally as "empty file", "window past
// EOF" and "something went wrong" — and a caller acting on the wrong reading
// can reach for a destructive fix. Both empty-looking cases must say what they
// are instead of returning "".
func TestReadTool_EmptyFileSaysSo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	mustWriteFile(t, path, "")

	out := readWith(t, path, map[string]any{})

	if out == "" {
		t.Fatal("an empty file must not read back as empty output")
	}
	if !strings.Contains(out, "empty") {
		t.Errorf("output should say the file is empty, got: %q", out)
	}
}

// A window past EOF happens honestly: line numbers go stale whenever an edit
// shrinks the file. It used to come back as empty output with nothing to say
// why — from which a caller has concluded "the file is empty" and rewritten
// it. The reply has to carry the file's real extent so the next call can be
// ranged correctly.
func TestReadTool_WindowPastEOFExplains(t *testing.T) {
	path := numbered(t, 20)

	for name, params := range map[string]map[string]any{
		"stale range":  {"ranges": []map[string]any{{"start_line": 50, "end_line": 80}}},
		"stale offset": {"offset": 20},
	} {
		t.Run(name, func(t *testing.T) {
			out := readWith(t, path, params)
			if out == "" {
				t.Fatal("a past-EOF window must not read back as empty output")
			}
			for _, want := range []string{"only 20 lines", "past the end"} {
				if !strings.Contains(out, want) {
					t.Errorf("output should say %q, got: %q", want, out)
				}
			}
		})
	}
}

// A read returns the file's lines exactly as they are. A very long line used to
// be sliced at MaxLineLength, which could split a multi-byte character and ship
// invalid UTF-8 to the provider; nothing is sliced now, so the whole line — and
// the edit anchor copied from it — is intact.
func TestReadTool_LongLineComesBackWhole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "long.txt")
	long := strings.Repeat("a", MaxLineLength-1) + "→ tail"
	mustWriteFile(t, path, long+"\n")

	out := readWith(t, path, map[string]any{})

	if !utf8.ValidString(out) {
		t.Error("read output contains invalid UTF-8")
	}
	if !strings.Contains(out, long) {
		t.Error("the long line came back shortened")
	}
	if strings.Contains(out, lineTruncatedSuffix) {
		t.Error("the long line should not be marked truncated")
	}
}

// One ranged call is the shape a post-grep fan-out needs: several hits in the
// same file used to cost one read each. Every window keeps its own numbering
// and the requested order, so the caller can walk the result top to bottom.
func TestReadTool_RangesReturnEveryWindowInOrder(t *testing.T) {
	path := numbered(t, 100)

	out := readWith(t, path, map[string]any{"ranges": []map[string]any{
		{"start_line": 3, "end_line": 5},
		{"start_line": 40, "end_line": 42},
	}})

	for _, want := range []struct {
		n    int
		text string
	}{
		{3, "line3"}, {4, "line4"}, {5, "line5"},
		{40, "line40"}, {41, "line41"}, {42, "line42"},
	} {
		if !strings.Contains(out, fmt.Sprintf("%6d%s%s", want.n, lineNumberSep, want.text)) {
			t.Errorf("multi-range output missing %s:\n%s", want.text, truncate(out))
		}
	}
	for _, unwanted := range []struct {
		n    int
		text string
	}{
		{2, "line2"}, {6, "line6"}, {39, "line39"},
	} {
		if strings.Contains(out, fmt.Sprintf("%6d%s%s", unwanted.n, lineNumberSep, unwanted.text)) {
			t.Errorf("multi-range output included %s:\n%s", unwanted.text, truncate(out))
		}
	}
	if strings.Index(out, "line3") > strings.Index(out, "line40") {
		t.Error("windows came back out of request order")
	}
	for _, header := range []string{"f.txt:3-5", "f.txt:40-42"} {
		if !strings.Contains(out, header) {
			t.Errorf("output missing the %q heading:\n%s", header, truncate(out))
		}
	}
}

// The map interception is a whole-file guard, not a ban on reading a long
// file: ranges is an explicit request for windows, so it must pass through.
func TestReadTool_RangesAreHonouredOnALongFile(t *testing.T) {
	path := longGoFile(t, 60)

	out := readWith(t, path, map[string]any{"ranges": []map[string]any{
		{"start_line": 4, "end_line": 6},
		{"start_line": 24, "end_line": 26},
	}})

	if strings.Contains(out, "too long to read in full") {
		t.Errorf("a ranges request was intercepted and mapped:\n%s", truncate(out))
	}
	for _, want := range []string{"func Fn0", "func Fn4"} {
		if !strings.Contains(out, want) {
			t.Errorf("multi-range read missing %q:\n%s", want, truncate(out))
		}
	}
}

// ranges and offset/limit are two ways to place a window. Mixing them has no
// single reading — a caller that sends both meant one of them, and guessing
// which can silently read the wrong lines — so the call is refused.
func TestReadTool_RangesRefusesPagingArgs(t *testing.T) {
	path := numbered(t, 20)

	for name, params := range map[string]map[string]any{
		"offset": {"ranges": []map[string]any{{"start_line": 1, "end_line": 2}}, "offset": 5},
		"limit":  {"ranges": []map[string]any{{"start_line": 1, "end_line": 2}}, "limit": 5},
	} {
		t.Run(name, func(t *testing.T) {
			params["path"] = path
			args, _ := json.Marshal(params)
			_, err := ReadTool{}.Execute(context.Background(), args, Context{SessionDir: filepath.Dir(path)})
			if err == nil {
				t.Fatal("ranges combined with a single-window param was accepted")
			}
			if !strings.Contains(err.Error(), "ranges cannot be combined") {
				t.Errorf("error should name the conflict, got: %v", err)
			}
		})
	}
}

func TestReadTool_RangesInvertedIsRefused(t *testing.T) {
	path := numbered(t, 20)
	args, _ := json.Marshal(map[string]any{
		"path": path,
		"ranges": []map[string]any{
			{"start_line": 3, "end_line": 5},
			{"start_line": 10, "end_line": 3},
		},
	})

	_, err := ReadTool{}.Execute(context.Background(), args, Context{SessionDir: filepath.Dir(path)})
	if err == nil {
		t.Fatal("an inverted range inside ranges was accepted")
	}
	for _, want := range []string{"ranges[1]", "inverted"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

// A stale line number is honest and common; it must not discard the windows
// that are still inside the file.
func TestReadTool_RangesPastEOFStillServeOtherWindows(t *testing.T) {
	path := numbered(t, 20)

	out := readWith(t, path, map[string]any{"ranges": []map[string]any{
		{"start_line": 50, "end_line": 60},
		{"start_line": 3, "end_line": 4},
	}})

	if !strings.Contains(out, "past the end") || !strings.Contains(out, "only 20 lines") {
		t.Errorf("stale window should name the file's extent:\n%s", truncate(out))
	}
	if !strings.Contains(out, fmt.Sprintf("%6d%sline3", 3, lineNumberSep)) {
		t.Errorf("a stale window dropped a window that was inside the file:\n%s", truncate(out))
	}
}

// Every window comes back whole, however much they add up to: a fan-out is one
// deliberate call, and cutting it would hand back less than the caller asked
// for with no way to tell which window was short.
func TestReadTool_RangesAreNotCut(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wide.txt")
	var b strings.Builder
	for i := 0; i < 80; i++ {
		b.WriteString(strings.Repeat("x", 1024))
		b.WriteString("\n")
	}
	mustWriteFile(t, path, b.String())

	out := readWith(t, path, map[string]any{"ranges": []map[string]any{
		{"start_line": 1, "end_line": 80},
		{"start_line": 1, "end_line": 1},
		{"start_line": 1, "end_line": 1},
	}})

	if strings.Contains(out, "cut at the output cap") || strings.Contains(out, "not shown") {
		t.Fatalf("a window was cut at an output cap:\n%s", truncate(out))
	}
	for _, want := range []string{"wide.txt:1-80", "wide.txt:1-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing window heading %q", want)
		}
	}
	if len(out) <= MaxToolOutputBytes {
		t.Errorf("three windows total %d bytes — the read should not be bounded at %d", len(out), MaxToolOutputBytes)
	}
}

// The result carries the number|text contract unchanged, so any line of any
// window can be copied into an edit anchor.
func TestReadTool_RangesKeepTheLineNumberContract(t *testing.T) {
	path := numbered(t, 100)

	out := readWith(t, path, map[string]any{"ranges": []map[string]any{
		{"start_line": 7, "end_line": 7},
		{"start_line": 90, "end_line": 90},
	}})

	for _, want := range []string{
		fmt.Sprintf("%6d%sline7", 7, lineNumberSep),
		fmt.Sprintf("%6d%sline90", 90, lineNumberSep),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("numbered line %q missing:\n%s", want, truncate(out))
		}
	}
}

// The schema description is the only place a caller learns the multi-window
// form exists before making the call.
func TestReadDescription_AdvertisesRanges(t *testing.T) {
	desc := ReadTool{}.Description()
	for _, want := range []string{"ranges", "one call", "cannot be combined with offset or limit"} {
		if !strings.Contains(desc, want) {
			t.Errorf("read description missing %q, so the multi-window form is unstated", want)
		}
	}
}

// The trailer plants the multi-window form at the decision point: a window was
// just read and more of the file remains.
func TestReadTool_TruncatedReadAdvertisesRanges(t *testing.T) {
	out := readWith(t, numbered(t, 30), map[string]any{"offset": 0, "limit": 5})

	if !strings.Contains(out, "ranges=[") {
		t.Errorf("a truncated read should name the multi-window form:\n%s", truncate(out))
	}
}

func TestReadTool_CompleteReadHasNoRangesTrailer(t *testing.T) {
	out := readWith(t, numbered(t, 10), map[string]any{})

	if strings.Contains(out, "ranges=[") {
		t.Errorf("a complete read has nothing left to window:\n%s", out)
	}
}

// Providers ignore the "integer" hint and some models write line numbers with
// a decimal point; the integral-float coercion has to reach inside ranges too,
// or a well-formed call fails to parse.
func TestReadTool_RangesAcceptFloatLineNumbers(t *testing.T) {
	path := numbered(t, 20)
	args := json.RawMessage(fmt.Sprintf(`{"path":%q,"ranges":[{"start_line":3.0,"end_line":4.0}]}`, path))

	res, err := ReadTool{}.Execute(context.Background(), args, Context{SessionDir: filepath.Dir(path)})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res.Output, "f.txt:3-4") {
		t.Errorf("integral floats in a range should decode as integers:\n%s", truncate(res.Output))
	}
}

// An omitted end_line runs to the end of the file: the caller named a start and
// left the rest open, and a read is never truncated.
func TestReadTool_RangeWithoutEndReadsToEOF(t *testing.T) {
	const total = 2500
	out := readWith(t, numbered(t, total), map[string]any{"ranges": []map[string]any{
		{"start_line": 1},
	}})

	if !strings.Contains(out, "f.txt:1-"+strconv.Itoa(total)) {
		t.Errorf("a range without end_line should run to the end of the file:\n%s", truncate(out))
	}
	last := fmt.Sprintf("%6d%sline%d", total, lineNumberSep, total)
	if !strings.Contains(out, last) {
		t.Errorf("range without end_line stopped short; missing %q", last)
	}
}
