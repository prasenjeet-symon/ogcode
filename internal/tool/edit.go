package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type EditTool struct{}

func (EditTool) ID() string          { return "edit" }
func (EditTool) Description() string { return "Make a search-and-replace edit to a file" }
func (EditTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "File path to edit"},
			"old_string": {"type": "string", "description": "Text to find in the file"},
			"new_string": {"type": "string", "description": "Text to replace it with"}
		},
		"required": ["path", "old_string", "new_string"]
	}`)
}

func (EditTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var input struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return Result{}, fmt.Errorf("parse args: %w", err)
	}

	path := input.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(tctx.SessionDir, path)
	}

	// Serialize the read-modify-write with any concurrent write/edit to the same
	// file. The agent loop runs a turn's tool calls in parallel; without this an
	// interleaved write could make edit operate on stale content or clobber it.
	unlock := lockPath(path)
	defer unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("read file: %w", err)
	}

	content := string(data)

	if !strings.Contains(content, input.OldString) {
		return Result{}, fmt.Errorf("old_string not found in %s", path)
	}

	count := strings.Count(content, input.OldString)
	if count > 1 {
		return Result{}, fmt.Errorf("old_string appears %d times in %s — edit requires a unique match", count, path)
	}

	newContent := strings.Replace(content, input.OldString, input.NewString, 1)
	if err := os.WriteFile(path, []byte(newContent), 0o644); err != nil {
		return Result{}, fmt.Errorf("write file: %w", err)
	}

	return Result{
		Title:  filepath.Base(path),
		Output: fmt.Sprintf("Edited %s (replaced 1 occurrence)", path),
	}, nil
}