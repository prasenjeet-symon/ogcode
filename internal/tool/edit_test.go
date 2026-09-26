package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// editWith runs one edit. For readability most tests below describe a single
// change with flat old_string/new_string keys; this wraps those into the one
// form the tool actually takes. Tests that care about the wire shape itself
// build their JSON directly — see TestEditTool_OnlyTakesTheEditsArray.
func editWith(t *testing.T, dir string, params map[string]any) (Result, error) {
	t.Helper()
	args, _ := json.Marshal(asEditsForm(params))
	return EditTool{}.Execute(context.Background(), args, Context{SessionDir: dir})
}

// asEditsForm turns {path, old_string, …} into {path, edits:[{old_string, …}]}.
// Params already using edits pass through untouched.
func asEditsForm(params map[string]any) map[string]any {
	if _, ok := params["edits"]; ok {
		return params
	}
	hunk := map[string]any{}
	out := map[string]any{}
	for k, v := range params {
		switch k {
		case "old_string", "new_string", "replace_all", "expected_count":
			hunk[k] = v
		default:
			out[k] = v
		}
	}
	if len(hunk) == 0 {
		return params
	}
	out["edits"] = []map[string]any{hunk}
	return out
}

func TestEditTool_UniqueMatchReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	mustWriteFile(t, path, "hello world\n")

	res, err := editWith(t, dir, map[string]any{"path": path, "old_string": "world", "new_string": "there"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "hello there\n" {
		t.Errorf("file content = %q, want %q", got, "hello there\n")
	}
	if !strings.Contains(res.Output, "replaced 1 occurrence") {
		t.Errorf("output missing replacement summary: %q", res.Output)
	}
}

func TestEditTool_NotFoundErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	mustWriteFile(t, path, "hello world\n")

	_, err := editWith(t, dir, map[string]any{"path": path, "old_string": "missing", "new_string": "x"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected a not-found error, got %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "hello world\n" {
		t.Errorf("file should be unchanged after a failed edit, got %q", got)
	}
}

func TestEditTool_AmbiguousMatchErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	mustWriteFile(t, path, "aa aa aa\n")

	_, err := editWith(t, dir, map[string]any{"path": path, "old_string": "aa", "new_string": "bb"})
	if err == nil || !strings.Contains(err.Error(), "appears 3 times") {
		t.Errorf("expected an ambiguous-match error naming the count, got %v", err)
	}
}

// Regression test: an empty old_string used to slip past validation. Go's
// strings.Count treats "" as occurring once between every rune, so on a
// non-empty file it produced a confusing "appears N times" error, and on an
// empty file (where that count is exactly 1) it silently "succeeded",
// inserting new_string into a file that was never actually matched against.
func TestEditTool_EmptyOldStringIsRejected(t *testing.T) {
	dir := t.TempDir()

	t.Run("non-empty file", func(t *testing.T) {
		path := filepath.Join(dir, "nonempty.txt")
		mustWriteFile(t, path, "hello world\n")

		_, err := editWith(t, dir, map[string]any{"path": path, "old_string": "", "new_string": "x"})
		if err == nil || !strings.Contains(err.Error(), "old_string must not be empty") {
			t.Errorf("expected the empty-old_string error, got %v", err)
		}
		if got, _ := os.ReadFile(path); string(got) != "hello world\n" {
			t.Errorf("file should be unchanged, got %q", got)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		path := filepath.Join(dir, "empty.txt")
		mustWriteFile(t, path, "")

		_, err := editWith(t, dir, map[string]any{"path": path, "old_string": "", "new_string": "hello"})
		if err == nil || !strings.Contains(err.Error(), "old_string must not be empty") {
			t.Errorf("expected the empty-old_string error, got %v", err)
		}
		if got, _ := os.ReadFile(path); string(got) != "" {
			t.Errorf("empty file should stay empty, got %q", got)
		}
	})
}

func TestEditTool_MissingFileErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.txt")

	_, err := editWith(t, dir, map[string]any{"path": path, "old_string": "a", "new_string": "b"})
	if err == nil {
		t.Error("expected an error editing a nonexistent file, got nil")
	}
}

// Same guarantee for edit: it writes via a temp file and a rename, so the
// file's mode has to survive the swap.
func TestEditTool_PreservesExecutableBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits don't apply on windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")
	mustWriteFile(t, path, "#!/bin/sh\necho hi\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := editWith(t, dir, map[string]any{"path": path, "old_string": "hi", "new_string": "bye"}); err != nil {
		t.Fatalf("execute: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("mode after edit = %v, want 0755 — the script is no longer executable", got)
	}
}

// Editing a file reached through a symlink must change what the link points at
// and leave the link a link, as the in-place write did. Renaming over the link
// itself would turn it into a regular file and break the layout the repo set up.
func TestEditTool_WritesThroughSymlinkWithoutReplacingIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevation on windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "real.txt")
	link := filepath.Join(dir, "link.txt")
	mustWriteFile(t, target, "hello world\n")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if _, err := editWith(t, dir, map[string]any{"path": link, "old_string": "world", "new_string": "there"}); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if got, _ := os.ReadFile(target); string(got) != "hello there\n" {
		t.Errorf("link's target = %q, want the edited content", got)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a regular file")
	}
}

