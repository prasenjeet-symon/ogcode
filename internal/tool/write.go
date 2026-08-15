package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type WriteTool struct{}

func (WriteTool) ID() string          { return "write" }
func (WriteTool) Description() string { return "Write content to a file, creating it if it doesn't exist" }
func (WriteTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "File path to write to"},
			"content": {"type": "string", "description": "Content to write"}
		},
		"required": ["path", "content"]
	}`)
}

func (WriteTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var input struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return Result{}, fmt.Errorf("parse args: %w", err)
	}

	path := input.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(tctx.SessionDir, path)
	}

	// Serialize with any concurrent write/edit to the same file. The agent loop
	// runs a turn's tool calls in parallel, so two mutations to this path would
	// otherwise race and lose one another's changes.
	unlock := lockPath(path)
	defer unlock()

	// Capture the prior content (if any) so the UI can render a before/after diff.
	// Cap the captured size so huge files don't bloat the persisted message.
	const maxDiffBytes = 256 * 1024
	var oldContent string
	created := true
	diffOmitted := false
	if existing, err := os.ReadFile(path); err == nil {
		created = false
		if len(existing) <= maxDiffBytes && len(input.Content) <= maxDiffBytes {
			oldContent = string(existing)
		} else {
			diffOmitted = true
		}
	} else if len(input.Content) > maxDiffBytes {
		diffOmitted = true
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create dirs: %w", err)
	}

	if err := os.WriteFile(path, []byte(input.Content), 0o644); err != nil {
		return Result{}, fmt.Errorf("write file: %w", err)
	}

	metadata := map[string]any{"created": created}
	if diffOmitted {
		metadata["diffOmitted"] = true
	} else if !created {
		metadata["oldContent"] = oldContent
	}

	verb := "Wrote"
	if created {
		verb = "Created"
	}
	return Result{
		Title:    filepath.Base(path),
		Output:   fmt.Sprintf("%s %d bytes to %s", verb, len(input.Content), path),
		Metadata: metadata,
	}, nil
}