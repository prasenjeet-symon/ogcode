package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/docindex"
)

// ProjectIndexTool returns a labeled JSON tree of all indexed files in the
// session directory — text/code files, PDF documents, and DOCX documents alike
// — so the agent can navigate the project by topic without knowing file paths
// upfront.
//
// Every file — text, code, PDF, or DOCX — appears as a leaf holding a flat
// topic-label array. For PDFs and DOCX files a concise subset of labels (capped
// at 15) is aggregated and de-duplicated across all pages, giving the agent a
// quick overview of what the document covers without the per-page breakdown.
// The dedicated pdf_index and docx_index tools provide the full per-page detail
// when the agent needs to decide which page to read.
type ProjectIndexTool struct {
	Store *docindex.Store
}

func NewProjectIndexTool(store *docindex.Store) ProjectIndexTool {
	return ProjectIndexTool{Store: store}
}

func (ProjectIndexTool) ID() string { return "codebase_map" }

func (ProjectIndexTool) Description() string {
	return "Return a labeled JSON tree of all indexed files — text, code, PDF, and DOCX documents. Every file appears as a leaf with its topic labels. For PDFs and DOCX files a concise subset of labels (up to 15) is aggregated across pages (no per-page breakdown). Use this to discover which files are relevant to a topic before reading them. Use the pdf_index tool when you need per-page labels for a specific PDF, or docx_index for a DOCX. Pass subdir to scope the results to a specific folder (e.g. \"internal/auth\") — recommended for large projects."
}

func (ProjectIndexTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"subdir": {
				"type": "string",
				"description": "Optional subdirectory path relative to the project root (e.g. \"internal/auth\"). Scopes results to that folder and its children. Omit to return the full project index."
			}
		}
	}`)
}

func (t ProjectIndexTool) Execute(_ context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var params struct {
		Subdir string `json:"subdir"`
	}
	if args != nil {
		_ = json.Unmarshal(args, &params)
	}

	prefix := tctx.SessionDir
	title := "Project Index"
	if params.Subdir != "" {
		prefix = filepath.Join(tctx.SessionDir, filepath.FromSlash(params.Subdir))
		title = fmt.Sprintf("Project Index / %s", params.Subdir)
	}

	textEntries, err := t.Store.ListTextFiles(prefix)
	if err != nil {
		return Result{}, fmt.Errorf("list text files: %w", err)
	}
	pdfEntries, err := t.Store.ListPDFFiles(prefix)
	if err != nil {
		return Result{}, fmt.Errorf("list pdf files: %w", err)
	}
	docxEntries, err := t.Store.ListDocxFiles(prefix)
	if err != nil {
		return Result{}, fmt.Errorf("list docx files: %w", err)
	}

	totalFiles := len(textEntries) + countDistinctDocs(pdfEntries) + countDistinctDocs(docxEntries)
	if totalFiles == 0 {
		msg := "no indexed files found — run Index Docs first to build the project index"
		if params.Subdir != "" {
			msg = fmt.Sprintf("no indexed files found under %q", params.Subdir)
		}
		return Result{Title: title, Output: msg}, nil
	}

	tree := buildProjectTree(tctx.SessionDir, textEntries, pdfEntries, docxEntries)
	out, err := json.MarshalIndent(tree, "", "  ")
	if err != nil {
		return Result{}, fmt.Errorf("marshal project tree: %w", err)
	}

	return Result{
		Title:  fmt.Sprintf("%s (%d files)", title, totalFiles),
		Output: string(out),
	}, nil
}

// countDistinctDocs returns the number of unique DocPath values in a list of
// page entries. PDFs have one entry per page, so this gives the document count.
func countDistinctDocs(entries []*docindex.PageEntry) int {
	seen := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		seen[e.DocPath] = struct{}{}
	}
	return len(seen)
}

// docLabelCap is the maximum number of topic labels returned for a multi-page
// document (PDF or DOCX) in the project tree. Labels are collected across pages
// (de-duplicated, first-seen order) and truncated at this limit so the map
// stays concise. The full per-page breakdown is available through the
// dedicated pdf_index / docx_index tools.
const docLabelCap = 15

// buildProjectTree converts flat lists of indexed entries into a nested
// folder/file tree. Every file — text/code, PDF, or DOCX — is a leaf holding a
// flat, de-duplicated label array. For PDFs and DOCX files, labels from all
// pages are merged so the agent sees what the document covers without per-page
// noise.
func buildProjectTree(baseDir string, textEntries []*docindex.PageEntry, pdfEntries []*docindex.PageEntry, docxEntries []*docindex.PageEntry) map[string]any {
	root := make(map[string]any)

	// Text/code files: one entry per file, leaf = labels array.
	for _, e := range textEntries {
		rel, err := filepath.Rel(baseDir, e.DocPath)
		if err != nil {
			rel = e.DocPath
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")

		node := root
		for _, part := range parts[:len(parts)-1] {
			child, ok := node[part]
			if !ok {
				child = make(map[string]any)
				node[part] = child
			}
			node = child.(map[string]any)
		}

		labels := e.Labels
		if labels == nil {
			labels = []string{}
		}
		node[parts[len(parts)-1]] = labels
	}

	// Multi-page documents (PDF and DOCX): multiple entries (one per page) for
	// the same file. Group them by DocPath, merge all page labels (de-duplicated,
	// order-preserving), and place a flat label-array leaf — same shape as a
	// text/code file.
	pagesByDoc := make(map[string][]*docindex.PageEntry)
	for _, e := range pdfEntries {
		pagesByDoc[e.DocPath] = append(pagesByDoc[e.DocPath], e)
	}
	for _, e := range docxEntries {
		pagesByDoc[e.DocPath] = append(pagesByDoc[e.DocPath], e)
	}

	// Sort doc paths for deterministic ordering.
	docs := make([]string, 0, len(pagesByDoc))
	for docPath := range pagesByDoc {
		docs = append(docs, docPath)
	}
	// Simple insertion sort to avoid a sort import; doc counts are small.
	for i := 1; i < len(docs); i++ {
		for j := i; j > 0 && docs[j-1] > docs[j]; j-- {
			docs[j-1], docs[j] = docs[j], docs[j-1]
		}
	}

	for _, docPath := range docs {
		entries := pagesByDoc[docPath]
		rel, err := filepath.Rel(baseDir, docPath)
		if err != nil {
			rel = docPath
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")

		node := root
		for _, part := range parts[:len(parts)-1] {
			child, ok := node[part]
			if !ok {
				child = make(map[string]any)
				node[part] = child
			}
			node = child.(map[string]any)
		}

		// Merge labels from every page, preserving first-seen order and
		// dropping duplicates. Cap at docLabelCap so the map stays concise —
		// the full per-page detail is available via the pdf_index / docx_index
		// tools.
		seen := make(map[string]struct{})
		var merged []string
		for _, e := range entries {
			for _, l := range e.Labels {
				if len(merged) >= docLabelCap {
					break
				}
				if _, ok := seen[l]; ok {
					continue
				}
				seen[l] = struct{}{}
				merged = append(merged, l)
			}
			if len(merged) >= docLabelCap {
				break
			}
		}
		if merged == nil {
			merged = []string{}
		}
		node[parts[len(parts)-1]] = merged
	}

	return root
}