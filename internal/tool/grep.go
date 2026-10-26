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
	return "Search file contents for a pattern; alternate patterns (a|b|c) to search several in one call"
}
func (GrepTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {"type": "string", "description": "Regex pattern to search for. Search several at once by alternating them: \"foo|bar|baz\". A capped result names itself — narrow the pattern then."},
			"path": {"type": "string", "description": "Directory or file to search in"},
			"include": {"type": "string", "description": "File glob to include (e.g. *.go)"}
		},
		"required": ["pattern"]
	}`)
}

// grepMaxResults caps one call's output. Past it the result says so in its own
// body and stays a successful result: a search whose 200 matches answer the
// question must not read as a failed call, and the notice tells the agent to
// narrow the pattern rather than repeat the same too-wide search.
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
	truncated := false
	filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if truncated {
			return filepath.SkipAll
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

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		rel, _ := filepath.Rel(searchDir, path)
		// Normalize to forward slashes so output is identical on every platform.
		rel = filepath.ToSlash(rel)
		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			if re.MatchString(scanner.Text()) {
				if len(results) >= grepMaxResults {
					truncated = true
					return nil
				}
				results = append(results, fmt.Sprintf("%s:%d: %s", rel, lineNum, scanner.Text()))
			}
		}
		return nil
	})

	if len(results) == 0 {
		return Result{
			Title:  input.Pattern,
			Output: "No matches found",
		}, nil
	}

	title := fmt.Sprintf("%s (%d matches)", input.Pattern, len(results))
	if truncated {
		title = fmt.Sprintf("%s (%d+ matches)", input.Pattern, len(results))
		results = append(results, fmt.Sprintf("... (stopped at %d of many) — narrow the pattern to see the rest", grepMaxResults))
	}

	return Result{
		Title:  title,
		Output: strings.Join(results, "\n"),
	}, nil
}
