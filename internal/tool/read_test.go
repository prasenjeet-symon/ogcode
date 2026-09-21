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

// start_line/end_line are 1-based and inclusive, matching what file_map prints.
// The equivalent offset is start_line-1; getting that wrong shifts the window by
// a line without erroring, which is exactly the failure these params exist to
// prevent.
func TestReadTool_StartEndLineAre1BasedInclusive(t *testing.T) {
	path := numbered(t, 20)

	out := readWith(t, path, map[string]any{"start_line": 5, "end_line": 8})

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
	out := readWith(t, numbered(t, 20), map[string]any{"start_line": 12, "end_line": 12})

	if !strings.Contains(out, lineNumberSep+"line12") {
		t.Errorf("single-line range lost its line:\n%s", out)
	}
	if strings.Contains(out, lineNumberSep+"line11") || strings.Contains(out, lineNumberSep+"line13") {
		t.Errorf("single-line range returned neighbours:\n%s", out)
	}
}

// start_line without end_line reads from there to the default limit, so a
// caller can open a file at a declaration without knowing where it ends.
func TestReadTool_StartLineWithoutEndLine(t *testing.T) {
	out := readWith(t, numbered(t, 20), map[string]any{"start_line": 18})

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
	out := readWith(t, numbered(t, 20), map[string]any{"start_line": 19, "end_line": 500})

	if !strings.Contains(out, lineNumberSep+"line20") {
		t.Errorf("clamped read lost the last line:\n%s", out)
	}
}

// offset stays 0-based for paging. Both forms must keep working, since the
// truncation notice tells the agent to continue with offset.
func TestReadTool_OffsetRemains0Based(t *testing.T) {
	out := readWith(t, numbered(t, 20), map[string]any{"offset": 5, "limit": 2})

	if !strings.Contains(out, lineNumberSep+"line6") || !strings.Contains(out, lineNumberSep+"line7") {
		t.Errorf("offset=5 limit=2 should yield lines 6-7:\n%s", out)
	}
}

// An end_line below start_line is always a caller slip — no amount of file
// drift produces one — and either reading of the swapped values is a guess.
// This used to fall back to the default 2000-line window, which handed back
// far more than the caller meant with nothing to say end_line was ignored;
// refusing with both numbers named costs one round trip and removes the guess.
func TestReadTool_InvertedRangeIsRefused(t *testing.T) {
	path := numbered(t, 20)
	args, _ := json.Marshal(map[string]any{"path": path, "start_line": 10, "end_line": 3})
	_, err := ReadTool{}.Execute(context.Background(), args, Context{SessionDir: filepath.Dir(path)})
	if err == nil {
		t.Fatal("an inverted range must be refused, not reinterpreted")
	}
	// Both numbers have to come back, or the caller cannot see which argument
	// to fix; "inverted" names the problem.
	for _, want := range []string{"10", "3", "inverted"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
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
	if !strings.Contains(out, "start_line=N, end_line=M") {
		t.Error("map response omits how to ask for a range")
	}
	// It must be a map, not the source.
	if strings.Contains(out, "return 7\n") {
		t.Error("map response leaked file contents")
	}
	if !strings.Contains(out, "func Fn7() int") {
		t.Errorf("map response missing declarations:\n%s", truncate(out))
	}
}

// The interception must never block a caller who said what they wanted.
func TestReadTool_LongFileWithRangeIsHonoured(t *testing.T) {
	path := longGoFile(t, 60)

	cases := map[string]map[string]any{
		"start/end range": {"start_line": 4, "end_line": 6},
		"offset paging":   {"offset": 3, "limit": 3},
		"explicit whole":  {"start_line": 1, "end_line": 5000},
		"limit only":      {"limit": 10},
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

// end_line without start_line used to be silently dropped, falling back to
// the default unranged window (from line 1, up to defaultReadLimit) with no
// sign the argument was ignored. It now defaults start_line to 1, so the
// range is honoured as "from the top through end_line".
func TestReadTool_EndLineWithoutStartLine(t *testing.T) {
	out := readWith(t, numbered(t, 20), map[string]any{"end_line": 3})

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

// A bare limit says how much to read, never which part, so it cannot serve as a
// range on a file the caller has never mapped. Before this, read(path,
// limit=5000) walked straight past the interception and returned the file — the
// same whole-file read the threshold exists to stop, reached through a
// different argument. The sibling case above pins the other half: a limit small
// enough to be a peek is still honoured.
func TestReadTool_LargeBareLimitStillReturnsMap(t *testing.T) {
	path := longGoFile(t, 60)

	out := readWith(t, path, map[string]any{"limit": 5000})

	if !strings.Contains(out, "too long to read in full") {
		t.Errorf("a bare limit past the threshold bypassed the map guard:\n%s", truncate(out))
	}
}

// The boundary itself, both sides. At or under the threshold a bare limit is a
// peek that costs no more context than a short file — which is read whole
// already — so refusing it would contradict the threshold it is measured
// against. Past it, the caller has to say which part.
func TestReadTool_BareLimitBoundary(t *testing.T) {
	path := longGoFile(t, 60)

	cases := []struct {
		name   string
		limit  int
		mapped bool
	}{
		{"one under", rangedReadThreshold - 1, false},
		{"exactly at", rangedReadThreshold, false},
		{"one over", rangedReadThreshold + 1, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := readWith(t, path, map[string]any{"limit": c.limit})
			got := strings.Contains(out, "too long to read in full")
			if got != c.mapped {
				t.Errorf("limit=%d: mapped=%v, want %v:\n%s", c.limit, got, c.mapped, truncate(out))
			}
		})
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

// The description has to state the cap, or a caller passing limit=5000 gets the
// map back with no way to tell whether that was the rule or a malfunction.
func TestReadDescription_StatesTheBareLimitCap(t *testing.T) {
	desc := ReadTool{}.Description()
	for _, want := range []string{"200 lines", "which part"} {
		if !strings.Contains(desc, want) {
			t.Errorf("read description missing %q; the bare-limit cap is unstated", want)
		}
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
		"stale start_line": {"start_line": 50, "end_line": 80},
		"stale offset":     {"offset": 20},
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

// The per-line cap slices bytes, and a slice through a multi-byte character
// used to ship the invalid tail to the provider — corrupted content, from
// which an edit anchor can never be copied. The cut must land on a rune
// boundary.
func TestReadTool_LongLineCapKeepsValidUTF8(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "long.txt")
	// A multi-byte rune straddling the MaxLineLength boundary.
	mustWriteFile(t, path, strings.Repeat("a", MaxLineLength-1)+"→ tail\n")

	out := readWith(t, path, map[string]any{})

	if !utf8.ValidString(out) {
		t.Error("read output contains invalid UTF-8 — the line cap split a rune")
	}
	if !strings.Contains(out, lineTruncatedSuffix) {
		t.Errorf("the long line should be marked truncated:\n%q", out)
	}
}
