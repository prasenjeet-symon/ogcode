package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
