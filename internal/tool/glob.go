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

func (GlobTool) ID() string { return "glob" }
func (GlobTool) Description() string {
	return "Find files matching a glob pattern. ** walks the directory tree, but the part that follows it is matched against file base names only — \"**/*.go\" matches at any depth while \"dir/**/*.go\" matches nothing: a ** walk has no per-directory filtering, so scope a simple pattern like \"pkg/*.go\" to one level instead. A malformed pattern is an error, not a silent empty search. The ** walk skips hidden (dot-prefixed) files and dirs, node_modules and vendor; a simple pattern (no **) is matched against directory entries and matches hidden files too. Relative patterns resolve against the session dir and may traverse outside it with \"..\"."
}
func (GlobTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {"type": "string", "description": "Glob pattern to match (e.g. **/*.go). ** walks the tree; the part after it matches file base names only, not path segments. The ** walk skips hidden (dot-prefixed) files and hidden/node_modules/vendor dirs — a simple pattern (no **) matches hidden files too. A malformed pattern is an error, not an empty result. A relative pattern is resolved against the session dir and may traverse outside it with \"..\"."}
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
		walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
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
			// A malformed pattern (an unclosed "[" class) must abort the
			// walk rather than silently match nothing: with the error
			// discarded every filename was "not matched" and the call came
			// back "No files found" — an empty result that read as an
			// honest search of a pattern the tool never understood.
			matched, err := filepath.Match(suffix, info.Name())
			if err != nil {
				return fmt.Errorf("glob: bad pattern %q: %w", suffix, err)
			}
			if matched {
				rel, _ := filepath.Rel(root, path)
				// Normalize to forward slashes so output is identical on every
				// platform and easy for the agent to reason about.
				matches = append(matches, filepath.ToSlash(rel))
			}
			return nil
		})
		if walkErr != nil {
			return Result{}, walkErr
		}
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
