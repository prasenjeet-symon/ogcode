package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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
// line — how many files it holds, the subfolders directly inside it, and a
// sample of topic labels drawn from across the whole branch — whatever its
// size, and the files sitting directly at that level are listed with their own
// labels. The subdir parameter is the only way down: each call re-roots the map
// at that folder, and a subfolder named on a folder line can be jumped to
// directly. Output size therefore tracks how wide a level is, never how large
// the project is beneath it.
//
// Text, code, PDF, and DOCX leaves all carry a flat topic-label array. A text or
// code file shows every label the index holds for it (the store caps that at
// MaxLabelsPerPage). For PDFs and DOCX files a subset is aggregated across
// pages; the dedicated pdf_index and docx_index tools provide the full per-page
// detail when the agent needs to decide which page to read.
type ProjectIndexTool struct {
	Store *docindex.Store
}

func NewProjectIndexTool(store *docindex.Store) ProjectIndexTool {
	return ProjectIndexTool{Store: store}
}

func (ProjectIndexTool) ID() string { return "codebase_map" }

func (ProjectIndexTool) Description() string {
	return fmt.Sprintf("Return one level of a labeled map of indexed files — text, code, PDF, and DOCX documents. Every folder is shown as a SINGLE line: how many files it holds, the subfolders directly inside it, and a sample of topic labels drawn from across the whole folder; files sitting directly at that level are listed individually with their own labels. Labels are separated by semicolons. To look inside a folder, call again with subdir set to its path from the project root (e.g. \"internal/tool\") — each call shows one level, and you can jump straight to a subfolder a folder line names. For PDFs and DOCX files a subset of labels (up to %d) is aggregated across pages (no per-page breakdown). Use this to find which area is relevant to a topic before reading anything; use pdf_index for per-page labels of a specific PDF, or docx_index for a DOCX.", docLabelCap)
}

