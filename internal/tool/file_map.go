package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/prasenjeet-symon/ogcode/internal/codemap"
)

// FileMapTool returns the structural outline of a single file — every
// declaration it makes and the line range that declaration occupies — so the
// agent can read the one region it needs instead of the whole file.
//
// It pairs with the read tool the way pdf_index pairs with read_pdf_page:
// file_map to decide where to look, read(start_line, end_line) to look. Unlike
// that pair it consults no index — the outline is parsed from the file on every
// call, so its ranges always describe the file's current contents.
type FileMapTool struct{}

func (FileMapTool) ID() string { return "file_map" }

// Description carries the two limits an agent cannot recover from the output.
//
// What the map omits is invisible by construction: a name that was never
// captured leaves no trace, so an agent hunting a struct field finds nothing and
// has no way to tell "not in this file" from "not the kind of thing this lists".
// And grammar coverage decides whether a range is exact or approximate, which
// changes how much slack to allow around it.
//
// The runtime notes Render already emits — the symbol cap, a recovered parse
// error, the heuristic-scan warning — are deliberately not repeated here. They
// arrive attached to the map they describe, which beats a static sentence the
// agent has to remember applies.
func (FileMapTool) Description() string {
	return "Return the structural map of a single file: every declaration with its 1-based line range and its doc comment where it has one, nested entries indented under whatever contains them (a class's methods, a component's handlers). Use this before reading an unfamiliar file, then pass a range straight to read(path, start_line, end_line) to pull in only the part you need instead of the entire file. Ranges include each declaration's doc comment. Call this again after editing a file — the line numbers of everything below an edit will have moved. It lists declarations only: struct fields, class properties, and non-function local variables never appear, so a name missing from the map is not missing from the file. Go, TypeScript, TSX/JavaScript, Python, Rust, Java, C#, Dart, PHP and Swift are parsed with a real grammar; any other file type falls back to an approximate heuristic scan, which the map flags when it happens."
}

func (FileMapTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["path"],
		"properties": {
			"path": {
				"type": "string",
				"description": "Path to the file (absolute or relative to session directory)"
			}
		}
	}`)
}

func (FileMapTool) Execute(_ context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return Result{}, fmt.Errorf("parse file_map args: %w", err)
	}
	if params.Path == "" {
		return Result{Title: "File Map", Output: "path is required"}, nil
	}

	path := params.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(tctx.SessionDir, path)
	}

	fm, err := codemap.Outline(path)
	if err != nil {
		// The cases below are ordinary properties of the file, not failures of
		// the call. Returning them as tool output rather than an error tells
		// the agent what to do next; returning an error would just read as the
		// tool being broken.
		var tooLarge *codemap.TooLargeError
		switch {
		case errors.Is(err, os.ErrNotExist):
			return Result{Title: "File Map", Output: fmt.Sprintf("%s does not exist", params.Path)}, nil
		case errors.Is(err, codemap.ErrBinary):
			return Result{Title: "File Map", Output: fmt.Sprintf("%s is a binary file — it has no line structure to map", params.Path)}, nil
		case errors.As(err, &tooLarge):
			return Result{Title: "File Map", Output: fmt.Sprintf("%s is too large to map (%d bytes). Read it in pages with read(path, offset, limit).", params.Path, tooLarge.Size)}, nil
		}
		return Result{}, fmt.Errorf("map %s: %w", params.Path, err)
	}

	return Result{
		Title:  fmt.Sprintf("File Map / %s (%d symbols)", filepath.Base(path), len(fm.Symbols)),
		Output: codemap.Render(fm),
	}, nil
}