// The batching prompt promises that same-file edits in one block are safe when
// their anchors don't overlap, because the per-path lock (pathlock.go)
// serializes them and the second edit re-reads fresh content. This pins the
// whole contract: every edit applies, none is lost, and the final content is
// the sum of all three replacements. Without the lock two of these goroutines
// would read the same base content and the atomic rename of the last writer
// would silently drop the other two edits.
func TestEditTool_ConcurrentDisjointEditsAllApply(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	mustWriteFile(t, path, "alpha beta gamma\n")

	edits := [][2]string{{"alpha", "ALPHA"}, {"beta", "BETA"}, {"gamma", "GAMMA"}}
	var wg sync.WaitGroup
	for _, e := range edits {
		wg.Add(1)
		go func(oldStr, newStr string) {
			defer wg.Done()
			if _, err := editWith(t, dir, map[string]any{"path": path, "old_string": oldStr, "new_string": newStr}); err != nil {
				t.Errorf("edit %q: %v", oldStr, err)
			}
		}(e[0], e[1])
	}
	wg.Wait()

	got, _ := os.ReadFile(path)
	if string(got) != "ALPHA BETA GAMMA\n" {
		t.Errorf("content after concurrent edits = %q, want %q — an update was lost", got, "ALPHA BETA GAMMA\n")
	}
}

// The other half of the same contract: overlapping anchors do not corrupt the
// file or produce a mangled merge. Exactly one edit applies; the losers fail
// cleanly with "old_string not found" (their anchor was consumed by the winner)
// or "appears N times" if the winner's replacement re-introduced the anchor —
// either way the file stays well-formed and the error names the reason.
func TestEditTool_ConcurrentOverlappingEditsFailCleanly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	mustWriteFile(t, path, "hello world\n")

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, e := range [][2]string{{"hello", "goodbye"}, {"hello", "hey"}} {
		wg.Add(1)
		go func(i int, oldStr, newStr string) {
			defer wg.Done()
			_, errs[i] = editWith(t, dir, map[string]any{"path": path, "old_string": oldStr, "new_string": newStr})
		}(i, e[0], e[1])
	}
	wg.Wait()

	// Both results are recorded; whichever won, exactly one content is on disk
	// and it is one of the two complete replacements — never a mixture.
	got, _ := os.ReadFile(path)
	if string(got) != "goodbye world\n" && string(got) != "hey world\n" {
		t.Errorf("content after overlapping edits = %q, want one of the two clean replacements", got)
	}
	notFound, dup := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
		case strings.Contains(err.Error(), "not found"):
			notFound++
		case strings.Contains(err.Error(), "appears"):
			dup++
		default:
			t.Errorf("unexpected error shape: %v", err)
		}
	}
	if notFound+dup == 0 {
		t.Error("no loser reported a clean failure — both edits reported success")
	}
}

