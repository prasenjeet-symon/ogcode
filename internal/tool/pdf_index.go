package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/prasenjeet-symon/ogcode/internal/docindex"
)

// PdfIndexTool returns the stored semantic map for a PDF — page labels — so
// the agent can decide which pages to read before calling read_pdf_page.
type PdfIndexTool struct {
	Store *docindex.Store
}

func NewPdfIndexTool(store *docindex.Store) PdfIndexTool {
	return PdfIndexTool{Store: store}
}

func (PdfIndexTool) ID() string { return "pdf_index" }

func (PdfIndexTool) Description() string {
	return "Return the semantic index for a PDF file: for each page, the topic labels produced by ogcode index. Use this to understand document structure and locate relevant pages before reading them with read_pdf_page."
}

func (PdfIndexTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["path"],
		"properties": {
			"path": {
				"type": "string",
				"description": "Path to the PDF file (absolute or relative to session directory)"
			}
		}
	}`)
}

func (t PdfIndexTool) Execute(_ context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var params struct {
		Path string `json:"path"`
	}
	if err := DecodeArgs(args, &params); err != nil {
		return Result{}, fmt.Errorf("parse pdf_index args: %w", err)
	}
	if params.Path == "" {
		return Result{Title: "PDF Index", Output: "path is required"}, nil
	}

	path := params.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(tctx.SessionDir, path)
	}

	entries, err := t.Store.GetByDoc(path)
	if err != nil {
		return Result{}, fmt.Errorf("get index for %s: %w", path, err)
	}
	if len(entries) == 0 {
		return Result{
			Title:  "PDF Index",
			Output: fmt.Sprintf("no index found for %s — run `ogcode index` first to build the semantic map", filepath.Base(path)),
		}, nil
	}

	type pageMap struct {
		Page   int      `json:"page"`
		Labels []string `json:"labels"`
	}
	pages := make([]pageMap, len(entries))
	for i, e := range entries {
		labels := e.Labels
		if labels == nil {
			labels = []string{}
		}
		pages[i] = pageMap{Page: e.PageNum, Labels: labels}
	}

	out, err := json.MarshalIndent(pages, "", "  ")
	if err != nil {
		return Result{}, fmt.Errorf("marshal index: %w", err)
	}

	return Result{
		Title:  fmt.Sprintf("PDF Index / %s (%d pages)", filepath.Base(path), len(entries)),
		Output: string(out),
	}, nil
}
