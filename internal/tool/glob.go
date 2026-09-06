package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type GlobTool struct{}

func (GlobTool) ID() string          { return "glob" }
func (GlobTool) Description() string { return "Find files matching a glob pattern" }
func (GlobTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {"type": "string", "description": "Glob pattern to match (e.g. **/*.go)"}
		},
		"required": ["pattern"]
	}`)
}

func (GlobTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var input struct {
		Pattern string `json:"pattern"`
	}
	if err := DecodeArgs(args, &input); err != nil {
		return Result{}, fmt.Errorf("parse args: %w", err)
	}

	pattern := input.Pattern
	var matches []string

	if strings.Contains(pattern, "**") {
		// Walk the directory tree for ** patterns.
		// Split pattern: "**/*.go" -> walk all, match suffix "*.go"
		suffix := strings.TrimPrefix(pattern, "**/")
		root := filepath.Clean(tctx.SessionDir)
		filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				// Skip hidden dirs and common non-source dirs, but never the
				// walk root itself. Use info.Name() (not filepath.Rel) so this
				// works on every OS — filepath.Rel separators differ between
				// Unix ("/") and Windows ("\"), and a hardcoded "/." check
				// silently matched nothing on Windows, causing the tool to
				// descend into .git/node_modules and flood results.
				if filepath.Clean(path) != root {
					name := info.Name()
					if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
						return filepath.SkipDir
					}
				}
				return nil
			}
			// Skip hidden files (e.g. .DS_Store, .env).
			if strings.HasPrefix(info.Name(), ".") {
				return nil
			}
			matched, _ := filepath.Match(suffix, info.Name())
			if matched {
				rel, _ := filepath.Rel(root, path)
				// Normalize to forward slashes so output is identical on every
				// platform and easy for the agent to reason about.
				matches = append(matches, filepath.ToSlash(rel))
			}
			return nil
		})
	} else {
		// Simple pattern: use filepath.Glob
		absMatches, err := filepath.Glob(filepath.Join(tctx.SessionDir, pattern))
		if err != nil {
			return Result{}, fmt.Errorf("glob: %w", err)
		}
		for _, m := range absMatches {
			rel, _ := filepath.Rel(tctx.SessionDir, m)
			matches = append(matches, filepath.ToSlash(rel))
		}
	}

	if len(matches) == 0 {
		return Result{
			Title:  input.Pattern,
			Output: "No files found",
		}, nil
	}

	return Result{
		Title:  fmt.Sprintf("%s (%d files)", input.Pattern, len(matches)),
		Output: strings.Join(matches, "\n"),
	}, nil
}