// TestEditTool_AmbiguousErrorGuidesRecovery covers what the ambiguity error has
// to carry. The count alone leaves the caller guessing which occurrences it hit
// and unaware that changing all of them is even possible — which is what turned
// one ambiguous edit into three tool calls in practice.
func TestEditTool_AmbiguousErrorGuidesRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.yml")
	mustWriteFile(t, path, "one\ndup\ntwo\ndup\nthree\n")

	_, err := editWith(t, dir, map[string]any{"path": path, "old_string": "dup", "new_string": "x"})
	if err == nil {
		t.Fatal("expected an ambiguity error")
	}
	// The line numbers matter, but so does the context above each one: the two
	// matches are byte-identical, so only their surroundings can tell them apart.
	for _, want := range []string{"appears 2 times", "line 2", "line 4", `after "one"`, `after "two"`, "replace_all"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

// TestEditTool_ReplaceAll covers the deliberate case: the same text genuinely
// should change everywhere, and the result says how many did.
func TestEditTool_ReplaceAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	mustWriteFile(t, path, "aa bb aa bb aa\n")

	res, err := editWith(t, dir, map[string]any{
		"path": path, "old_string": "aa", "new_string": "zz", "replace_all": true,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "zz bb zz bb zz\n" {
		t.Errorf("content = %q, want all occurrences replaced", got)
	}
	if !strings.Contains(res.Output, "replaced 3 occurrences") {
		t.Errorf("output should report the count, got %q", res.Output)
	}
}

// TestEditTool_ReplaceAllStillRequiresAMatch checks replace_all does not soften
// the not-found case into a silent success.
func TestEditTool_ReplaceAllStillRequiresAMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	mustWriteFile(t, path, "hello\n")

	_, err := editWith(t, dir, map[string]any{
		"path": path, "old_string": "absent", "new_string": "x", "replace_all": true,
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected a not-found error, got %v", err)
	}
}

// TestEditTool_ReplaceAllDefaultsOff pins the default. A caller that omits the
// flag must still get the uniqueness guarantee, or every existing edit silently
// changes meaning.
func TestEditTool_ReplaceAllDefaultsOff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	mustWriteFile(t, path, "aa aa\n")

	_, err := editWith(t, dir, map[string]any{"path": path, "old_string": "aa", "new_string": "z"})
	if err == nil || !strings.Contains(err.Error(), "unique match") {
		t.Errorf("omitting replace_all must still require uniqueness, got %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "aa aa\n" {
		t.Errorf("file should be unchanged, got %q", got)
	}
}

// TestEditTool_NotFoundHintsWhitespace covers the miss that is really an
// indentation mismatch — the anchor is right and only the leading whitespace is
// wrong. A bare "not found" sends the caller hunting for absent text instead.
func TestEditTool_NotFoundHintsWhitespace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.go")
	mustWriteFile(t, path, "func main() {\n\tfmt.Println(\"hi\")\n}\n")

	// Same code, spaces instead of the file's tab.
	_, err := editWith(t, dir, map[string]any{
		"path": path, "old_string": "    fmt.Println(\"hi\")", "new_string": "    fmt.Println(\"bye\")",
	})
	if err == nil {
		t.Fatal("expected a not-found error")
	}
	if !strings.Contains(err.Error(), "indentation") {
		t.Errorf("error should point at whitespace, got: %v", err)
	}
	// Naming the problem is not enough — knowing the whitespace is wrong does not
	// say what it should be. The file's own bytes have to come back, tab intact,
	// or the caller spends another round trip guessing again.
	if !strings.Contains(err.Error(), "\tfmt.Println(\"hi\")") {
		t.Errorf("error should quote the file's exact text, got: %v", err)
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error should say where, got: %v", err)
	}

	// Text that genuinely is not there gets no such hint, so the hint stays a
	// signal rather than noise on every miss.
	_, err = editWith(t, dir, map[string]any{"path": path, "old_string": "nowhere", "new_string": "x"})
	if err == nil || strings.Contains(err.Error(), "indentation") {
		t.Errorf("a genuine miss should not blame whitespace, got: %v", err)
	}
}

// TestEditTool_RepeatedCIStep reproduces the case that prompted this: the same
// "Build web UI" step in three jobs of a release workflow, where the two-line
// form matches only the two jobs that lack a "shell:" line between them. Both
// recoveries the error offers have to actually work.
func TestEditTool_RepeatedCIStep(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "release.yml")
	step := "      - name: Build web UI\n        run: cd web && npm run build\n"
	workflow := "jobs:\n  build-linux:\n" + step +
		"  build-macos:\n" + step +
		"  build-windows:\n      - name: Build web UI\n        shell: bash\n        run: cd web && npm run build\n"
	mustWriteFile(t, path, workflow)

	_, err := editWith(t, dir, map[string]any{"path": path, "old_string": step, "new_string": ""})
	if err == nil || !strings.Contains(err.Error(), "appears 2 times") {
		t.Fatalf("expected the two-job ambiguity, got %v", err)
	}

	// Recovery 1: extend the anchor with the job name above it.
	if _, err := editWith(t, dir, map[string]any{
		"path": path, "old_string": "  build-macos:\n" + step, "new_string": "  build-macos:\n",
	}); err != nil {
		t.Fatalf("extending the anchor should disambiguate: %v", err)
	}

	// Recovery 2: the remaining copy is now unique, and replace_all would have
	// taken both in one call.
	res, err := editWith(t, dir, map[string]any{
		"path": path, "old_string": step, "new_string": "", "replace_all": true,
	})
	if err != nil {
		t.Fatalf("replace_all should apply: %v", err)
	}
	if !strings.Contains(res.Output, "replaced 1 occurrence") {
		t.Errorf("one copy should remain by now, got %q", res.Output)
	}
	got, _ := os.ReadFile(path)
	if strings.Count(string(got), "Build web UI") != 1 {
		t.Errorf("only the windows variant should survive, got:\n%s", got)
	}
}

