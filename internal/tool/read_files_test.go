package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// readFilesWith runs one files=[...] call from dir. The entries are marshalled
// rather than decoded by hand so the test exercises the same JSON shape a model
// sends.
func readFilesWith(t *testing.T, dir string, entries ...map[string]any) (Result, error) {
	t.Helper()
	args, err := json.Marshal(map[string]any{"files": entries})
	if err != nil {
		t.Fatalf("marshal files args: %v", err)
	}
	return ReadTool{}.Execute(context.Background(), args, Context{SessionDir: dir})
}

func readFilesOK(t *testing.T, dir string, entries ...map[string]any) Result {
	t.Helper()
	res, err := readFilesWith(t, dir, entries...)
	if err != nil {
		t.Fatalf("files read: %v", err)
	}
	return res
}

// writeNumberedLines writes n lines whose text is "lineN", the same body
// numbered() builds, but at a caller-chosen path so several files can share one
// session directory.
func writeNumberedLines(t *testing.T, path string, n int) {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line%d\n", i)
	}
	mustWriteFile(t, path, b.String())
}

// A files call answers every named file, in the order given, each under its own
// heading.
func TestReadTool_FilesReturnsEveryFileInOrder(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "a.txt"), "alpha\n")
	mustWriteFile(t, filepath.Join(dir, "b.txt"), "beta\n")

	res := readFilesOK(t, dir,
		map[string]any{"path": "a.txt"},
		map[string]any{"path": "b.txt"},
	)

	if res.Title != "2 files" {
		t.Errorf("title = %q, want the file count", res.Title)
	}
	first, second := strings.Index(res.Output, "== a.txt =="), strings.Index(res.Output, "== b.txt ==")
	if first < 0 || second < 0 {
		t.Fatalf("both files should open with their own heading:\n%s", truncate(res.Output))
	}
	if first > second {
		t.Errorf("sections are out of order:\n%s", truncate(res.Output))
	}
	for _, want := range []string{"alpha", "beta"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("output dropped %q:\n%s", want, truncate(res.Output))
		}
	}
	if res.Truncated {
		t.Error("two short files should not report truncation")
	}
}

// Each entry carries its own window, so a post-grep fan-out over several files
// is one call and each section holds only the lines asked for.
func TestReadTool_FilesHonoursRangesPerFile(t *testing.T) {
	dir := t.TempDir()
	writeNumberedLines(t, filepath.Join(dir, "a.txt"), 10)
	writeNumberedLines(t, filepath.Join(dir, "b.txt"), 10)

	out := readFilesOK(t, dir,
		map[string]any{"path": "a.txt", "ranges": []map[string]any{{"start_line": 2, "end_line": 3}}},
		map[string]any{"path": "b.txt", "ranges": []map[string]any{{"start_line": 5, "end_line": 5}}},
	).Output

	for _, want := range []string{"a.txt:2-3", "b.txt:5-5"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing per-file window heading %q:\n%s", want, truncate(out))
		}
	}
	for _, want := range []string{
		fmt.Sprintf("%6d%sline2", 2, lineNumberSep),
		fmt.Sprintf("%6d%sline3", 3, lineNumberSep),
		fmt.Sprintf("%6d%sline5", 5, lineNumberSep),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing requested line %q:\n%s", want, truncate(out))
		}
	}
	if strings.Contains(out, fmt.Sprintf("%6d%sline1\n", 1, lineNumberSep)) {
		t.Errorf("a.txt returned a line outside its range:\n%s", truncate(out))
	}
	if strings.Contains(out, "a.txt:5-5") {
		t.Errorf("a window leaked into the other file's section:\n%s", truncate(out))
	}
}

// A range is an explicit request, so a long mappable file inside files is read
// rather than answered with its map — the interception is per entry, not a
// blanket on the call.
func TestReadTool_FilesRangeBypassesTheMap(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("package demo\n\n")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&b, "// Fn%d does a thing.\nfunc Fn%d() int {\n\treturn %d\n}\n\n", i, i, i)
	}
	mustWriteFile(t, filepath.Join(dir, "big.go"), b.String())

	out := readFilesOK(t, dir, map[string]any{
		"path":   "big.go",
		"ranges": []map[string]any{{"start_line": 3, "end_line": 4}},
	}).Output

	if strings.Contains(out, "too long to read in full") {
		t.Errorf("a ranged entry was intercepted by the map:\n%s", truncate(out))
	}
	if !strings.Contains(out, "big.go:3-4") || !strings.Contains(out, "func Fn0() int {") {
		t.Errorf("ranged entry did not return its window:\n%s", truncate(out))
	}
}

