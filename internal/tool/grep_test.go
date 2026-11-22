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

// Exceeding the cap rejects the whole call: no partial matches are returned.
// The error names the limit and how to narrow (path/include or codebase_map)
// instead of returning the first hundred as if that were the search's answer.
func TestGrepTool_OverLimitRejectsAndSaysNarrowIt(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	for i := 0; i < grepMaxResults*2; i++ {
		sb.WriteString("HIT\n")
	}
	mustWriteFile(t, filepath.Join(dir, "big.txt"), sb.String())

	tool := GrepTool{}
	args, _ := json.Marshal(map[string]string{"pattern": "HIT"})
	res, err := tool.Execute(context.Background(), args, Context{SessionDir: dir})
	if err == nil {
		t.Fatalf("an over-limit search must be an error, got success with %q", res.Output)
	}
	if res.Output != "" {
		t.Errorf("a rejected search must return no matches, got %q", res.Output)
	}
	for _, want := range []string{"too large", "narrow", "codebase_map"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q, got %q", want, err.Error())
		}
	}
}

// A line longer than bufio's 64KB token cap stops the scanner mid-file. The
// matches before the break are real but the rest was never looked at; the
// call is rejected — no partial matches — and the error names the file cut
// short and how to narrow the search instead of presenting an unfinished
// scan as the search's answer.
func TestGrepTool_LineTooLongRefusesNamingTheFile(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	sb.WriteString("HIT before the long line\n")
	sb.WriteString(strings.Repeat("x", 128*1024)) // one 128KB line, no newline
	sb.WriteString("\nHIT after the long line\n")
	mustWriteFile(t, filepath.Join(dir, "long.txt"), sb.String())

	tool := GrepTool{}
	args, _ := json.Marshal(map[string]string{"pattern": "HIT"})
	res, err := tool.Execute(context.Background(), args, Context{SessionDir: dir})
	if err == nil {
		t.Fatalf("a >64KB line must reject the search, got %q", res.Output)
	}
	if res.Output != "" {
		t.Errorf("a rejected search must return no matches, got %q", res.Output)
	}
	for _, want := range []string{"long.txt", "64KB", "codebase_map"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q, got %q", want, err.Error())
		}
	}
}

// One file hitting the 64KB cap rejects the whole search — clean files'
// matches are not returned either: refusing with the first file's error
// keeps the agent from treating an unfinished walk as a complete search.
func TestGrepTool_LineTooLongRejectsEvenWithCleanFilesPresent(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	sb.WriteString("HIT in the long file\n")
	sb.WriteString(strings.Repeat("x", 128*1024))
	mustWriteFile(t, filepath.Join(dir, "long.txt"), sb.String())
	mustWriteFile(t, filepath.Join(dir, "good.txt"), "HIT in the clean file\n")

	tool := GrepTool{}
	args, _ := json.Marshal(map[string]string{"pattern": "HIT"})
	res, err := tool.Execute(context.Background(), args, Context{SessionDir: dir})
	if err == nil {
		t.Fatalf("a file over the 64KB cap must reject the search, got %q", res.Output)
	}
	if res.Output != "" {
		t.Errorf("a rejected search must return no matches, got %q", res.Output)
	}
	for _, want := range []string{"long.txt", "64KB", "codebase_map"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q, got %q", want, err.Error())
		}
	}
}

// A binary file is skipped silently — a NUL byte in the first 8KB means it is
// not a text source, so a repo holding binaries does not reject searches that
// would otherwise be fine.
func TestGrepTool_BinaryFileSkippedSilently(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "blob.bin"), "HIT\x00payload\n")

	tool := GrepTool{}
	args, _ := json.Marshal(map[string]string{"pattern": "HIT"})
	res, err := tool.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Output != "No matches found" {
		t.Fatalf("got %q, want a plain no-matches result", res.Output)
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
