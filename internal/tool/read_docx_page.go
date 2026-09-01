package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/prasenjeet-symon/ogcode/internal/docx"
)

// ReadDocxPageTool extracts the text of a single pseudo-page from a DOCX file
// for the agent to read. Use docx_index first to identify which pages are
// relevant, then call this tool to read their content.
type ReadDocxPageTool struct{}

func (ReadDocxPageTool) ID() string { return "read_docx_page" }

func (ReadDocxPageTool) Description() string {
	return "Extract the plain text of a single pseudo-page from a DOCX file. Use docx_index first to identify which pages are relevant, then call this tool to read their content. DOCX files do not have inherent page boundaries, so pages are grouped by explicit page breaks or word count limits."
}

func (ReadDocxPageTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["path", "page"],
		"properties": {
			"path": {
				"type": "string",
				"description": "Path to the DOCX file (absolute or relative to session directory)"
			},
			"page": {
				"type": "integer",
				"description": "1-based pseudo-page number to read"
			}
		}
	}`)
}

func (ReadDocxPageTool) Execute(_ context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var params struct {
		Path string `json:"path"`
		Page int    `json:"page"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return Result{}, fmt.Errorf("parse read_docx_page args: %w", err)
	}
	if params.Path == "" {
		return Result{Title: "Read DOCX Page", Output: "path is required"}, nil
	}
	if params.Page < 1 {
		return Result{Title: "Read DOCX Page", Output: "page must be >= 1"}, nil
	}

	path := params.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(tctx.SessionDir, path)
	}

	pages, err := docx.ExtractPages(path)
	if err != nil {
		return Result{}, fmt.Errorf("extract docx %s: %w", path, err)
	}

	if params.Page > len(pages) {
		return Result{
			Title:  fmt.Sprintf("DOCX Page %d", params.Page),
			Output: fmt.Sprintf("page %d out of range (document has %d pages)", params.Page, len(pages)),
		}, nil
	}

	text := pages[params.Page-1].Text
	if text == "" {
		text = "(no extractable text on this page)"
	}

	return Result{
		Title:  fmt.Sprintf("DOCX Page %d / %s", params.Page, filepath.Base(path)),
		Output: text,
	}, nil
}