// TestEditTool_MultiEditApplies covers the ordinary multi-hunk case: several
// changes to one file in one call, applied in order.
func TestEditTool_MultiEditApplies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.go")
	mustWriteFile(t, path, "package a\n\nfunc one() {}\nfunc two() {}\nfunc three() {}\n")

	res, err := editWith(t, dir, map[string]any{
		"path": path,
		"edits": []map[string]any{
			{"old_string": "func one() {}", "new_string": "func uno() {}"},
			{"old_string": "func two() {}", "new_string": "func dos() {}"},
			{"old_string": "func three() {}", "new_string": "func tres() {}"},
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, _ := os.ReadFile(path)
	want := "package a\n\nfunc uno() {}\nfunc dos() {}\nfunc tres() {}\n"
	if string(got) != want {
		t.Errorf("content = %q, want %q", got, want)
	}
	if !strings.Contains(res.Output, "3 edits") {
		t.Errorf("output should report the edit count, got %q", res.Output)
	}
}

// TestEditTool_MultiEditIsAllOrNothing is the reason this exists. A refactor
// whose fourth change fails must leave the file exactly as it was — not
// three-quarters rewritten into a state nobody asked for and nobody is looking at.
func TestEditTool_MultiEditIsAllOrNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.go")
	original := "package a\n\nfunc one() {}\nfunc two() {}\n"
	mustWriteFile(t, path, original)

	_, err := editWith(t, dir, map[string]any{
		"path": path,
		"edits": []map[string]any{
			{"old_string": "func one() {}", "new_string": "func uno() {}"},
			{"old_string": "func two() {}", "new_string": "func dos() {}"},
			{"old_string": "func absent() {}", "new_string": "boom"},
		},
	})
	if err == nil {
		t.Fatal("expected the third hunk to fail")
	}
	// The error has to name which hunk, or a caller sending six edits learns
	// only that something somewhere went wrong.
	for _, want := range []string{"edits[2]", "no changes were written", "not found"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
	if got, _ := os.ReadFile(path); string(got) != original {
		t.Errorf("file must be untouched after a failed multi-edit, got %q", got)
	}
}

// TestEditTool_MultiEditSequential pins that a later hunk sees the result of an
// earlier one, which is what lets a call fix a line and then fix its caller.
func TestEditTool_MultiEditSequential(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	mustWriteFile(t, path, "alpha\n")

	if _, err := editWith(t, dir, map[string]any{
		"path": path,
		"edits": []map[string]any{
			{"old_string": "alpha", "new_string": "beta"},
			{"old_string": "beta", "new_string": "gamma"},
		},
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "gamma\n" {
		t.Errorf("content = %q, want the second hunk applied to the first's result", got)
	}
}

func TestEditTool_ExpectedCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	mustWriteFile(t, path, "x x x x\n")

	_, err := editWith(t, dir, map[string]any{
		"path": path, "old_string": "x", "new_string": "y",
		"replace_all": true, "expected_count": 3,
	})
	if err == nil || !strings.Contains(err.Error(), "expected_count is 3") {
		t.Errorf("expected a count-assertion failure, got %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "x x x x\n" {
		t.Errorf("file should be unchanged after a failed assertion, got %q", got)
	}

	// The honest count goes through.
	if _, err := editWith(t, dir, map[string]any{
		"path": path, "old_string": "x", "new_string": "y",
		"replace_all": true, "expected_count": 4,
	}); err != nil {
		t.Fatalf("a correct expected_count should apply: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "y y y y\n" {
		t.Errorf("content = %q, want all replaced", got)
	}
}

// TestEditTool_RepeatedCIStepInOneCall is the case that started this, done the
// way the tool now allows: one call, three jobs, all-or-nothing.
func TestEditTool_RepeatedCIStepInOneCall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "release.yml")
	step := "      - name: Build web UI\n        run: cd web && npm run build\n"
	mustWriteFile(t, path, "jobs:\n  build-linux:\n"+step+"  build-macos:\n"+step)

	res, err := editWith(t, dir, map[string]any{
		"path": path,
		"edits": []map[string]any{
			{"old_string": step, "new_string": "", "replace_all": true, "expected_count": 2},
			{"old_string": "jobs:\n", "new_string": "jobs:\n  build-web:\n    runs-on: ubuntu-latest\n"},
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "Build web UI") {
		t.Errorf("every copy of the step should be gone, got:\n%s", got)
	}
	if !strings.Contains(string(got), "build-web:") {
		t.Errorf("the new job should be present, got:\n%s", got)
	}
	if !strings.Contains(res.Output, "2 edits") {
		t.Errorf("output should report both edits, got %q", res.Output)
	}
}

// TestEditTool_OmittedNewStringRejected is the difference between a caller who
// means "delete this" and one who forgot the replacement. Nothing upstream can
// tell them apart — DecodeArgs is a plain json.Unmarshal, so the schema's
// "required" is advice to the model, not a check, and a missing field arrives as
// the zero value. Read as "", it deletes working code and reports success.
func TestEditTool_OmittedNewStringRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.go")
	original := "func important() { doWork() }\n"
	mustWriteFile(t, path, original)

	raw := json.RawMessage(`{"path":` + strconv.Quote(path) + `,"edits":[{"old_string":"doWork()"}]}`)
	_, err := EditTool{}.Execute(context.Background(), raw, Context{SessionDir: dir})
	if err == nil {
		t.Fatal("omitting new_string must not be read as a deletion")
	}
	for _, want := range []string{"edits[0]", "new_string is missing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
	if got, _ := os.ReadFile(path); string(got) != original {
		t.Errorf("file must be untouched, got %q", got)
	}
}

// TestEditTool_ExplicitEmptyNewStringDeletes is the other half of that contract.
// Deleting by replacing with "" is legitimate and common — removing a step from a
// workflow is exactly it — so the stricter check must not cost it.
func TestEditTool_ExplicitEmptyNewStringDeletes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	mustWriteFile(t, path, "keep\ndrop\nkeep\n")

	raw := json.RawMessage(`{"path":` + strconv.Quote(path) + `,"edits":[{"old_string":"drop\n","new_string":""}]}`)
	if _, err := (EditTool{}).Execute(context.Background(), raw, Context{SessionDir: dir}); err != nil {
		t.Fatalf("an explicit empty new_string should delete: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "keep\nkeep\n" {
		t.Errorf("content = %q, want the line deleted", got)
	}
}

func TestEditTool_NotFoundHintQuotesExactBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sqlite.go")
	body := "\tif _, err := d.Exec(\"PRAGMA busy_timeout = 30000\"); err != nil {\n\t\td.Close()\n"
	mustWriteFile(t, path, "func open() error {\n"+body+"\t}\n}\n")

	// The same block with spaces where the file has tabs.
	spaced := strings.ReplaceAll(body, "\t", "    ")
	_, err := editWith(t, dir, map[string]any{
		"path": path, "old_string": strings.TrimSuffix(spaced, "\n"), "new_string": "x",
	})
	if err == nil {
		t.Fatal("expected a not-found error")
	}

	// The quoted region must be usable as-is. Recovering it from the message and
	// re-issuing the edit is exactly what a caller would do next.
	msg := err.Error()
	start := strings.Index(msg, "The file has:\n")
	if start < 0 {
		t.Fatalf("error did not quote the file: %v", err)
	}
	quoted := msg[start+len("The file has:\n"):]
	quoted = strings.TrimSuffix(quoted, "\nSend that verbatim as old_string.")
	if quoted != strings.TrimSuffix(body, "\n") {
		t.Fatalf("quoted text is not the file's bytes:\n got %q\nwant %q", quoted, strings.TrimSuffix(body, "\n"))
	}
	if _, err := editWith(t, dir, map[string]any{
		"path": path, "old_string": quoted, "new_string": "\t\treturn nil",
	}); err != nil {
		t.Errorf("the quoted text should apply as an anchor: %v", err)
	}
}

// TestEditTool_NotFoundHintAmbiguous covers the case where quoting would be
// misleading: several regions match once indentation is ignored, so there is no
// single answer to hand back and the caller is pointed at the candidates instead.
func TestEditTool_NotFoundHintAmbiguous(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.go")
	mustWriteFile(t, path, "func a() {\n\tdo()\n}\nfunc b() {\n\t\tdo()\n}\n")

	_, err := editWith(t, dir, map[string]any{
		"path": path, "old_string": "      do()", "new_string": "done()",
	})
	if err == nil {
		t.Fatal("expected a not-found error")
	}
	if !strings.Contains(err.Error(), "matches 2 places") {
		t.Errorf("error should report the count, got: %v", err)
	}
	if !strings.Contains(err.Error(), "lines 2, 5") {
		t.Errorf("error should name the candidate lines, got: %v", err)
	}
	if strings.Contains(err.Error(), "Send that verbatim") {
		t.Error("must not offer a single answer when several regions match")
	}
}

// TestEditTool_OnlyTakesTheEditsArray pins the wire contract directly, without
// the readability shim the other tests use.
//
// This tool used to accept a top-level old_string/new_string as well as the
// array, and callers routinely sent both — the flat pair out of habit, the array
// because the tool asked for it — producing a call that could be read two ways
// and had to be refused. Documenting the exclusivity did not stop it. There is
// now one form, and a caller reaching for the old one is told exactly what to
// send rather than left to guess.
func TestEditTool_OnlyTakesTheEditsArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "version.go")
	original := "import (\n\t\"fmt\"\n\t\"github.com/spf13/cobra\"\n)\n"
	run := func(body string) (Result, error) {
		mustWriteFile(t, path, original)
		raw := json.RawMessage(`{"path":` + strconv.Quote(path) + `,` + body + `}`)
		return EditTool{}.Execute(context.Background(), raw, Context{SessionDir: dir})
	}

	t.Run("the array applies", func(t *testing.T) {
		if _, err := run(`"edits":[{"old_string":"\t\"fmt\"","new_string":"\t\"fmt\"\n\t\"internal/version\""}]`); err != nil {
			t.Fatalf("the single supported form should apply: %v", err)
		}
		if got, _ := os.ReadFile(path); !strings.Contains(string(got), "internal/version") {
			t.Errorf("edit did not apply: %s", got)
		}
	})

	// The flat shape is refused whether or not an array came with it, so a
	// caller can never have half of its intent silently dropped.
	for name, body := range map[string]string{
		"flat alone": `"old_string":"\t\"fmt\"","new_string":"x"`,
		"flat beside an array": `"old_string":"\t\"fmt\"","new_string":"x",` +
			`"edits":[{"old_string":"cobra","new_string":"y"}]`,
		"flat with only new_string": `"new_string":"x"`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := run(body)
			if err == nil {
				t.Fatal("the flat form must be refused")
			}
			// The message has to carry the shape to send, or the caller retries
			// the same thing — which is how the old dual form kept failing.
			for _, want := range []string{`"edits"`, "old_string", "new_string", "one entry per change"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error should mention %q, got: %v", want, err)
				}
			}
			if got, _ := os.ReadFile(path); string(got) != original {
				t.Errorf("file must be untouched, got %q", got)
			}
		})
	}

	t.Run("an empty array is refused", func(t *testing.T) {
		_, err := run(`"edits":[]`)
		if err == nil || !strings.Contains(err.Error(), "at least one entry") {
			t.Errorf("expected an empty-array rejection, got %v", err)
		}
	})
}

