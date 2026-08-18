package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/docindex"
)

// ProjectIndexTool returns a labeled tree of all indexed files in the
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
	return "Return a labeled tree of all indexed files — text, code, PDF, and DOCX documents. Folders end in \"/\"; each file is followed by its topic labels. For PDFs and DOCX files a concise subset of labels (up to 15) is aggregated across pages (no per-page breakdown). Use this to discover which files are relevant to a topic before reading them. Use the pdf_index tool when you need per-page labels for a specific PDF, or docx_index for a DOCX. Pass subdir to scope the results to a specific folder (e.g. \"internal/auth\") — recommended for large projects."
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

	return Result{
		Title:  fmt.Sprintf("%s (%d files)", title, totalFiles),
		Output: renderProjectMap(tree, params.Subdir),
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

// textLabelCap is the maximum number of topic labels shown per text/code file.
//
// Labels dominate this output: on a 277-file index of this repo they account
// for 55 KB of the 64 KB of real content, against 8 KB of file paths. Uncapped,
// the rendered map came to 86 KB — past the 50 KB MaxToolOutputBytes ceiling,
// so the tail was being silently truncated before the agent ever saw it. Five
// labels is enough to tell what a file covers; the file itself is one file_map
// call away.
const textLabelCap = 5

// projectMapBudget is the byte budget for a rendered project map.
//
// Held just under MaxToolOutputBytes (50 KB) so the map degrades on its own
// terms rather than being cut mid-tree by the generic backstop, which would
// leave the agent with a truncated branch and no idea what it was missing.
//
// The margin is deliberately thin. Dropping labels is a heavy loss — they are
// what makes this tool more than `find` — so it must happen only when the
// alternative is genuinely not fitting. An earlier 40 KB budget pushed this
// repo, which renders to 44 KB, onto the degraded path for nothing.
const projectMapBudget = 49 * 1024

// renderProjectMap renders the tree, dropping labels wholesale if the result
// would not fit the budget.
//
// Capping labels per file is not by itself a guarantee: at five labels each,
// this repo's 277 files render to 44 KB, but the same shape at 400 files passes
// 60 KB. When that happens the structure is worth far more than the labels —
// paths alone are 8 KB where labels are 55 KB — so the labels go and the agent
// is told how to get them back for the part of the tree it actually cares about.
func renderProjectMap(tree map[string]any, subdir string) string {
	var b strings.Builder
	b.WriteString("Folders end in \"/\". Each file is followed by its topic labels.\n\n")
	renderProjectTree(tree, 0, &b, true)

	if b.Len() <= projectMapBudget {
		return b.String()
	}

	scope := "a subdirectory"
	if subdir == "" {
		scope = "a subdirectory (e.g. subdir=\"internal/auth\")"
	}

	b.Reset()
	b.WriteString("Folders end in \"/\". This project is too large to show topic labels for every file, so only the structure is listed.\n")
	fmt.Fprintf(&b, "Call codebase_map again scoped to %s to get labels for the files there.\n\n", scope)
	renderProjectTree(tree, 0, &b, false)
	return b.String()
}

// renderProjectTree writes the tree as an indented outline. With withLabels
// false it emits paths only.
//
// Deliberately not JSON. Measured on this repo's 277-file index, MarshalIndent
// spent 20,094 tokens carrying 64 KB of paths and labels — 1.4x the content
// itself — because it puts every one of a file's labels on its own line, each
// quoted and comma-separated. The same tree as an indented outline costs 13,212
// tokens, a 34% saving, and roughly half of that comes from nothing more than
// keeping a file's labels on one line.
//
// Nothing downstream unmarshals this: it is read by a model, not parsed. The
// same reasoning gave file_map its plain-text output.
func renderProjectTree(node map[string]any, depth int, b *strings.Builder, withLabels bool) {
	keys := make([]string, 0, len(node))
	for k := range node {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	indent := strings.Repeat("  ", depth)
	for _, k := range keys {
		switch v := node[k].(type) {
		case map[string]any:
			fmt.Fprintf(b, "%s%s/\n", indent, k)
			renderProjectTree(v, depth+1, b, withLabels)
		case []string:
			if !withLabels || len(v) == 0 {
				fmt.Fprintf(b, "%s%s\n", indent, k)
				continue
			}
			fmt.Fprintf(b, "%s%s  %s\n", indent, k, strings.Join(v, ", "))
		}
	}
}

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
		if len(labels) > textLabelCap {
			labels = labels[:textLabelCap]
		}
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
