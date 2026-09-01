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
// The map shows one level at a time. Every folder at that level is a single
// line — its most common topic labels and how many files it holds — whatever
// its size, and the files sitting directly at that level are listed with their
// own labels. The subdir parameter is the only way down: each call re-roots the
// map one directory deeper. Output size therefore tracks how wide a level is,
// never how large the project is beneath it.
//
// Text, code, PDF, and DOCX leaves all carry a flat topic-label array. For
// PDFs and DOCX files a concise subset of labels (capped at 15) is aggregated
// and de-duplicated across all pages; the dedicated pdf_index and docx_index
// tools provide the full per-page detail when the agent needs to decide which
// page to read.
type ProjectIndexTool struct {
	Store *docindex.Store
}

func NewProjectIndexTool(store *docindex.Store) ProjectIndexTool {
	return ProjectIndexTool{Store: store}
}

func (ProjectIndexTool) ID() string { return "codebase_map" }

func (ProjectIndexTool) Description() string {
	return "Return one level of a labeled map of indexed files — text, code, PDF, and DOCX documents. Every folder is shown as a SINGLE line with its most common topic labels and the number of files inside it; files sitting directly at that level are listed individually with their own labels. To look inside a folder, call again with subdir set to its path (e.g. \"internal/tool\") — each call descends one level. For PDFs and DOCX files a concise subset of labels (up to 15) is aggregated across pages (no per-page breakdown). Use this to find which area is relevant to a topic before reading anything; use pdf_index for per-page labels of a specific PDF, or docx_index for a DOCX."
}