// TestEditTool_StringifiedEditsArrayIsExplained covers the shape weak models
// emit for an array param: the hunk list arrives as a JSON string holding the
// array rather than the array itself. The decoder's own message
// ("cannot unmarshal string into Go struct field .edits") names neither the fix
// nor the field as written, so the caller has to be told what to send instead.
func TestEditTool_StringifiedEditsArrayIsExplained(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	original := "hello world\n"
	mustWriteFile(t, path, original)

	// The hunk list as a string, escaped the way a caller double-encoding it
	// would: the inner array is JSON text, not a second array.
	inner := `[{"old_string":"world","new_string":"there"}]`
	raw := json.RawMessage(`{"path":` + strconv.Quote(path) + `,"edits":` + strconv.Quote(inner) + `}`)

	_, err := EditTool{}.Execute(context.Background(), raw, Context{SessionDir: dir})
	if err == nil {
		t.Fatal("a stringified edits array must be refused")
	}
	for _, want := range []string{"string containing a JSON array", `"edits"`, "old_string", "one entry per change"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
	if got, _ := os.ReadFile(path); string(got) != original {
		t.Errorf("file must be untouched, got %q", got)
	}
}

// TestEditTool_NoOpEditIsRejected covers an edit that finds its anchor and
// changes nothing.
//
// Replacing text with itself used to look identical to real work: the anchor
// matched, identical bytes were written, and the result said "replaced 1
// occurrence". A caller told its change landed, then seeing the file unchanged,
// can only send it again — which in practice produced a run of successful edits
// that moved nothing at all.
func TestEditTool_NoOpEditIsRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "indexing.go")
	original := "func index() error {\n\treturn nil\n}\n"
	mustWriteFile(t, path, original)

	_, err := editWith(t, dir, map[string]any{
		"path": path, "old_string": "\treturn nil", "new_string": "\treturn nil",
	})
	if err == nil {
		t.Fatal("an edit that changes nothing must not report success")
	}
	if !strings.Contains(err.Error(), "identical to old_string") {
		t.Errorf("error should say why, got: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != original {
		t.Errorf("file should be untouched, got %q", got)
	}
}

