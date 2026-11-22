package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type GrepTool struct{}

func (GrepTool) ID() string { return "grep" }
func (GrepTool) Description() string {
	return "Search file contents for a pattern; alternate patterns (a|b|c) to search several in one call. Prefer scoping the search with path or include (e.g. to a directory or *.go) and use codebase_map to locate relevant files first — an unscoped search that would produce a large output is rejected outright, so narrow it before calling. Hidden (dot-prefixed) dirs and node_modules/vendor dirs are skipped — the search root itself is always searched, and hidden FILES are still searched."
}
func (GrepTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {"type": "string", "description": "Regex pattern to search for. Search several at once by alternating them: \"foo|bar|baz\". A search matching more than 100 lines is rejected, not partially returned — narrow the pattern, path or include instead."},
			"path": {"type": "string", "description": "Directory or file to search in"},
			"include": {"type": "string", "description": "File glob to include (e.g. *.go), matched against file base names"}
		},
		"required": ["pattern"]
	}`)
}

// grepMaxResults caps one call's output. Past it the call is rejected with
// an error and returns no matches: partial output spends the context on a
// search that did not finish, and the error names the limit and how to
// narrow (pattern, path, include, or codebase_map) instead of repeating the
// same too-wide search for a slightly different subset.
const grepMaxResults = 100

func (GrepTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var input struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Include string `json:"include"`
	}
	if err := DecodeArgs(args, &input); err != nil {
		return Result{}, fmt.Errorf("parse args: %w", err)
	}

	re, err := regexp.Compile(input.Pattern)
	if err != nil {
		return Result{}, fmt.Errorf("compile pattern: %w", err)
	}

	searchDir := tctx.SessionDir
	if input.Path != "" {
		if filepath.IsAbs(input.Path) {
			searchDir = input.Path
		} else {
			searchDir = filepath.Join(tctx.SessionDir, input.Path)
		}
	}
	searchDir = filepath.Clean(searchDir)

	includePattern := input.Include
	if includePattern == "" {
		includePattern = "*"
	}

	var results []string
	walkErr := filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			// Skip hidden dirs and common non-source dirs, but never the search
			// root itself — if the user explicitly points the tool at a hidden
			// or vendor dir we should still search it.
			if path != searchDir {
				name := info.Name()
				if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
					return filepath.SkipDir
				}
			}
			return nil
		}

		matched, _ := filepath.Match(includePattern, info.Name())
		if !matched && includePattern != "*" {
			return nil
		}

		if isBinaryFile(path) {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}

		rel, _ := filepath.Rel(searchDir, path)
		// Normalize to forward slashes so output is identical on every platform.
		rel = filepath.ToSlash(rel)
		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			if re.MatchString(scanner.Text()) {
				if len(results) >= grepMaxResults {
					return fmt.Errorf("too many matches (more than %d) — output would be too large; narrow the pattern or scope the search with path/include, or use codebase_map to identify the relevant files first and search those", grepMaxResults)
				}
				results = append(results, fmt.Sprintf("%s:%d: %s", rel, lineNum, scanner.Text()))
			}
		}
		f.Close()
		// A scan error (bufio's 64KB token cap on a long line) stopped the
		// scan partway through the file: the matches before the break are
		// real but the rest was never looked at, so returning them would
		// present a partial search as a complete one. Refuse the call and
		// name the file and how to scope the search instead.
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("%s was scanned only partway — %v; any line longer than the 64KB scan buffer ends it, so matches past that line went unread. Narrow the search with path/include (e.g. include *.go) or use codebase_map to identify the relevant files first", rel, err)
		}
		return nil
	})
	if walkErr != nil {
		return Result{}, walkErr
	}

	if len(results) == 0 {
		return Result{Title: input.Pattern, Output: "No matches found"}, nil
	}

	title := fmt.Sprintf("%s (%d matches)", input.Pattern, len(results))
	return Result{Title: title, Output: strings.Join(results, "\n")}, nil
}

// isBinaryFile reports whether the first 8KB of the file contains a NUL
// byte — the usual binary marker. Binary payloads (compiled binaries, etc.)
// are skipped silently; only text files hit by the 64KB line cap reject the
// whole search.
func isBinaryFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 8192)
	n, _ := f.Read(buf)
	for _, b := range buf[:n] {
		if b == 0 {
			return true
		}
	}
	return false
}
