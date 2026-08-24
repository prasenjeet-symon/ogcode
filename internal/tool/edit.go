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

func (EditTool) ID() string { return "edit" }
func (EditTool) Description() string {
	return "Make a search-and-replace edit to a file. The result reports any syntax error the edit introduced, so a broken edit surfaces immediately rather than at the next build."
}
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

	// An empty old_string matches everywhere (Go's Count treats it as
	// occurring once between every rune), so it either falls into the
	// "appears N times" ambiguity error with a confusing count, or — on an
	// empty file, where that count is exactly 1 — silently "succeeds" by
	// inserting new_string into a file whose content was never actually
	// matched against anything. Reject it up front with a clear reason
	// instead of either of those.
	if input.OldString == "" {
		return Result{}, fmt.Errorf("old_string must not be empty")
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

	count := strings.Count(content, input.OldString)
	switch {
	case count == 0:
		return Result{}, fmt.Errorf("old_string not found in %s", path)
	case count > 1:
		return Result{}, fmt.Errorf("old_string appears %d times in %s — edit requires a unique match", count, path)
	}

	newContent := strings.Replace(content, input.OldString, input.NewString, 1)
	// Atomic: an edit that fails to write must not consume the file it was
	// editing. The original is still on disk, untouched, if this returns an
	// error — which matters more here than anywhere else, since the only other
	// copy of it is `data`, in memory, about to go out of scope.
	if err := writeFileAtomic(path, []byte(newContent)); err != nil {
		return Result{}, fmt.Errorf("write file: %w", err)
	}

	// A replaced block that drops a brace or breaks an indent leaves a file that
	// still writes fine and only fails much later, in a build the agent may not
	// run for several turns. The bytes on both sides are already in hand here,
	// so the check costs one parse and reports the damage while the change that
	// caused it is still the last thing that happened.
	note, check := syntaxNote(path, data, []byte(newContent))

	return applySyntaxNote(Result{
		Title:  filepath.Base(path),
		Output: fmt.Sprintf("Edited %s (replaced 1 occurrence)", path),
	}, note, check), nil
}