// TestEditTool_CancellingHunksRejected is the same lie spread across entries:
// each hunk resolves, and together they write the file back byte-identical.
func TestEditTool_CancellingHunksRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	original := "alpha\n"
	mustWriteFile(t, path, original)

	_, err := editWith(t, dir, map[string]any{
		"path": path,
		"edits": []map[string]any{
			{"old_string": "alpha", "new_string": "beta"},
			{"old_string": "beta", "new_string": "alpha"},
		},
	})
	if err == nil {
		t.Fatal("hunks that cancel out must not report success")
	}
	if !strings.Contains(err.Error(), "cancel out") {
		t.Errorf("error should name the cause, got: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != original {
		t.Errorf("file should be untouched, got %q", got)
	}
}

// TestEditTool_RealChangeStillApplies guards the boundary: the new checks must
// reject only edits that achieve nothing, never a genuine one that happens to
// reuse some of its own text.
func TestEditTool_RealChangeStillApplies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.go")
	mustWriteFile(t, path, "import (\n\t\"fmt\"\n)\n")

	if _, err := editWith(t, dir, map[string]any{
		"path": path, "old_string": "\t\"fmt\"", "new_string": "\t\"fmt\"\n\t\"os\"",
	}); err != nil {
		t.Fatalf("an additive edit should apply: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "import (\n\t\"fmt\"\n\t\"os\"\n)\n" {
		t.Errorf("content = %q", got)
	}
}

// Models emit \n; a CRLF file made every multi-line anchor a guaranteed miss,
// since a bare \n cannot occur where every newline follows a \r. In a
// uniformly-CRLF file the hunk is adapted to the file's own endings, so the
// natural LF form simply works — and the file stays CRLF throughout.
func TestEditTool_CRLFFileTakesLFAnchor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crlf.go")
	mustWriteFile(t, path, "package x\r\n\r\nfunc A() {\r\n\treturn\r\n}\r\n")

	if _, err := editWith(t, dir, map[string]any{
		"path":       path,
		"old_string": "func A() {\n\treturn\n}",
		"new_string": "func A() {\n\treturn // done\n}",
	}); err != nil {
		t.Fatalf("an LF anchor should match a uniformly-CRLF file: %v", err)
	}

	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "// done") {
		t.Fatalf("edit did not apply:\n%q", got)
	}
	if strings.Contains(strings.ReplaceAll(string(got), "\r\n", ""), "\n") {
		t.Errorf("edit introduced bare LF endings into a CRLF file:\n%q", got)
	}
}

