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
// session directory — text/code files and PDF documents alike — so the agent
// can navigate the project by topic without knowing file paths upfront.
//
// Text/code files appear as leaves holding their topic-label array. PDF files
// appear as leaves holding a per-page map of page-number → labels, giving the
// agent enough structure to decide which pages to read with read_pdf_page.
type ProjectIndexTool struct {
	Store *docindex.Store
}

func NewProjectIndexTool(store *docindex.Store) ProjectIndexTool {
	return ProjectIndexTool{Store: store}
}

func (ProjectIndexTool) ID() string { return "codebase_map" }

func (ProjectIndexTool) Description() string {
	return "Return a labeled JSON tree of all indexed files — text, code, and PDF documents. Each text/code file appears as a leaf with its topic labels; each PDF appears as a leaf with per-page topic labels. Use this to discover which files (including PDFs) are relevant to a topic before reading them. Pass subdir to scope the results to a specific folder (e.g. \"internal/auth\") — recommended for large projects."
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

	totalFiles := len(textEntries) + countDistinctDocs(pdfEntries)
	if totalFiles == 0 {
		msg := "no indexed files found — run Index Docs first to build the project index"
		if params.Subdir != "" {
			msg = fmt.Sprintf("no indexed files found under %q", params.Subdir)
		}
		return Result{Title: title, Output: msg}, nil
	}

	tree := buildProjectTree(tctx.SessionDir, textEntries, pdfEntries)
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

// buildProjectTree converts flat lists of indexed entries into a nested
// folder/file tree. Text/code files are leaves holding their label array.
// PDF files are leaves holding a per-page map: {"pages": {1: [...], 2: [...]}}.
func buildProjectTree(baseDir string, textEntries []*docindex.PageEntry, pdfEntries []*docindex.PageEntry) map[string]any {
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

	// PDF files: multiple entries (one per page) for the same file. Group
	// them by DocPath and place a {"pages": {pageNum: labels}} leaf.
	pagesByDoc := make(map[string][]*docindex.PageEntry)
	for _, e := range pdfEntries {
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

		pages := make(map[int][]string, len(entries))
		for _, e := range entries {
			labels := e.Labels
			if labels == nil {
				labels = []string{}
			}
			pages[e.PageNum] = labels
		}
		node[parts[len(parts)-1]] = map[string]any{"pages": pages}
	}

	return root
}