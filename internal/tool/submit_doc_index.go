package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/docindex"
)

// SubmitDocIndexTool allows the index agent to store semantic page labels.
type SubmitDocIndexTool struct {
	Store *docindex.Store
}

// NewSubmitDocIndexTool creates a new SubmitDocIndexTool.
func NewSubmitDocIndexTool(store *docindex.Store) SubmitDocIndexTool {
	return SubmitDocIndexTool{Store: store}
}

func (SubmitDocIndexTool) ID() string { return "submit_doc_index" }

func (SubmitDocIndexTool) Description() string {
	return `Submit semantic page labels for an indexed document (PDF, DOCX, or text/code file).

Call this tool once you have analyzed all page keyword corpora and determined
2-5 concise semantic labels per page. Include ALL pages — do not skip any.

Parameters:
- doc_path: absolute path to the document being indexed
- pages: array of {page_num, labels} objects covering every page`
}

func (SubmitDocIndexTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["doc_path", "pages"],
		"properties": {
			"doc_path": {
				"type": "string",
				"description": "Absolute path to the document being indexed"
			},
			"pages": {
				"type": "array",
				"description": "Array of page label objects, one per page",
				"items": {
					"type": "object",
					"required": ["page_num", "labels"],
					"properties": {
						"page_num": {
							"type": "integer",
							"description": "1-based page number"
						},
						"labels": {
							"type": "array",
							"description": "2-5 concise semantic labels for this page",
							"items": {"type": "string"}
						}
					}
				}
			}
		}
	}`)
}

func (t SubmitDocIndexTool) Execute(_ context.Context, args json.RawMessage, _ Context) (Result, error) {
	var params struct {
		DocPath string `json:"doc_path"`
		Pages   []struct {
			PageNum int      `json:"page_num"`
			Labels  []string `json:"labels"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return Result{}, fmt.Errorf("parse submit_doc_index args: %w", err)
	}
	if params.DocPath == "" {
		return Result{Title: "Submit Doc Index", Output: "doc_path is required"}, nil
	}
	if len(params.Pages) == 0 {
		return Result{Title: "Submit Doc Index", Output: "pages array is empty"}, nil
	}

	var updated int
	now := time.Now().UnixMilli()
	for _, p := range params.Pages {
		labels := p.Labels
		if labels == nil {
			labels = []string{}
		}
		// Upsert so labels are saved even if the row was never pre-registered.
		if err := t.Store.Upsert(&docindex.PageEntry{
			DocPath:   params.DocPath,
			PageNum:   p.PageNum,
			Keywords:  []string{},
			Labels:    labels,
			IndexedAt: now,
		}); err != nil {
			return Result{}, fmt.Errorf("upsert labels for page %d: %w", p.PageNum, err)
		}
		updated++
	}

	return Result{
		Title:  "Submit Doc Index",
		Output: fmt.Sprintf("Updated labels for %d pages of %s", updated, params.DocPath),
	}, nil
}
