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

func (GrepTool) ID() string          { return "grep" }
func (GrepTool) Description() string { return "Search file contents for a pattern" }
func (GrepTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {"type": "string", "description": "Regex pattern to search for"},
			"path": {"type": "string", "description": "Directory or file to search in"},
			"include": {"type": "string", "description": "File glob to include (e.g. *.go)"}
		},
		"required": ["pattern"]
	}`)
}

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
	filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
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
				results = append(results, fmt.Sprintf("%s:%d: %s", rel, lineNum, scanner.Text()))
				if len(results) > 100 {
					results = append(results, "... (truncated)")
					return fmt.Errorf("max results")
				}
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

	return Result{
		Title:  fmt.Sprintf("%s (%d matches)", input.Pattern, len(results)),
		Output: strings.Join(results, "\n"),
	}, nil
}
