package tool

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepTool_HiddenAndVendorDirsSkipped(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "main.go"), "TODO: fix this\n")
	mustWriteFile(t, filepath.Join(dir, ".git", "config.go"), "TODO: leak\n")
	mustWriteFile(t, filepath.Join(dir, "node_modules", "dep", "dep.go"), "TODO: dep\n")
	mustWriteFile(t, filepath.Join(dir, "vendor", "v.go"), "TODO: vendor\n")

	tool := GrepTool{}
	args, _ := json.Marshal(map[string]string{"pattern": "TODO", "include": "*.go"})
	res, err := tool.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Only main.go should match; hidden/vendor dirs must be pruned.
	if strings.TrimSpace(res.Output) != "main.go:1: TODO: fix this" {
		t.Fatalf("got %q, want only main.go match", res.Output)
	}
}

func TestGrepTool_PathsAreSlashSeparated(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "a", "b", "c.go"), "FINDME\n")

	tool := GrepTool{}
	args, _ := json.Marshal(map[string]string{"pattern": "FINDME", "include": "*.go"})
	res, err := tool.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(res.Output, "\\") {
		t.Fatalf("output contains backslash: %q", res.Output)
	}
	if !strings.Contains(res.Output, "a/b/c.go:1: FINDME") {
		t.Fatalf("expected a/b/c.go match, got %q", res.Output)
	}
}

func TestGrepTool_NoMatches(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "main.go"), "package main\n")

	tool := GrepTool{}
	args, _ := json.Marshal(map[string]string{"pattern": "NOTHING_HERE"})
	res, err := tool.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Output != "No matches found" {
		t.Fatalf("got %q, want No matches found", res.Output)
	}
}

func TestGrepTool_SearchExplicitHiddenDir(t *testing.T) {
	// Pointing the tool explicitly at a hidden dir should still search it
	// (root is never pruned).
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, ".hidden", "x.go"), "TARGET\n")

	tool := GrepTool{}
	args, _ := json.Marshal(map[string]string{"pattern": "TARGET", "path": ".hidden"})
	res, err := tool.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res.Output, "x.go:1: TARGET") {
		t.Fatalf("expected match in explicit hidden dir, got %q", res.Output)
	}
}