func (ProjectIndexTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"subdir": {
				"type": "string",
				"description": "Optional subdirectory path relative to the project root (e.g. \"internal/auth\"). Roots the map at that folder: the files directly inside it are listed, and the folders inside it are each summarized on one line. Omit to start at the project root."
			}
		}
	}`)
}

// fileCount renders a file tally with the right noun. Used by both the map's
// opening line and its folder lines, so "1 file" never reads as "1 files".
func fileCount(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

// levelSummary describes what a rendered level actually contains, for the tool
// result's title.
//
// The title used to read "(332 files)" — totalFiles, every file the map covers.
// Next to a 19-line result that reads as "this call just pulled in 332 files",
// which is exactly backwards: the whole point of collapsing folders is that 332
// indexed files cost sixteen lines. Say what was listed, and keep the coverage
// figure in parentheses where it cannot be mistaken for the payload.
func levelSummary(tree map[string]any, totalFiles int) string {
	var dirs, files int
	for _, v := range tree {
		if _, isDir := v.(map[string]any); isDir {
			dirs++
			continue
		}
		files++
	}

	plural := func(n int, noun string) string {
		if n == 1 {
			return fmt.Sprintf("%d %s", n, noun)
		}
		return fmt.Sprintf("%d %ss", n, noun)
	}

	var shown []string
	if dirs > 0 {
		shown = append(shown, plural(dirs, "folder"))
	}
	if files > 0 {
		shown = append(shown, plural(files, "file"))
	}
	if len(shown) == 0 {
		return "empty"
	}
	return fmt.Sprintf("%s (%d indexed)", strings.Join(shown, ", "), totalFiles)
}

// resolveSubdirPrefix joins subdir onto sessionDir and reports whether the
// result is still inside it. ok is false for a subdir that climbs out.
//
// filepath.Join cleans its result, so "../other-project" resolves to a sibling
// of the session directory and the store would then happily list it: the doc
// index is one store for the whole machine, so that reaches another project's
// paths and topic labels. The store's own prefix filter was tightened to a
// directory boundary (see docindex.dirPrefixFilter) — this is the same boundary
// one layer up, where the untrusted string actually enters.
//
// An empty sessionDir means the tool is already unscoped; there is no boundary
// to enforce, and the joined path is used as-is.
func resolveSubdirPrefix(sessionDir, subdir string) (string, bool) {
	joined := filepath.Join(sessionDir, filepath.FromSlash(subdir))
	if sessionDir == "" {
		return joined, true
	}
	rel, err := filepath.Rel(sessionDir, joined)
	if err != nil {
		return "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return joined, true
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
		scoped, ok := resolveSubdirPrefix(tctx.SessionDir, params.Subdir)
		if !ok {
			return Result{
				Title:  title,
				Output: fmt.Sprintf("subdir %q resolves outside the project directory", params.Subdir),
			}, nil
		}
		prefix = scoped
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

	// When subdir is set the tree is rooted at that folder: its files list in
	// full and the drill-down reads naturally ("internal/tool/read.go", not
	// "internal/tool/ internal/tool/ read.go"), while large folders inside it
	// stay summarized.
	tree := buildProjectTree(prefix, textEntries, pdfEntries, docxEntries)

	return Result{
		Title:  fmt.Sprintf("%s — %s", title, levelSummary(tree, dirStatsOf(tree).files)),
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

// folderLabelCap is the maximum number of topic labels shown on a collapsed
// folder's summary line.
//
// One more than textLabelCap: a folder summarizes many files and needs
// slightly more topical spread than a single file. Labels are ranked by how
// many of the folder's files carry them, so what survives is what is most
// common there, not what came first.
const folderLabelCap = 6

// dirStats holds what folder summarization needs to know about one directory:
// how many files it contains (all descendants, not just immediate children)
// and how many of those files carry each topic label.
type dirStats struct {
	files  int
	labels map[string]int
}

// dirStatsOf computes the descendant-file count and label frequencies of a
// directory node by walking its leaves. A []string node value is a file (its
// elements are labels); a map[string]any is a subdirectory. Rendered counts
// must be identical everywhere they are needed — the collapse decision and the
// summary line both read this — so nothing downstream can disagree about how
// many files a folder holds.
//
// Labels are counted once per file, not once per occurrence: a file's label
// array is expected to hold distinct labels, but if a stored index ever
// repeats one, the folder line must still report how many files carry the
// label, not how many times it happened to appear.
func dirStatsOf(node map[string]any) dirStats {
	st := dirStats{labels: make(map[string]int)}
	var walk func(map[string]any)
	walk = func(n map[string]any) {
		for _, v := range n {
			switch t := v.(type) {
			case []string:
				st.files++
				seen := make(map[string]struct{}, len(t))
				for _, l := range t {
					if _, dup := seen[l]; dup {
						continue
					}
					seen[l] = struct{}{}
					st.labels[l]++
				}
			case map[string]any:
				walk(t)
			}
		}
	}
	walk(node)
	return st
}

// topLabels picks the n most frequent labels from a frequency map, breaking
// ties alphabetically. Frequency ranked, rather than the first-seen order a
// file leaf keeps, because a folder line summarizes many files and "what is
// common here" is the useful signal; alphabetical ties keep the output
// deterministic against map iteration order.
func topLabels(labels map[string]int, n int) []string {
	type kv struct {
		label string
		count int
	}
	entries := make([]kv, 0, len(labels))
	for l, c := range labels {
		entries = append(entries, kv{l, c})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].label < entries[j].label
	})
	if len(entries) > n {
		entries = entries[:n]
	}
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.label
	}
	return out
}

// renderProjectMap renders the tree, degrading to a label-less outline if the
// result would not fit the budget.
//
// Collapsing every folder bounds the output by how wide one level is, so the
// budget tripping means something pathological — most plausibly thousands of
// loose files in a single flat directory, where there is no folder to hide them
// behind. When it does trip, labels go wholesale and the agent is told how to
// get them back for the part of the tree it actually cares about.
func renderProjectMap(tree map[string]any, subdir string) string {
	// Derived from the tree rather than passed in, so the total can never
	// disagree with the folder counts printed underneath it — they are computed
	// by the same walk over the same leaves.
	total := dirStatsOf(tree).files

	var b strings.Builder
	// The total goes first because it is the one number the model cannot work
	// out for itself: the level below shows a handful of folder counts, and
	// summing them to find out how big the project is costs a step and gets it
	// wrong whenever loose files sit at this level too.
	if subdir == "" {
		fmt.Fprintf(&b, "%s indexed in this project.\n", fileCount(total))
	} else {
		fmt.Fprintf(&b, "%s indexed under %q.\n", fileCount(total), subdir)
	}
	b.WriteString("Folders end in \"/\" and are shown as ONE line each: the folder's most common topic labels and the number of files inside it. Files at this level are listed individually with their own labels. To see inside a folder, call again with subdir set to its path.\n\n")
	renderProjectLevel(tree, &b, true)

	if b.Len() <= projectMapBudget {
		return b.String()
	}

	scope := "a subdirectory"
	if subdir == "" {
		scope = "a subdirectory (e.g. subdir=\"internal/auth\")"
	}

	b.Reset()
	fmt.Fprintf(&b, "%s indexed here.\n", fileCount(total))
	b.WriteString("Folders end in \"/\". This level is too wide to show topic labels, so only the names are listed.\n")
	fmt.Fprintf(&b, "Call codebase_map again scoped to %s to get labels for the files there.\n\n", scope)
	renderProjectLevel(tree, &b, false)
	return b.String()
}

// renderProjectLevel writes exactly one level of the tree: every subdirectory
// as a single summary line carrying its most common topic labels and the number
// of files beneath it, and every loose file at this level with its own labels.
//
// One level, never recursive, and no size threshold: a folder is a folder
// whether it holds three files or three thousand. That makes the map's cost a
// function of how wide the current level is, not how large the project is
// underneath it — the root of a 5,000-file monorepo costs the same handful of
// lines as a toy repo. subdir is the only way down, so descending is an
// explicit choice the model makes one directory at a time, paying only for the
// branch it actually cares about.
//
// An earlier version opened any folder holding fewer than ten files. That read
// as "the map is still dumping everything", because on a real repo most folders
// are small: the two big ones collapsed and the remaining twenty-six files
// carried 87% of the output between them.
//
// Deliberately not JSON. Measured on this repo's 277-file index, MarshalIndent
// spent 20,094 tokens carrying 64 KB of paths and labels — 1.4x the content
// itself — because it puts every one of a file's labels on its own line, each
// quoted and comma-separated. The same tree as an indented outline costs much
// less, and roughly half of that saving comes from nothing more than keeping a
// file's labels on one line.
//
// Nothing downstream unmarshals this: it is read by a model, not parsed. The
// same reasoning gave file_map its plain-text output.
func renderProjectLevel(node map[string]any, b *strings.Builder, withLabels bool) {
	// Folders first, then files, each group alphabetical. The two are different
	// kinds of thing: a folder line is somewhere to go next, a file line is
	// something to read. Interleaving them alphabetically made the reader scan
	// the whole level to find the branches, and the trailing "/" was the only
	// thing separating them.
	dirs := make([]string, 0, len(node))
	files := make([]string, 0, len(node))
	for k, v := range node {
		if _, isDir := v.(map[string]any); isDir {
			dirs = append(dirs, k)
			continue
		}
		files = append(files, k)
	}
	sort.Strings(dirs)
	sort.Strings(files)

	ordered := make([]string, 0, len(dirs)+len(files))
	ordered = append(ordered, dirs...)
	ordered = append(ordered, files...)

	for _, k := range ordered {
		switch v := node[k].(type) {
		case map[string]any:
			st := dirStatsOf(v)
			summary := "(" + fileCount(st.files) + ")"
			if withLabels && len(st.labels) > 0 {
				if top := topLabels(st.labels, folderLabelCap); len(top) > 0 {
					summary = strings.Join(top, ", ") + "  " + summary
				}
			}
			fmt.Fprintf(b, "%s/  %s\n", k, summary)
		case []string:
			if withLabels && len(v) > 0 {
				fmt.Fprintf(b, "%s  %s\n", k, strings.Join(v, ", "))
				continue
			}
			fmt.Fprintf(b, "%s\n", k)
		}
	}
}

func buildProjectTree(baseDir string, textEntries []*docindex.PageEntry, pdfEntries []*docindex.PageEntry, docxEntries []*docindex.PageEntry) map[string]any {
	root := make(map[string]any)

	// Text/code files: one entry per file, leaf = labels array.
	for _, e := range textEntries {
		rel, err := filepath.Rel(baseDir, e.DocPath)
		if err != nil {
			continue
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			// An entry outside baseDir cannot be placed under it; skip it
			// rather than render a "../" branch that does not exist.
			continue
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
		// De-duplicate after capping: a repeated label in a stored array would
		// otherwise render twice on the file's line and count twice toward the
		// folder summary, which reports files carrying a label, not occurrences.
		if len(labels) > 1 {
			seenLabel := make(map[string]struct{}, len(labels))
			// A fresh slice, not labels[:0]: that idiom is safe on its own, but
			// labels aliases the caller's PageEntry.Labels, so compacting in
			// place writes through it and leaves the entry's own label array
			// rewritten behind our back.
			deduped := make([]string, 0, len(labels))
			for _, l := range labels {
				if _, dup := seenLabel[l]; dup {
					continue
				}
				seenLabel[l] = struct{}{}
				deduped = append(deduped, l)
			}
			labels = deduped
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
			continue
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue // outside baseDir — see the text-file branch above
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
