package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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

// MaxLabelsPerPage is the ceiling on the labels the index agent may attach to a
// single page — the one limit on label production. It exists only to stop a
// runaway model filling the store with near-duplicates; the agent is asked for
// every distinct topic the page supports, and a well-indexed page sits far
// below this. Every mention of it (the tool description, the JSON schema, the
// project map's textLabelCap) is derived from this constant, so a change here
// cannot leave one of them behind.
const MaxLabelsPerPage = 30

func (SubmitDocIndexTool) ID() string { return "submit_doc_index" }

func (SubmitDocIndexTool) Description() string {
	return fmt.Sprintf(`Submit semantic page labels for an indexed document (PDF, DOCX, or text/code file).

Call this tool once you have analyzed all page keyword corpora and determined
the labels for each page — every distinct topic the content supports, up to %d
per page. Include ALL pages — do not skip any.

Parameters:
- doc_path: absolute path to the document being indexed
- pages: array of {page_num, labels} objects covering every page`, MaxLabelsPerPage)
}

func (SubmitDocIndexTool) Parameters() json.RawMessage {
	// Built with the constant rather than a literal, so the advertised ceiling
	// cannot drift from the one the agent is told about in Description.
	return json.RawMessage(fmt.Sprintf(`{
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
							"description": "Semantic labels for this page — every distinct topic the content supports, up to %d",
							"maxItems": %d,
							"items": {"type": "string"}
						}
					}
				}
			}
		}
	}`, MaxLabelsPerPage, MaxLabelsPerPage))
}

func (t SubmitDocIndexTool) Execute(_ context.Context, args json.RawMessage, _ Context) (Result, error) {
	var params struct {
		DocPath string `json:"doc_path"`
		Pages   []struct {
			PageNum int      `json:"page_num"`
			Labels  []string `json:"labels"`
		} `json:"pages"`
	}
	if err := DecodeArgs(args, &params); err != nil {
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
	// Record the file's modification time with its rows, so a later incremental
	// run can tell this file from a rewritten one. A stat that fails leaves 0,
	// which reads as stale and simply costs one re-index rather than hiding an
	// edit.
	var modTime int64
	if info, err := os.Stat(params.DocPath); err == nil {
		modTime = info.ModTime().UnixMilli()
	}
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
			ModTime:   modTime,
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
