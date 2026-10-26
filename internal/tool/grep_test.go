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

func TestGrepTool_AlternationSearchesSeveralPatternsInOneCall(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "main.go"), "package main\n// ALPHA one\n// BETA two\n")

	tool := GrepTool{}
	args, _ := json.Marshal(map[string]string{"pattern": "ALPHA|BETA"})
	res, err := tool.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"main.go:2: // ALPHA one", "main.go:3: // BETA two"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("alternation output missing %q, got %q", want, res.Output)
		}
	}
}

// Exceeding the cap is not a failed search. Returning an error made a call
// whose first hundred matches answered the question read as broken, and gave
// the agent no instruction but to repeat the same too-wide search; the result
// now stays successful and says to narrow the pattern instead.
func TestGrepTool_CappedResultStillSucceedsAndSaysNarrowIt(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	for i := 0; i < grepMaxResults*2; i++ {
		sb.WriteString("HIT\n")
	}
	mustWriteFile(t, filepath.Join(dir, "big.txt"), sb.String())

	tool := GrepTool{}
	args, _ := json.Marshal(map[string]string{"pattern": "HIT"})
	res, err := tool.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("a capped search must not be an error: %v", err)
	}
	lines := strings.Split(res.Output, "\n")
	if len(lines) != grepMaxResults+1 {
		t.Fatalf("got %d lines, want %d matches plus the notice", len(lines), grepMaxResults)
	}
	if !strings.Contains(lines[len(lines)-1], "narrow the pattern") {
		t.Errorf("last line = %q, want the narrow-the-pattern notice", lines[len(lines)-1])
	}
	if !strings.Contains(res.Title, "100+") {
		t.Errorf("title = %q, want it to mark the cap", res.Title)
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