func (ProjectIndexTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"subdir": {
				"type": "string",
				"description": "Optional subdirectory path relative to the project root, never to the level last shown (e.g. \"internal/auth\"). Roots the map at that folder: the files directly inside it are listed, and the folders inside it are each summarized on one line. A subfolder named on a folder line can be passed directly (\"internal\" lists \"auth\" → subdir \"internal/auth\"). Omit to start at the project root."
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
		_ = DecodeArgs(args, &params)
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
		// Rendered to projectMapBudget, which sits above the generic 50 KB cap,
		// so opt out of the loop's backstop: it would otherwise head-truncate the
		// tree mid-branch. The budget is what bounds this result.
		Truncated: true,
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
// document (PDF or DOCX) in the project tree. A multi-page document's labels are
// collected across every page (de-duplicated, first-seen order), so unlike a
// text file there is no natural bound and a cap is required. Set to
// MaxLabelsPerPage: deep enough that the merged list reads as a real summary of
// the document rather than a teaser. The full per-page breakdown is available
// through the dedicated pdf_index / docx_index tools.
const docLabelCap = MaxLabelsPerPage

// textLabelCap is the maximum number of topic labels shown per text/code file.
//
// Equal to MaxLabelsPerPage — the ceiling on what the index can store for a
// page — so a file's line shows every label it holds; nothing the indexer
// produced is hidden by the render. Labels still dominate the output, so the
// byte budget below (projectMapBudget) is the real limit: on a pathologically
// wide level the map sheds labels rung by rung, and the result opts out of the
// loop's generic cap (Result.Truncated) so that budget, not MaxToolOutputBytes,
// bounds it.
const textLabelCap = MaxLabelsPerPage

// projectMapBudget is the byte budget for a rendered project map.
//
// Independent of MaxToolOutputBytes, the generic 50 KB cap on any tool result:
// the map opts out of the loop's backstop (Execute marks its result Truncated),
// so this budget is the only thing that bounds a map and may sit above that
// generic cap without a large map being head-truncated mid-branch — a tree that
// simply stops, with nothing telling the model what was lost. The map degrades
// on its own terms instead: labels shed rung by rung until the level fits, and
// only a level that will not fit at one label per file falls back to the
// label-less outline.
//
// Set well above what a repo-sized map costs (a few KB with folders collapsed),
// so ordinary use never degrades. It exists for the pathological level — many
// loose files, each near the label ceiling — which would otherwise run to
// hundreds of KB.
const projectMapBudget = 100 * 1024

// folderLabelCap is the maximum number of topic labels shown on a collapsed
// folder's summary line.
//
// The labels are a sample, not a ranking: sampleFolderLabels draws them evenly
// from every part of the branch, so twenty is enough to say what a folder
// covers. It was forty, chosen by how many subfolders and then how many files
// carried each label — but an index's labels are nearly all unique, so both
// counts tied at one and the line was really an alphabetical slice: forty
// labels starting with A, B and C, filling a kilobyte and describing whichever
// files happened to sort first. The budget still bounds the level: on a wide
// one, renderProjectMap lowers this rung by rung.
const folderLabelCap = 20

// subfolderNameCap is the most subfolder names a folder line lists. They are
// the cheapest navigation the map offers — each is a subdir the agent can jump
// straight to — so the cap only stops a folder with hundreds of children from
// filling its line; past it the largest are named and the rest counted.
const subfolderNameCap = 24

// dirStats holds what folder summarization needs to know about one directory:
// how many files it contains — all descendants, not just immediate children.
type dirStats struct {
	files int
}

// dirStatsOf counts the files beneath a directory node by walking its leaves.
// A []string node value is a file (its elements are labels); a map[string]any
// is a subdirectory. Rendered counts must be identical everywhere they are
// needed — the map's opening total and every folder line both read this — so
// nothing downstream can disagree about how many files a folder holds.
func dirStatsOf(node map[string]any) dirStats {
	var st dirStats
	var walk func(map[string]any)
	walk = func(n map[string]any) {
		for _, v := range n {
			switch t := v.(type) {
			case []string:
				st.files++
			case map[string]any:
				walk(t)
			}
		}
	}
	walk(node)
	return st
}

// labelSource is one child a sampled line draws its labels from — a file or a
// subfolder on a folder line, a turn on a conversation line.
type labelSource struct {
	name   string   // stable order among equal sources
	order  int      // ahead of name among equal sizes: lower comes first
	size   int      // how many files (or turns) it stands for
	labels []string // most important first
	test   bool     // a test file or folder: counts half
}

// sampleLabels picks up to n labels for one line, drawn from every source in
// proportion to its size.
//
// Slots go one at a time to the source with the strongest claim: its weight
// over one more than the slots it already has (the D'Hondt rule), the weight
// being the square root of its size, halved for tests. So a 90-file subfolder
// gets most of a folder line, but not all of it — six loose config files
// beside it still get a word in — and no source's last label beats another
// source's first. Equal claims go to whichever source comes first in
// spreadOrder, so sixty equal files or turns are summarised by entries from
// across all sixty, not by the first twenty in name order.
//
// A ranking by how many sources carry a label cannot do this: an index's
// labels are nearly all unique, so every count ties at one and the tie-break
// decides everything. What does repeat is generic ("Temp File Cleanup",
// "Graceful Shutdown Handling") — the least useful thing a line can say about
// what it stands for.
//
// Deterministic: sources are ordered by size, then order, then name, never by
// map order.
func sampleLabels(sources []labelSource, n int) []string {
	if n <= 0 || len(sources) == 0 {
		return nil
	}
	type claim struct {
		labelSource
		weight float64
		rank   int // position in spreadOrder: the tie-break between equal claims
		taken  int
	}
	claims := make([]*claim, 0, len(sources))
	for _, src := range sources {
		if len(src.labels) > 0 {
			claims = append(claims, &claim{labelSource: src})
		}
	}
	sort.Slice(claims, func(i, j int) bool {
		if claims[i].size != claims[j].size {
			return claims[i].size > claims[j].size
		}
		if claims[i].order != claims[j].order {
			return claims[i].order < claims[j].order
		}
		return claims[i].name < claims[j].name
	})
	for pos, i := range spreadOrder(len(claims)) {
		claims[i].rank = pos
	}
	for _, c := range claims {
		c.weight = math.Sqrt(float64(c.size))
		// Tests describe what they check ("Pricing Unit Tests"), which a line
		// already says through the code they cover, and in a Go package they
		// are half the files. At half weight they speak after the code.
		if c.test {
			c.weight /= 2
		}
	}

	out := make([]string, 0, n)
	seen := make(map[string]struct{}, n)
	for len(out) < n {
		var best *claim
		for _, c := range claims {
			if c.taken >= len(c.labels) {
				continue
			}
			if best == nil {
				best = c
				continue
			}
			// c's claim beats best's when weight/(taken+1) is larger; compared
			// cross-multiplied so equal claims compare exactly equal.
			mine, theirs := c.weight*float64(best.taken+1), best.weight*float64(c.taken+1)
			if mine > theirs || (mine == theirs && c.rank < best.rank) {
				best = c
			}
		}
		if best == nil {
			break
		}
		l := best.labels[best.taken]
		best.taken++
		if _, dup := seen[l]; dup {
			continue
		}
		seen[l] = struct{}{}
		out = append(out, l)
	}
	return out
}

// sampleFolderLabels picks up to n labels that describe a folder's whole
// branch: each child offers its labels — a file its own, a subfolder its own
// sample, drawn the same way — and sampleLabels shares the line among them.
// Test files and test folders count half, so the code they cover speaks first.
func sampleFolderLabels(node map[string]any, n int) []string {
	if n <= 0 {
		return nil
	}
	sources := make([]labelSource, 0, len(node))
	for name, v := range node {
		switch t := v.(type) {
		case []string:
			sources = append(sources, labelSource{name: name, size: 1, labels: t, test: isTestPath(name)})
		case map[string]any:
			sources = append(sources, labelSource{
				name: name, size: dirStatsOf(t).files, labels: sampleFolderLabels(t, n), test: isTestPath(name),
			})
		}
	}
	return sampleLabels(sources, n)
}

// isTestPath reports whether a file or folder name marks tests, by the
// conventions of the languages the index covers: Go's _test.go, JS/TS
// .test./.spec., Python's test_*.py and *_test.py, and the usual test
// folders.
func isTestPath(name string) bool {
	lower := strings.ToLower(name)
	switch lower {
	case "test", "tests", "__tests__", "testdata", "spec", "specs":
		return true
	}
	return strings.HasSuffix(lower, "_test.go") ||
		strings.Contains(lower, ".test.") || strings.Contains(lower, ".spec.") ||
		(strings.HasPrefix(lower, "test_") && strings.HasSuffix(lower, ".py")) ||
		strings.HasSuffix(lower, "_test.py")
}

// spreadOrder returns the indexes 0..m-1 in an order whose every prefix is
// spread evenly across the range — 0, then the middle, then the quarters, and
// so on (bit-reversed counting). Taking the first k of them samples a list of
// m evenly from first to last, rather than taking its head.
func spreadOrder(m int) []int {
	bits := 0
	for 1<<bits < m {
		bits++
	}
	out := make([]int, 0, m)
	for x := 0; x < 1<<bits; x++ {
		r := 0
		for b := 0; b < bits; b++ {
			if x&(1<<b) != 0 {
				r |= 1 << (bits - 1 - b)
			}
		}
		if r < m {
			out = append(out, r)
		}
	}
	return out
}

// labelSeparator joins labels on a line. A semicolon rather than a comma: a
// label is a short phrase and some carry commas of their own ("Yamux Tunnel,
// Reverse Proxy, Subdomains"), which a comma-joined list turns into three
// labels that were never there.
const labelSeparator = "; "

// labelCleaner makes a stored label safe for a line: a semicolon inside it
// becomes a comma and a line break a space, so the label can be neither read
// as two nor break the one-line-per-entry shape.
var labelCleaner = strings.NewReplacer(";", ",", "\r\n", " ", "\n", " ", "\r", " ")

// joinLabels renders labels for one line, cleaned and semicolon-separated.
func joinLabels(labels []string) string {
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		if c := strings.TrimSpace(labelCleaner.Replace(l)); c != "" {
			out = append(out, c)
		}
	}
	return strings.Join(out, labelSeparator)
}

// subfolders returns the names of a node's immediate subdirectories for its
// folder line, alphabetical, and how many were left off. Past subfolderNameCap
// the largest are kept — they are the likeliest next step.
func subfolders(node map[string]any) (names []string, more int) {
	type sub struct {
		name  string
		files int
	}
	var subs []sub
	for k, v := range node {
		if child, isDir := v.(map[string]any); isDir {
			subs = append(subs, sub{k, dirStatsOf(child).files})
		}
	}
	if len(subs) > subfolderNameCap {
		sort.Slice(subs, func(i, j int) bool {
			if subs[i].files != subs[j].files {
				return subs[i].files > subs[j].files
			}
			return subs[i].name < subs[j].name
		})
		more = len(subs) - subfolderNameCap
		subs = subs[:subfolderNameCap]
	}
	for _, sb := range subs {
		names = append(names, sb.name)
	}
	sort.Strings(names)
	return names, more
}

// subdirExample is a real path the header can offer as the next call: the
// largest folder at this level, and its largest subfolder when it has one —
// always from the project root, since subdir never resolves against the level
// last shown. "" when the level has no folders.
func subdirExample(tree map[string]any, subdir string) string {
	largest := func(node map[string]any) (string, map[string]any) {
		best, bestFiles := "", -1
		var bestNode map[string]any
		for k, v := range node {
			child, isDir := v.(map[string]any)
			if !isDir {
				continue
			}
			f := dirStatsOf(child).files
			if f > bestFiles || (f == bestFiles && k < best) {
				best, bestFiles, bestNode = k, f, child
			}
		}
		return best, bestNode
	}
	name, node := largest(tree)
	if name == "" {
		return ""
	}
	var parts []string
	if s := strings.Trim(filepath.ToSlash(subdir), "/"); s != "" {
		parts = append(parts, s)
	}
	parts = append(parts, name)
	if sub, _ := largest(node); sub != "" {
		parts = append(parts, sub)
	}
	return strings.Join(parts, "/")
}

// renderProjectMap renders the tree, degrading gracefully if the result would
// not fit the budget.
//
// Collapsing every folder bounds the output by how wide one level is, so a
// tripped budget means a level with many entries — enough loose files, or
// enough folder lines, that even a handful of labels each overflows. The
// response is to render again at a progressively shallower cap, on both files
// and folder lines, so a wide level shows fewer labels rather than losing them
// wholesale: the model still learns what each file and folder covers, and opens
// the branches it cares about. Dropping labels entirely is the last resort, for
// a level where even one label per entry will not fit.
func renderProjectMap(tree map[string]any, subdir string) string {
	// Derived from the tree rather than passed in, so the total can never
	// disagree with the folder counts printed underneath it — they are computed
	// by the same walk over the same leaves.
	total := dirStatsOf(tree).files

	// The way down, with a real path from this level as the example. subdir is
	// always from the project root: a drill-down lists "tool/", and the call
	// that opens it names "internal/tool" — the one step a model gets wrong when
	// the header just says "its path".
	nextStep := "\n"
	if ex := subdirExample(tree, subdir); ex != "" {
		nextStep = fmt.Sprintf("To look inside a folder, call again with subdir set to its path from the project root — a folder listed here, or one of the subfolders it names (e.g. subdir=%q).\n\n", ex)
	}

	// Full depth first, then progressively shallower — both a loose file's
	// labels and a folder line's, since either can be the bulk of a wide level.
	// The floor rung is 1 label each; past it the label-less outline below takes
	// over.
	for _, rung := range []struct{ fileCap, folderCap int }{
		{textLabelCap, folderLabelCap},
		{10, 10},
		{3, 3},
		{1, 1},
	} {
		var b strings.Builder
		// The total goes first because it is the one number the model cannot
		// work out for itself: the level below shows a handful of folder counts,
		// and summing them to find out how big the project is costs a step and
		// gets it wrong whenever loose files sit at this level too.
		if subdir == "" {
			fmt.Fprintf(&b, "%s indexed in this project.\n", fileCount(total))
		} else {
			fmt.Fprintf(&b, "%s indexed under %q.\n", fileCount(total), subdir)
		}
		b.WriteString("Each folder is ONE line: its name ending in \"/\"; in parentheses, how many files it holds and the subfolders directly inside it; then, after \"—\", a sample of topic labels drawn from across the whole folder. Files at this level are listed with their own labels after \"—\". Labels are separated by \";\".\n")
		b.WriteString(nextStep)
		renderProjectLevel(tree, &b, true, rung.fileCap, rung.folderCap)
		if b.Len() <= projectMapBudget {
			return b.String()
		}
	}

	scope := "a subdirectory"
	if ex := subdirExample(tree, subdir); ex != "" {
		scope = fmt.Sprintf("a subdirectory (e.g. subdir=%q)", ex)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s indexed here.\n", fileCount(total))
	b.WriteString("Folders end in \"/\", with their file count and subfolders in parentheses. This level is too wide to show topic labels, so only the names are listed.\n")
	fmt.Fprintf(&b, "Call codebase_map again scoped to %s to get labels for the files there.\n\n", scope)
	renderProjectLevel(tree, &b, false, 0, 0)
	return b.String()
}

// renderProjectLevel writes exactly one level of the tree: every subdirectory
// as a single summary line — its file count and subfolders in parentheses, then
// a sample of topic labels from across it — and every loose file at this level
// with its own labels:
//
//	internal/ (472 files; subfolders: agent, bus, cli) — Agent Loop; Event Bus
//	main.go — CLI Entrypoint; Flag Parsing
//
// The count and the subfolders lead because they are what a model needs first
// to choose where to go; the labels trail, where a long list does not bury
// them.
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
//
// fileCap and folderCap bound how many labels a loose file and a folder line
// show. renderProjectMap lowers both rung by rung when a level will not fit the
// budget: a folder line is one line whatever the branch holds, but on a level
// with many folders those lines are the bulk, so they shed depth alongside the
// files. The last resort (withLabels false) passes 0 for both.
func renderProjectLevel(node map[string]any, b *strings.Builder, withLabels bool, fileCap, folderCap int) {
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
			meta := fileCount(dirStatsOf(v).files)
			if names, more := subfolders(v); len(names) > 0 {
				list := strings.Join(names, ", ")
				if more > 0 {
					list += fmt.Sprintf(", +%d more", more)
				}
				meta += "; subfolders: " + list
			}
			line := fmt.Sprintf("%s/ (%s)", k, meta)
			if withLabels {
				if labels := joinLabels(sampleFolderLabels(v, folderCap)); labels != "" {
					line += " — " + labels
				}
			}
			b.WriteString(line + "\n")
		case []string:
			labels := v
			if len(labels) > fileCap {
				labels = labels[:fileCap]
			}
			if withLabels {
				if joined := joinLabels(labels); joined != "" {
					fmt.Fprintf(b, "%s — %s\n", k, joined)
					continue
				}
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
