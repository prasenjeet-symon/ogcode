package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeWith(t *testing.T, dir string, params map[string]any) Result {
	t.Helper()
	args, _ := json.Marshal(params)
	res, err := WriteTool{}.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return res
}

func TestWriteTool_NewFileReportsCreated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")

	res := writeWith(t, dir, map[string]any{"path": path, "content": "hello\n"})

	if created, _ := res.Metadata["created"].(bool); !created {
		t.Errorf("new file should report created=true, got metadata %+v", res.Metadata)
	}
	if got, _ := os.ReadFile(path); string(got) != "hello\n" {
		t.Errorf("file content = %q, want %q", got, "hello\n")
	}
}

func TestWriteTool_ExistingFileReportsOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.txt")
	mustWriteFile(t, path, "old\n")

	res := writeWith(t, dir, map[string]any{"path": path, "content": "new\n"})

	if created, _ := res.Metadata["created"].(bool); created {
		t.Errorf("overwriting an existing file should report created=false, got metadata %+v", res.Metadata)
	}
	if old, _ := res.Metadata["oldContent"].(string); old != "old\n" {
		t.Errorf("oldContent = %q, want %q", old, "old\n")
	}
}

// Regression test: existence used to be inferred from whether os.ReadFile
// succeeded, which conflated "does not exist" with "exists but is
// unreadable". A write-only file (content unreadable, but still writable) got
// silently overwritten while being reported as newly "Created" — hiding that
// content existed and was destroyed. Existence must come from Stat, not from
// whether the content could be read.
func TestWriteTool_UnreadableExistingFileIsNotReportedAsCreated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits don't apply on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission checks")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	mustWriteFile(t, path, "content that predates this write\n")
	if err := os.Chmod(path, 0200); err != nil { // write-only: unreadable, still writable
		t.Fatal(err)
	}

	res := writeWith(t, dir, map[string]any{"path": path, "content": "new content\n"})

	if created, _ := res.Metadata["created"].(bool); created {
		t.Errorf("pre-existing (if unreadable) file must not be reported as created, got metadata %+v", res.Metadata)
	}

	// Restore read access to verify the write itself went through.
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "new content\n" {
		t.Errorf("write did not go through: got %q", got)
	}
}

func TestWriteTool_StatErrorOtherThanNotExistIsSurfaced(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits don't apply on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission checks")
	}

	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	if err := os.Mkdir(blocked, 0000); err != nil { // no traversal permission
		t.Fatal(err)
	}
	defer os.Chmod(blocked, 0755)
	path := filepath.Join(blocked, "f.txt")

	args, _ := json.Marshal(map[string]any{"path": path, "content": "x"})
	_, err := WriteTool{}.Execute(context.Background(), args, Context{SessionDir: dir})
	if err == nil {
		t.Error("expected an error stat-ing through a directory with no permissions, got nil")
	}
}

// The write now goes through a temp file and a rename, which replaces the
// inode — so without carrying the mode across, rewriting a shell script or a
// hook would silently strip its executable bit.
func TestWriteTool_PreservesExecutableBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits don't apply on windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "hook.sh")
	mustWriteFile(t, path, "#!/bin/sh\necho old\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}

	writeWith(t, dir, map[string]any{"path": path, "content": "#!/bin/sh\necho new\n"})

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("mode after write = %v, want 0755 — the script is no longer executable", got)
	}
}
