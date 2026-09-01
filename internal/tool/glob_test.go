package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestGlobTool_HiddenAndVendorDirsSkipped(t *testing.T) {
	dir := t.TempDir()
	// Visible source file (should match)
	mustWriteFile(t, filepath.Join(dir, "main.go"), "package main\n")
	// Hidden dir with a .go file (must be skipped)
	mustWriteFile(t, filepath.Join(dir, ".git", "config.go"), "package config\n")
	// node_modules with a .go file (must be skipped)
	mustWriteFile(t, filepath.Join(dir, "node_modules", "dep", "dep.go"), "package dep\n")
	// vendor with a .go file (must be skipped)
	mustWriteFile(t, filepath.Join(dir, "vendor", "v.go"), "package v\n")
	// Hidden top-level file (must be skipped)
	mustWriteFile(t, filepath.Join(dir, ".env"), "SECRET=1\n")
	// Nested visible dir (should match)
	mustWriteFile(t, filepath.Join(dir, "pkg", "util.go"), "package pkg\n")

	tool := GlobTool{}
	args, _ := json.Marshal(map[string]string{"pattern": "**/*.go"})
	res, err := tool.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := strings.Split(strings.TrimSpace(res.Output), "\n")
	sort.Strings(got)
	want := []string{"main.go", "pkg/util.go"}
	sort.Strings(want)

	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestGlobTool_PathsAreSlashSeparated(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "a", "b", "c.go"), "package c\n")

	tool := GlobTool{}
	args, _ := json.Marshal(map[string]string{"pattern": "**/*.go"})
	res, err := tool.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Output must use forward slashes on every OS (no backslashes).
	if strings.Contains(res.Output, "\\") {
		t.Fatalf("output contains backslash: %q", res.Output)
	}
	if !strings.Contains(res.Output, "a/b/c.go") {
		t.Fatalf("expected a/b/c.go in output, got %q", res.Output)
	}
}

func TestGlobTool_SimplePattern(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "main.go"), "package main\n")
	mustWriteFile(t, filepath.Join(dir, "README.md"), "# readme\n")

	tool := GlobTool{}
	args, _ := json.Marshal(map[string]string{"pattern": "*.go"})
	res, err := tool.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Output != "main.go" {
		t.Fatalf("got %q, want main.go", res.Output)
	}
}

func TestGlobTool_NoMatches(t *testing.T) {
	dir := t.TempDir()
	tool := GlobTool{}
	args, _ := json.Marshal(map[string]string{"pattern": "**/*.nope"})
	res, err := tool.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Output != "No files found" {
		t.Fatalf("got %q, want No files found", res.Output)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