func TestReadTool_FilesAndPathAreExclusive(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "a.txt"), "alpha\n")

	args, _ := json.Marshal(map[string]any{
		"path":  "a.txt",
		"files": []map[string]any{{"path": "a.txt"}},
	})
	_, err := ReadTool{}.Execute(context.Background(), args, Context{SessionDir: dir})
	if err == nil {
		t.Fatal("path and files together were accepted")
	}
	if !strings.Contains(err.Error(), "not both") {
		t.Errorf("error should name the exclusivity, got: %v", err)
	}
}

func TestReadTool_FilesEmptyIsRefused(t *testing.T) {
	dir := t.TempDir()

	args, _ := json.Marshal(map[string]any{"files": []map[string]any{}})
	_, err := ReadTool{}.Execute(context.Background(), args, Context{SessionDir: dir})
	if err == nil {
		t.Fatal("an empty files array was accepted")
	}
	if !strings.Contains(err.Error(), "files is empty") {
		t.Errorf("error should name the empty array, got: %v", err)
	}
}

func TestReadTool_FilesRequiresOneForm(t *testing.T) {
	dir := t.TempDir()

	args, _ := json.Marshal(map[string]any{})
	_, err := ReadTool{}.Execute(context.Background(), args, Context{SessionDir: dir})
	if err == nil {
		t.Fatal("a read with no path and no files was accepted")
	}
	if !strings.Contains(err.Error(), "one of the two is required") {
		t.Errorf("error should name the two forms, got: %v", err)
	}
}

func TestReadTool_FilesTopLevelWindowRefused(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "a.txt"), "alpha\n")

	args, _ := json.Marshal(map[string]any{
		"files":  []map[string]any{{"path": "a.txt"}},
		"ranges": []map[string]any{{"start_line": 1, "end_line": 2}},
	})
	_, err := ReadTool{}.Execute(context.Background(), args, Context{SessionDir: dir})
	if err == nil {
		t.Fatal("a top-level window alongside files was accepted")
	}
	if !strings.Contains(err.Error(), "top-level window") {
		t.Errorf("error should say the window has no file to apply to, got: %v", err)
	}
}

// An entry-level start_line/end_line would otherwise be silently ignored by the
// decoder and turn a windowed read into a whole-file read. The refusal names the
// entry so a long list is fixable without bisecting it.
func TestReadTool_FilesEntryWindowPairRefused(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "a.txt"), "alpha\n")

	_, err := readFilesWith(t, dir, map[string]any{"path": "a.txt", "start_line": 1, "end_line": 2})
	if err == nil {
		t.Fatal("start_line/end_line on a files entry were accepted")
	}
	for _, want := range []string{"files[0]", "ranges"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

func TestReadTool_FilesEntryInvertedRangeNamesIndices(t *testing.T) {
	dir := t.TempDir()
	writeNumberedLines(t, filepath.Join(dir, "a.txt"), 20)

	_, err := readFilesWith(t, dir,
		map[string]any{"path": "a.txt", "ranges": []map[string]any{{"start_line": 3, "end_line": 5}}},
		map[string]any{"path": "a.txt", "ranges": []map[string]any{{"start_line": 10, "end_line": 3}}},
	)
	if err == nil {
		t.Fatal("an inverted entry range was accepted")
	}
	for _, want := range []string{"files[1]", "ranges[0]", "inverted"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

func TestReadTool_FilesEntryRangesRefusePaging(t *testing.T) {
	dir := t.TempDir()
	writeNumberedLines(t, filepath.Join(dir, "a.txt"), 20)

	_, err := readFilesWith(t, dir, map[string]any{
		"path":   "a.txt",
		"ranges": []map[string]any{{"start_line": 1, "end_line": 2}},
		"offset": 5,
	})
	if err == nil {
		t.Fatal("ranges combined with offset on an entry were accepted")
	}
	for _, want := range []string{"files[0]", "cannot be combined"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

// One stale or missing path must not discard the content the caller asked for
// alongside it: the failing entry becomes a section naming the error.
func TestReadTool_FilesPartialFailureServesTheRest(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "a.txt"), "alpha\n")
	mustWriteFile(t, filepath.Join(dir, "c.txt"), "gamma\n")

	res := readFilesOK(t, dir,
		map[string]any{"path": "a.txt"},
		map[string]any{"path": "missing.txt"},
		map[string]any{"path": "c.txt"},
	)

	for _, want := range []string{"alpha", "gamma", "== missing.txt ==", "no such file"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("output missing %q:\n%s", want, truncate(res.Output))
		}
	}
	if res.Truncated {
		t.Error("a missing file is not a truncation")
	}
	if res.Title != "3 files" {
		t.Errorf("title = %q, want the file count", res.Title)
	}
}

// A long mappable file is answered with its map even inside a files call, and
// the short file after it still arrives in the same result.
func TestReadTool_FilesMapSection(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("package demo\n\n")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&b, "// Fn%d does a thing.\nfunc Fn%d() int {\n\treturn %d\n}\n\n", i, i, i)
	}
	mustWriteFile(t, filepath.Join(dir, "big.go"), b.String())
	mustWriteFile(t, filepath.Join(dir, "small.txt"), "small content\n")

	out := readFilesOK(t, dir,
		map[string]any{"path": "big.go"},
		map[string]any{"path": "small.txt"},
	).Output

	if !strings.Contains(out, "too long to read in full") {
		t.Errorf("the long file should be answered with its map:\n%s", truncate(out))
	}
	if strings.Contains(out, "return 7") {
		t.Errorf("the map section leaked file content:\n%s", truncate(out))
	}
	if !strings.Contains(out, "small content") {
		t.Errorf("the short file after a map section was dropped:\n%s", truncate(out))
	}
}