// The other half of the same damage: a single-line anchor DID match in a CRLF
// file, so a multi-line LF new_string spliced LF lines into it and left mixed
// endings that nothing flagged. The replacement must arrive in the file's own
// endings too.
func TestEditTool_CRLFFileKeepsItsEndingsOnInsertion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crlf.go")
	mustWriteFile(t, path, "func A() {\r\n\treturn\r\n}\r\n")

	if _, err := editWith(t, dir, map[string]any{
		"path":       path,
		"old_string": "\treturn",
		"new_string": "\tif ok {\n\t\treturn\n\t}",
	}); err != nil {
		t.Fatalf("single-line anchor should still apply: %v", err)
	}

	got, _ := os.ReadFile(path)
	if strings.Contains(strings.ReplaceAll(string(got), "\r\n", ""), "\n") {
		t.Errorf("multi-line new_string left bare LF endings in a CRLF file:\n%q", got)
	}
}

// A mixed-endings file has no unambiguous convention to adapt a hunk to — an
// LF anchor there may be a deliberate reference to one of its LF lines,
// possibly the caller fixing the endings themselves. Hunks apply exactly as
// sent.
func TestEditTool_MixedEndingsFileIsNotSecondGuessed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mixed.txt")
	mustWriteFile(t, path, "a\nb\r\nc\n")

	if _, err := editWith(t, dir, map[string]any{
		"path": path, "old_string": "a\nb", "new_string": "a2\nb2",
	}); err != nil {
		t.Fatalf("a literal LF match in a mixed file should apply: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "a2\nb2\r\nc\n" {
		t.Errorf("content = %q, want the LF lines edited and the CRLF line untouched", got)
	}
}

// An LF old_string with a CRLF new_string in a uniformly-CRLF file is a no-op
// wearing a disguise: under the file's endings both sides are the same bytes.
// It must be caught like any other no-op, with a message that explains the
// endings — to the caller the two strings look different, and a bare
// "identical" would read as a tool fault.
func TestEditTool_CRLFNoOpDetected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crlf.txt")
	original := "alpha\r\nbeta\r\n"
	mustWriteFile(t, path, original)

	_, err := editWith(t, dir, map[string]any{
		"path": path, "old_string": "alpha\nbeta", "new_string": "alpha\r\nbeta",
	})
	if err == nil {
		t.Fatal("an endings-only rewrite of a CRLF file changes nothing and must say so")
	}
	if !strings.Contains(err.Error(), "CRLF") {
		t.Errorf("error should explain the endings, got: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != original {
		t.Errorf("file should be untouched, got %q", got)
	}
}

