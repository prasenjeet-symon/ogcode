package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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
		if !strings.Contains(out, "\t"+want) {
			t.Errorf("window missing %s:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"\tline4", "\tline9"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("window included %s, so the range is off by one:\n%s", strings.TrimPrefix(unwanted, "\t"), out)
		}
	}
}

// A single-line range is the degenerate case of the inclusive rule and is what
// file_map emits for one-line declarations.
func TestReadTool_SingleLineRange(t *testing.T) {
	out := readWith(t, numbered(t, 20), map[string]any{"start_line": 12, "end_line": 12})

	if !strings.Contains(out, "\tline12") {
		t.Errorf("single-line range lost its line:\n%s", out)
	}
	if strings.Contains(out, "\tline11") || strings.Contains(out, "\tline13") {
		t.Errorf("single-line range returned neighbours:\n%s", out)
	}
}

// start_line without end_line reads from there to the default limit, so a
// caller can open a file at a declaration without knowing where it ends.
func TestReadTool_StartLineWithoutEndLine(t *testing.T) {
	out := readWith(t, numbered(t, 20), map[string]any{"start_line": 18})

	if !strings.Contains(out, "\tline18") || !strings.Contains(out, "\tline20") {
		t.Errorf("open-ended range lost lines:\n%s", out)
	}
	if strings.Contains(out, "\tline17") {
		t.Errorf("open-ended range started too early:\n%s", out)
	}
}

// An end_line past EOF clamps instead of erroring: file_map ranges can go stale
// after an edit, and truncating the read is far kinder than failing it.
func TestReadTool_EndLineBeyondEOFClamps(t *testing.T) {
	out := readWith(t, numbered(t, 20), map[string]any{"start_line": 19, "end_line": 500})

	if !strings.Contains(out, "\tline20") {
		t.Errorf("clamped read lost the last line:\n%s", out)
	}
}

// offset stays 0-based for paging. Both forms must keep working, since the
// truncation notice tells the agent to continue with offset.
func TestReadTool_OffsetRemains0Based(t *testing.T) {
	out := readWith(t, numbered(t, 20), map[string]any{"offset": 5, "limit": 2})

	if !strings.Contains(out, "\tline6") || !strings.Contains(out, "\tline7") {
		t.Errorf("offset=5 limit=2 should yield lines 6-7:\n%s", out)
	}
}

// An end_line below start_line is incoherent; falling back to the default
// window beats returning nothing.
func TestReadTool_InvertedRangeFallsBack(t *testing.T) {
	out := readWith(t, numbered(t, 20), map[string]any{"start_line": 10, "end_line": 3})

	if !strings.Contains(out, "\tline10") {
		t.Errorf("inverted range returned nothing usable:\n%s", out)
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
