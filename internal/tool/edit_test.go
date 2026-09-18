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

func editWith(t *testing.T, dir string, params map[string]any) (Result, error) {
	t.Helper()
	args, _ := json.Marshal(params)
	return EditTool{}.Execute(context.Background(), args, Context{SessionDir: dir})
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

// TestEditTool_MixedFormsRejected covers the one ambiguity the two request
// shapes could create. Guessing either way could silently drop an edit the
// caller believed they had made.
func TestEditTool_MixedFormsRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	mustWriteFile(t, path, "a b\n")

	_, err := editWith(t, dir, map[string]any{
		"path": path, "old_string": "a", "new_string": "x",
		"edits": []map[string]any{{"old_string": "b", "new_string": "y"}},
	})
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Errorf("expected a mixed-form rejection, got %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "a b\n" {
		t.Errorf("file should be unchanged, got %q", got)
	}
}

// TestEditTool_ExpectedCount guards the blunt edge of replace_all: an anchor
// that matches more than the caller believed would otherwise rewrite more of
// the file than they intended, and the result would look like a success.
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

	raw := json.RawMessage(`{"path":` + strconv.Quote(path) + `,"old_string":"doWork()"}`)
	_, err := EditTool{}.Execute(context.Background(), raw, Context{SessionDir: dir})
	if err == nil {
		t.Fatal("omitting new_string must not be read as a deletion")
	}
	if !strings.Contains(err.Error(), "new_string is missing") {
		t.Errorf("error should name the missing field, got: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != original {
		t.Errorf("file must be untouched, got %q", got)
	}

	// The same omission inside an edits entry, where it is just as destructive.
	raw = json.RawMessage(`{"path":` + strconv.Quote(path) + `,"edits":[{"old_string":"doWork()"}]}`)
	_, err = EditTool{}.Execute(context.Background(), raw, Context{SessionDir: dir})
	if err == nil || !strings.Contains(err.Error(), "edits[0]") {
		t.Errorf("an edits entry missing new_string should be rejected and named, got: %v", err)
	}
}

// TestEditTool_ExplicitEmptyNewStringDeletes is the other half of that contract.
// Deleting by replacing with "" is legitimate and common — removing a step from a
// workflow is exactly it — so the stricter check must not cost it.
func TestEditTool_ExplicitEmptyNewStringDeletes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	mustWriteFile(t, path, "keep\ndrop\nkeep\n")

	raw := json.RawMessage(`{"path":` + strconv.Quote(path) + `,"old_string":"drop\n","new_string":""}`)
	if _, err := (EditTool{}).Execute(context.Background(), raw, Context{SessionDir: dir}); err != nil {
		t.Fatalf("an explicit empty new_string should delete: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "keep\nkeep\n" {
		t.Errorf("content = %q, want the line deleted", got)
	}
}