// In a mixed-endings file (where no adaptation happens) a CRLF region missed
// by an LF anchor must be diagnosed as a line-endings problem. The hint used
// to assert "only the leading whitespace is wrong" unconditionally, sending
// the caller hunting through indentation that was right all along while the
// real difference sat invisibly at the end of every line.
func TestEditTool_NotFoundHintNamesLineEndings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mixed.go")
	// The leading LF-ended line makes the file mixed, so the CRLF region below
	// is matched as sent rather than adapted.
	mustWriteFile(t, path, "// header\nfunc A() {\r\n\tdo()\r\n}\r\n")

	_, err := editWith(t, dir, map[string]any{
		"path": path, "old_string": "func A() {\n\tdo()\n}", "new_string": "func A() {\n\tdone()\n}",
	})
	if err == nil {
		t.Fatal("expected a not-found error")
	}
	if !strings.Contains(err.Error(), "CRLF") {
		t.Errorf("hint should name the line endings, got: %v", err)
	}
	if strings.Contains(err.Error(), "only the leading whitespace is wrong") {
		t.Errorf("hint must not blame indentation for an endings mismatch, got: %v", err)
	}
}

// When the matched region is too long to quote whole, the excerpt ends in a
// cut marker — and the instruction must switch from "send that verbatim" to a
// re-read, or a literal-minded caller anchors on the marker itself.
func TestEditTool_OverlongRegionHintSaysReRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	var fileLines, anchor []string
	for i := 0; i < 30; i++ {
		line := strings.Repeat("x", 100) + strconv.Itoa(i)
		fileLines = append(fileLines, "\t"+line)
		anchor = append(anchor, line) // same lines, indentation lost
	}
	mustWriteFile(t, path, strings.Join(fileLines, "\n")+"\n")

	_, err := editWith(t, dir, map[string]any{
		"path": path, "old_string": strings.Join(anchor, "\n"), "new_string": "gone",
	})
	if err == nil {
		t.Fatal("expected a not-found error")
	}
	if strings.Contains(err.Error(), "Send that verbatim") {
		t.Errorf("a cut excerpt must not be offered verbatim, got: %v", err)
	}
	if !strings.Contains(err.Error(), "re-read lines 1-30") {
		t.Errorf("hint should say what to re-read, got: %v", err)
	}
}

// An anchor that contains the read tool's truncation marker can never match:
// the marker replaced the rest of a line longer than read displays. The bare
// not-found left the caller to re-read and copy the same marker again; the
// error has to name the marker and the way out.
func TestEditTool_TruncationMarkerAnchorExplained(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "min.js")
	longLine := strings.Repeat("a", MaxLineLength+50)
	mustWriteFile(t, path, longLine+"\n")

	// What a caller copying from read's capped output would actually send.
	_, err := editWith(t, dir, map[string]any{
		"path":       path,
		"old_string": longLine[:MaxLineLength] + lineTruncatedSuffix,
		"new_string": "x",
	})
	if err == nil {
		t.Fatal("expected a not-found error")
	}
	if !strings.Contains(err.Error(), "truncation marker") {
		t.Errorf("error should name the marker, got: %v", err)
	}
	if !strings.Contains(err.Error(), "shorter distinctive part") {
		t.Errorf("error should offer the recovery, got: %v", err)
	}
}

// The legacy-shape guard has to watch all four per-hunk fields. A top-level
// replace_all or expected_count used to be dropped without a word — the
// dropped expected_count being the worse of the two, an assertion the caller
// believes is active with nothing checking it.
func TestEditTool_TopLevelPerHunkFieldsRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	original := "foo\nfoo\n"

	for name, extra := range map[string]string{
		"replace_all":    `"replace_all":true`,
		"expected_count": `"expected_count":2`,
	} {
		t.Run(name, func(t *testing.T) {
			mustWriteFile(t, path, original)
			raw := json.RawMessage(`{"path":` + strconv.Quote(path) + `,` + extra +
				`,"edits":[{"old_string":"foo","new_string":"bar","replace_all":true}]}`)
			_, err := EditTool{}.Execute(context.Background(), raw, Context{SessionDir: dir})
			if err == nil {
				t.Fatal("a top-level per-hunk field must be refused, not dropped")
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error should name %s among the misplaced fields, got: %v", name, err)
			}
			if got, _ := os.ReadFile(path); string(got) != original {
				t.Errorf("file must be untouched, got %q", got)
			}
		})
	}
}