// A files call returns every file it was given. The call used to share one
// byte/line budget across its sections, so a first long file could push the
// rest out of the result; nothing is budgeted now, and the whole call may run
// past the size every other tool is capped at.
func TestReadTool_FilesReturnsEveryFile(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 2100; i++ {
		fmt.Fprintf(&b, "xxxxxxxxxxxxxxxxxxxx%04d\n", i)
	}
	mustWriteFile(t, filepath.Join(dir, "long.txt"), b.String())
	mustWriteFile(t, filepath.Join(dir, "second.txt"), "second file body\n")

	res := readFilesOK(t, dir,
		map[string]any{"path": "long.txt"},
		map[string]any{"path": "second.txt"},
	)

	if !strings.Contains(res.Output, "second file body") {
		t.Errorf("the second file was dropped:\n%s", truncate(res.Output))
	}
	if strings.Contains(res.Output, "file(s) not shown") {
		t.Errorf("a file was withheld:\n%s", truncate(res.Output))
	}
	if res.Title != "2 files" {
		t.Errorf("title = %q, want the file count", res.Title)
	}
	if len(res.Output) <= MaxToolOutputBytes {
		t.Errorf("output is %d bytes — a files call should not be bounded at %d", len(res.Output), MaxToolOutputBytes)
	}
	if !res.Uncapped {
		t.Error("a files call must opt out of the loop's backstop")
	}
}

// Providers ignore the "integer" hint; the integral-float coercion has to reach
// inside files entries too.
func TestReadTool_FilesAcceptFloatLineNumbers(t *testing.T) {
	dir := t.TempDir()
	writeNumberedLines(t, filepath.Join(dir, "f.txt"), 20)

	res, err := ReadTool{}.Execute(context.Background(), json.RawMessage(
		`{"files":[{"path":"f.txt","ranges":[{"start_line":3.0,"end_line":4.0}]}]}`,
	), Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res.Output, "f.txt:3-4") {
		t.Errorf("integral floats in an entry range should decode as integers:\n%s", truncate(res.Output))
	}
}

// One entry is still the single-file shape, so its title stays the file's name
// rather than a count of one.
func TestReadTool_FilesSingleEntryKeepsTitle(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "a.txt"), "alpha\n")

	res := readFilesOK(t, dir, map[string]any{"path": "a.txt"})
	if res.Title != "a.txt" {
		t.Errorf("title = %q, want the single file's name", res.Title)
	}
}

// The description is the only place a caller learns the files form exists
// before making the call, so it has to name the argument, the section heading
// and the exclusivity rule.
func TestReadDescription_AdvertisesFiles(t *testing.T) {
	desc := ReadTool{}.Description()
	for _, want := range []string{"files", "== path ==", "exclusive"} {
		if !strings.Contains(desc, want) {
			t.Errorf("read description missing %q, so the multi-file form is unstated", want)
		}
	}
}

// The opening sentence is where a caller decides the shape of the call. The
// files form used to sit mid-paragraph, after the single-file prose, so a model
// skimming the contract still spent one call per path — the shape has to be in
// the opening, not merely present.
func TestReadDescription_LeadsWithTheMultiFileShape(t *testing.T) {
	desc := ReadTool{}.Description()
	head := desc
	if i := strings.Index(desc, "Each line comes back"); i >= 0 {
		head = desc[:i]
	}

	for _, want := range []string{"files=[", "ONE call"} {
		if !strings.Contains(head, want) {
			t.Errorf("read description does not lead with the multi-file form: missing %q in its opening", want)
		}
	}
}
