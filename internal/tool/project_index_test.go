package tool

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/docindex"
)

func textEntry(path string, labels ...string) *docindex.PageEntry {
	return &docindex.PageEntry{DocPath: path, PageNum: 1, Labels: labels}
}

// The map is an indented outline, not JSON: folders carry a trailing slash and
// a file's labels stay on the file's own line. Keeping labels inline is where
// roughly half the token saving over MarshalIndent comes from.
func TestProjectIndex_CollapsesEveryFolderAndListsLooseFiles(t *testing.T) {
	tree := buildProjectTree("/proj", []*docindex.PageEntry{
		textEntry("/proj/internal/tool/read.go", "file reading", "line offsets"),
		textEntry("/proj/internal/tool/edit.go", "string replace"),
		textEntry("/proj/main.go", "entrypoint"),
	}, nil, nil)

	out := renderProjectMap(tree, "")

	// internal/ holds only two files and still collapses: size is not the
	// criterion, being a folder is. Its labels merge up from the whole branch.
	if !strings.Contains(out, "internal/  file reading, line offsets, string replace  (2 files)") {
		t.Errorf("folder not collapsed to a single line:\n%s", out)
	}
	// A file sitting at the rendered level is listed with its own labels.
	if !strings.Contains(out, "main.go  entrypoint") {
		t.Errorf("loose file at this level not listed:\n%s", out)
	}
	// Nothing below the first level appears — that is what subdir is for.
	for _, leaked := range []string{"read.go", "edit.go", "tool/"} {
		if strings.Contains(out, leaked) {
			t.Errorf("output descended past one level, leaked %q:\n%s", leaked, out)
		}
	}
	for _, unwanted := range []string{"{", "}", "\": ["} {
		if strings.Contains(out, unwanted) {
			t.Errorf("output still carries JSON syntax %q:\n%s", unwanted, out)
		}
	}
}

// A folder's line merges the labels of everything beneath it, ranked by how
// many files carry each. This is the contract that bounds the map by how wide
// a level is rather than by how many files the project holds.
func TestProjectIndex_CollapsedFolderMergesLabels(t *testing.T) {
	// All three labels tie at 4 files each, so they order alphabetically.
	entries := make([]*docindex.PageEntry, 0, 12)
	for i := 0; i < 12; i++ {
		label := "shared topic"
		switch {
		case i%3 == 0:
			label = "alpha topic"
		case i%3 == 1:
			label = "gamma topic"
		}
		entries = append(entries, textEntry(fmt.Sprintf("/proj/pkg/file%02d.go", i), label))
	}
	tree := buildProjectTree("/proj", entries, nil, nil)

	out := renderProjectMap(tree, "")

	if !strings.Contains(out, "pkg/  alpha topic, gamma topic, shared topic  (12 files)") {
		t.Errorf("large folder not summarized correctly:\n%s", out)
	}
	if strings.Contains(out, "file00.go") {
		t.Errorf("collapsed folder still lists loose files:\n%s", out)
	}
}

// Frequency decides which labels survive on a folder line — not what came
// first — and ties break alphabetically. Map iteration order must not leak
// into the output.
func TestProjectIndex_FolderLabelsRankedByFrequency(t *testing.T) {
	// Counts: zebra 12 (last file adds a second occurrence), yak 11.
	entries := make([]*docindex.PageEntry, 0, 12)
	for i := 0; i < 11; i++ {
		entries = append(entries, textEntry(fmt.Sprintf("/proj/pkg/f%02d.go", i), "zebra", "yak"))
	}
	entries = append(entries, textEntry("/proj/pkg/extra.go", "zebra"))

	tree := buildProjectTree("/proj", entries, nil, nil)
	out := renderProjectMap(tree, "")

	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "pkg/") && strings.Contains(l, "files)") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("no collapsed line for pkg/:\n%s", out)
	}
	if !strings.Contains(line, "zebra") || !strings.Contains(line, "yak") {
		t.Errorf("collapsed line lost a label:\n%s", line)
	}
	// Both labels are within folderLabelCap (6) so both appear, in frequency
	// order: zebra (12) before yak (11).
	if strings.Index(line, "yak") < strings.Index(line, "zebra") {
		t.Errorf("frequency ranking ignored — yak (11) before zebra (12):\n%s", line)
	}
}

// An unlabeled branch still renders: a loose file keeps its name, a collapsed
// folder keeps its count — knowing the branch exists is the point.
func TestProjectIndex_UnlabeledFileAndFolderStillListed(t *testing.T) {
	entries := []*docindex.PageEntry{textEntry("/proj/empty.go")}
	for i := 0; i < 11; i++ {
		entries = append(entries, textEntry(fmt.Sprintf("/proj/pkg/f%02d.go", i)))
	}
	tree := buildProjectTree("/proj", entries, nil, nil)

	out := renderProjectMap(tree, "")

	if !strings.Contains(out, "empty.go") {
		t.Errorf("unlabeled file dropped from the map:\n%s", out)
	}
	if !strings.Contains(out, "pkg/  (11 files)") {
		t.Errorf("unlabeled collapsed folder lost its file count:\n%s", out)
	}
}

// drilling into a folder re-roots the tree there: the target lists in full
// (up to the threshold), large children inside it stay summarized, and the
// redundant path prefix from the old baseDir behaviour is gone — paths inside
// the drill-down read from the target folder, not from the project root.
func TestProjectIndex_SubdirExpandsTargetAndDropsPrefix(t *testing.T) {
	entries := make([]*docindex.PageEntry, 0, 28)
	// internal/tool: 12 files → large inside its own subdir call.
	for i := 0; i < 12; i++ {
		entries = append(entries, textEntry(fmt.Sprintf("/proj/internal/tool/f%02d.go", i), "tool topic"))
	}
	// internal/agent: 12 files.
	for i := 0; i < 12; i++ {
		entries = append(entries, textEntry(fmt.Sprintf("/proj/internal/agent/g%02d.go", i), "agent topic"))
	}
	// A loose file at the project root — must be absent from the drill-down.
	entries = append(entries, textEntry("/proj/top.go", "root topic"))

	// Simulate the store's prefix filter for subdir="internal/tool".
	var scoped []*docindex.PageEntry
	for _, e := range entries {
		path := e.DocPath
		if strings.HasPrefix(path, "/proj/internal/tool/") {
			scoped = append(scoped, e)
		}
	}

	tree := buildProjectTree("/proj/internal/tool", scoped, nil, nil)
	out := renderProjectMap(tree, "internal/tool")

	if strings.Contains(out, "top.go") {
		t.Errorf("drill-down leaked files outside the target folder:\n%s", out)
	}
	if strings.Contains(out, "internal/tool/") {
		t.Errorf("drill-down kept the redundant path prefix:\n%s", out)
	}
	if !strings.Contains(out, "f00.go  tool topic") {
		t.Errorf("target folder's files not listed:\n%s", out)
	}
}

// PDF and DOCX pages aggregate into the same leaf shape as text files before
// folder summarization sees them, so one branch renders uniformly whatever the
// file types inside it.
func TestProjectIndex_MixedDocumentTypesAggregateIntoFolderStats(t *testing.T) {
	texts := []*docindex.PageEntry{
		textEntry("/proj/docs/a.go", "paper topic", "extra topic"),
		textEntry("/proj/docs/b.go", "paper topic", "extra topic"),
	}
	pdf := []*docindex.PageEntry{
		{DocPath: "/proj/docs/report.pdf", PageNum: 1, Labels: []string{"paper topic"}},
		{DocPath: "/proj/docs/report.pdf", PageNum: 2, Labels: []string{"paper topic"}},
	}
	docx := []*docindex.PageEntry{
		{DocPath: "/proj/docs/notes.docx", PageNum: 1, Labels: []string{"paper topic"}},
	}

	// Rooted at docs/ — the drill-down a subdir call produces — so the
	// documents are the loose files of the rendered level and each leaf shows.
	out := renderProjectMap(buildProjectTree("/proj/docs", texts, pdf, docx), "docs")

	if !strings.Contains(out, "report.pdf  paper topic") || !strings.Contains(out, "notes.docx  paper topic") {
		t.Errorf("document leaves missing from the map:\n%s", out)
	}
	if !strings.Contains(out, "paper topic, extra topic") {
		t.Errorf("text file labels missing:\n%s", out)
	}

	// One level up they are a single folder line whose count spans all types.
	rolled := renderProjectMap(buildProjectTree("/proj", texts, pdf, docx), "")
	if !strings.Contains(rolled, "docs/") || !strings.Contains(rolled, "(4 files)") {
		t.Errorf("mixed document types did not roll up into one folder line:\n%s", rolled)
	}
}

// Labels are the bulk of this output, so text/code files are capped.
func TestProjectIndex_CapsTextFileLabels(t *testing.T) {
	many := []string{"one", "two", "three", "four", "five", "six", "seven", "eight"}
	tree := buildProjectTree("/proj", []*docindex.PageEntry{textEntry("/proj/a.go", many...)}, nil, nil)

	out := renderProjectMap(tree, "")

	if !strings.Contains(out, "a.go  one, two, three, four, five") {
		t.Errorf("expected exactly %d label(s):\n%s", textLabelCap, out)
	}
	if strings.Contains(out, "six") {
		t.Errorf("label cap not applied:\n%s", out)
	}
}

// bigIndex fabricates a project of n files with realistically long labels
// (~30 chars): 20 packages × n/20 files each — at 100+ files every package
// sits past the collapse threshold, so the render is bounded by folders, not
// files.
func bigIndex(n int) []*docindex.PageEntry {
	entries := make([]*docindex.PageEntry, 0, n)
	for i := 0; i < n; i++ {
		labels := make([]string, 8)
		for j := range labels {
			labels[j] = fmt.Sprintf("subsystem behaviour topic %d-%d", i, j)
		}
		entries = append(entries,
			textEntry(fmt.Sprintf("/proj/internal/pkg%02d/file%03d.go", i%20, i), labels...))
	}
	return entries
}

// With collapsing, the map must stay inside MaxToolOutputBytes at every size —
// the 2000-file render now costs a few KB, not tens of KB — and folders
// summarize rather than leak loose file names.
func TestProjectIndex_StaysUnderOutputCap(t *testing.T) {
	for _, files := range []int{100, 300, 800, 2000} {
		t.Run(fmt.Sprintf("%dfiles", files), func(t *testing.T) {
			tree := buildProjectTree("/proj", bigIndex(files), nil, nil)
			out := renderProjectMap(tree, "")

			if len(out) > MaxToolOutputBytes {
				t.Errorf("map is %d bytes, over the %d cap — it will be truncated by the backstop",
					len(out), MaxToolOutputBytes)
			}
			// With one call = one expanded level, the root's large child
			// (internal/, many packages) is itself summarized — expanding
			// further is what a subdir call is for. What must survive is the
			// summary structure itself, at every size.
			if !strings.Contains(out, "internal/") || !strings.Contains(out, "files)") {
				t.Errorf("map lost its folder summaries at %d files", files)
			}
		})
	}
}

// Past the budget the labels go, not the structure — and the agent is told how
// to get them back rather than being left with a silently shorter tree. With
// collapsing this path needs a pathological tree, so the fabricated index is
// one flat directory of 2000 loose files: no structural cut exists there.
func TestProjectIndex_LargeProjectDropsLabelsWithGuidance(t *testing.T) {
	flat := make([]*docindex.PageEntry, 0, 2000)
	for i := 0; i < 2000; i++ {
		flat = append(flat, textEntry(fmt.Sprintf("/proj/f%04d.go", i), "subsystem behaviour topic 0-0"))
	}

	smallHasLabels := renderProjectMap(buildProjectTree("/proj", bigIndex(50), nil, nil), "")
	large := renderProjectMap(buildProjectTree("/proj", flat, nil, nil), "")

	if !strings.Contains(smallHasLabels, "subsystem behaviour topic 0-0") {
		t.Error("a small project should still show labels")
	}
	if strings.Contains(large, "subsystem behaviour topic 0-0") {
		t.Error("an oversized project should drop labels, not keep them")
	}
	if !strings.Contains(large, "subdir=") {
		t.Errorf("oversized map does not tell the agent how to get labels back:\n%s", large[:300])
	}
}

// A repo-sized project must keep its labels — they are what makes this tool
// more than a file listing, and dropping them is the heaviest loss available.
//
// This pins the budget against being kept too conservative. With collapsing a
// repo-sized map is a few KB, so the budget could safely be much lower; the
// test guards against the reverse mistake with the old flat 280-file render.
func TestProjectIndex_RepoSizedProjectKeepsLabels(t *testing.T) {
	out := renderProjectMap(buildProjectTree("/proj", bigIndex(280), nil, nil), "")

	if strings.Contains(out, "too large to show topic labels") {
		t.Errorf("a 280-file project dropped its labels at %d bytes, well inside the %d cap",
			len(out), MaxToolOutputBytes)
	}
	if !strings.Contains(out, "subsystem behaviour topic 0-0") {
		t.Error("labels missing from a repo-sized map")
	}
}

// A label repeated within one file's stored array must neither render twice on
// the file's line nor count twice toward its folder's summary — the folder
// line reports files carrying a label, not occurrences of one.
func TestProjectIndex_DuplicateLabelsCountedOncePerFile(t *testing.T) {
	entries := []*docindex.PageEntry{
		textEntry("/proj/pkg/a.go", "shared topic", "shared topic", "other topic"),
		textEntry("/proj/pkg/b.go", "shared topic"),
	}
	// Rooted at pkg/ so the files are this level's loose files and their own
	// lines are visible.
	out := renderProjectMap(buildProjectTree("/proj/pkg", entries, nil, nil), "pkg")

	if strings.Contains(out, "a.go  shared topic, shared topic") {
		t.Errorf("duplicate label rendered twice on the file line:\n%s", out)
	}
	if !strings.Contains(out, "a.go  shared topic, other topic") {
		t.Errorf("dedup reordered or dropped unique labels:\n%s", out)
	}

	// And the folder line counts "shared topic" once per file (2), not once
	// per occurrence (3), so a repeated label cannot inflate a folder's rank.
	rolled := renderProjectMap(buildProjectTree("/proj", entries, nil, nil), "")
	if !strings.Contains(rolled, "pkg/  shared topic, other topic  (2 files)") {
		t.Errorf("folder summary miscounted a repeated label:\n%s", rolled)
	}
}

// The store's prefix filter must respect the directory boundary: a sibling
// whose name merely shares the target's string prefix ("tool" vs "toolbox")
// stays out of the drill-down, and a LIKE wildcard inside a folder name
// ("my_dir") cannot widen the match. Exercises the real SQLite store, since
// the drill-down's correctness is the store's LIKE semantics, not the tree
// builder's.
func TestStore_PrefixFilterRespectsDirectoryBoundary(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	s := docindex.NewStore(database)

	entries := []*docindex.PageEntry{
		{DocPath: "/proj/internal/tool/read.go", PageNum: 1, Labels: []string{"tool topic"}},
		{DocPath: "/proj/internal/toolbox/spanner.go", PageNum: 1, Labels: []string{"toolbox topic"}},
		{DocPath: "/proj/internal/my_dir/needle.go", PageNum: 1, Labels: []string{"needle topic"}},
		{DocPath: "/proj/internal/agent/loop.go", PageNum: 1, Labels: []string{"agent topic"}},
		{DocPath: "/other/c.go", PageNum: 1, Labels: []string{"other topic"}},
	}
	for _, e := range entries {
		if err := s.Upsert(e); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	// "tool" must not match "toolbox"; "_" must not act as a wildcard.
	// Presence is asserted too: with ESCAPE missing, the escaped pattern
	// matches nothing in SQLite (there is no default escape character) and an
	// absence-only check would pass against an empty result.
	for _, tc := range []struct{ prefix, present, missing string }{
		{"/proj/internal/tool", "/proj/internal/tool/read.go", "/proj/internal/toolbox/spanner.go"},
		{"/proj/internal/my_dir", "/proj/internal/my_dir/needle.go", "/proj/internal/tool/read.go"},
	} {
		paths, err := s.ListDocPaths(tc.prefix)
		if err != nil {
			t.Fatalf("ListDocPaths(%q): %v", tc.prefix, err)
		}
		if len(paths) != 1 || paths[0] != tc.present {
			t.Errorf("prefix %q matched %v, want exactly [%s]", tc.prefix, paths, tc.present)
		}
	}

	// Root prefixes must keep matching files directly inside them.
	root, err := s.ListDocPaths("/proj")
	if err != nil {
		t.Fatalf("ListDocPaths(/proj): %v", err)
	}
	if len(root) != 4 {
		t.Errorf("root prefix lost direct children, got %v", root)
	}
}

// subdir is a model-supplied string, and filepath.Join cleans its result — so
// "../other-project" resolves to a sibling of the session directory. The doc
// index is one store for the whole machine, so without a boundary check that
// call lists another project's paths and topic labels.
func TestProjectIndex_SubdirCannotEscapeTheProject(t *testing.T) {
	const root = "/Users/me/projects/current"

	inside := []struct{ subdir, want string }{
		{"internal/auth", "/Users/me/projects/current/internal/auth"},
		{"internal/../internal/tool", "/Users/me/projects/current/internal/tool"},
		{".", "/Users/me/projects/current"},
		// An absolute-looking subdir is joined, not honoured, so it stays inside.
		{"/etc/secrets", "/Users/me/projects/current/etc/secrets"},
	}
	for _, tc := range inside {
		got, ok := resolveSubdirPrefix(root, tc.subdir)
		if !ok {
			t.Errorf("resolveSubdirPrefix(%q) rejected a path inside the project", tc.subdir)
			continue
		}
		if got != tc.want {
			t.Errorf("resolveSubdirPrefix(%q) = %q, want %q", tc.subdir, got, tc.want)
		}
	}

	for _, subdir := range []string{"..", "../other-project", "../../../etc", "internal/../../sibling"} {
		if got, ok := resolveSubdirPrefix(root, subdir); ok {
			t.Errorf("resolveSubdirPrefix(%q) = %q, want rejection — it escapes the project", subdir, got)
		}
	}

	// No session directory means the tool is already unscoped; there is no
	// boundary to enforce and the join is used as-is.
	if _, ok := resolveSubdirPrefix("", "anything"); !ok {
		t.Error("an empty session dir has no boundary to enforce, so nothing should be rejected")
	}
}

// buildProjectTree caps and de-duplicates a file's labels. It must not do that
// by compacting the caller's slice in place: labels aliases PageEntry.Labels,
// so an in-place filter rewrites the entry the caller still owns.
func TestProjectIndex_BuildTreeLeavesCallerLabelsIntact(t *testing.T) {
	entry := &docindex.PageEntry{
		DocPath: "/proj/a.go",
		PageNum: 1,
		Labels:  []string{"auth", "auth", "http", "http", "db"},
	}
	original := append([]string(nil), entry.Labels...)

	buildProjectTree("/proj", []*docindex.PageEntry{entry}, nil, nil)

	if len(entry.Labels) != len(original) {
		t.Fatalf("entry.Labels length changed: got %d, want %d", len(entry.Labels), len(original))
	}
	for i := range original {
		if entry.Labels[i] != original[i] {
			t.Errorf("entry.Labels[%d] = %q, want %q — the tree build wrote through the caller's slice",
				i, entry.Labels[i], original[i])
		}
	}
}

// A folder holding one file reads "(1 file)", not "(1 files)". The map is
// prose a model reads, and every folder now carries this line.
func TestProjectIndex_SingleFileFolderReadsSingular(t *testing.T) {
	tree := buildProjectTree("/proj", []*docindex.PageEntry{
		textEntry("/proj/.vscode/settings.json", "editor config"),
		textEntry("/proj/pkg/a.go", "one"),
		textEntry("/proj/pkg/b.go", "two"),
	}, nil, nil)

	out := renderProjectMap(tree, "")

	if !strings.Contains(out, "(1 file)") || strings.Contains(out, "(1 files)") {
		t.Errorf("single-file folder should read \"(1 file)\":\n%s", out)
	}
	if !strings.Contains(out, "(2 files)") {
		t.Errorf("multi-file folder should stay plural:\n%s", out)
	}
}

// Folders are listed before files, each group alphabetical. A folder line is
// somewhere to go next and a file line is something to read; interleaving them
// made the reader scan the whole level to find the branches.
func TestProjectIndex_FoldersListedBeforeFiles(t *testing.T) {
	tree := buildProjectTree("/proj", []*docindex.PageEntry{
		// Names chosen so plain alphabetical ordering would interleave them:
		// alpha.md, beta/, gamma.md, delta/ sorts as alpha, beta, delta, gamma.
		textEntry("/proj/alpha.md", "root doc"),
		textEntry("/proj/gamma.md", "another doc"),
		textEntry("/proj/beta/x.go", "beta topic"),
		textEntry("/proj/delta/y.go", "delta topic"),
	}, nil, nil)

	out := renderProjectMap(tree, "")

	idx := func(needle string) int { return strings.Index(out, needle) }
	beta, delta := idx("beta/"), idx("delta/")
	alpha, gamma := idx("alpha.md"), idx("gamma.md")
	for _, missing := range []struct {
		name string
		at   int
	}{{"beta/", beta}, {"delta/", delta}, {"alpha.md", alpha}, {"gamma.md", gamma}} {
		if missing.at < 0 {
			t.Fatalf("%s missing from the map:\n%s", missing.name, out)
		}
	}
	if beta > delta {
		t.Errorf("folders not alphabetical:\n%s", out)
	}
	if alpha > gamma {
		t.Errorf("files not alphabetical:\n%s", out)
	}
	if delta > alpha {
		t.Errorf("a file was listed before a folder:\n%s", out)
	}
}

// The result title is read next to the output, so it must describe what was
// listed. "(332 files)" beside a 19-line map reads as "this call pulled in 332
// files" — the opposite of what collapsing folders achieves.
func TestProjectIndex_TitleDescribesWhatWasListed(t *testing.T) {
	tree := buildProjectTree("/proj", []*docindex.PageEntry{
		textEntry("/proj/internal/a.go", "x"),
		textEntry("/proj/internal/b.go", "x"),
		textEntry("/proj/web/c.ts", "y"),
		textEntry("/proj/main.go", "z"),
	}, nil, nil)

	got := levelSummary(tree, 4)
	if got != "2 folders, 1 file (4 indexed)" {
		t.Errorf("levelSummary = %q, want %q", got, "2 folders, 1 file (4 indexed)")
	}

	// Folders only — no dangling ", 0 files".
	dirsOnly := buildProjectTree("/proj", []*docindex.PageEntry{
		textEntry("/proj/internal/a.go", "x"),
	}, nil, nil)
	if got := levelSummary(dirsOnly, 1); got != "1 folder (1 indexed)" {
		t.Errorf("levelSummary(folders only) = %q, want %q", got, "1 folder (1 indexed)")
	}

	if got := levelSummary(map[string]any{}, 0); got != "empty" {
		t.Errorf("levelSummary(empty) = %q, want %q", got, "empty")
	}
}

// The map's opening line states how many files the level covers. It is the one
// number the model cannot derive: the folder lines below give a handful of
// counts, and summing them is both a step of work and wrong whenever loose
// files sit at the same level.
func TestProjectIndex_OutputStatesTheIndexedTotal(t *testing.T) {
	entries := []*docindex.PageEntry{
		textEntry("/proj/internal/a.go", "x"),
		textEntry("/proj/internal/b.go", "x"),
		textEntry("/proj/web/c.ts", "y"),
		textEntry("/proj/main.go", "z"), // loose, so folder counts alone under-report
	}
	tree := buildProjectTree("/proj", entries, nil, nil)

	root := renderProjectMap(tree, "")
	if !strings.Contains(root, "4 files indexed in this project.") {
		t.Errorf("root map does not state the total:\n%s", root)
	}

	// A drill-down reports its own scope, named, so two calls in one transcript
	// cannot be confused for each other.
	scoped := renderProjectMap(buildProjectTree("/proj/internal", entries[:2], nil, nil), "internal")
	if !strings.Contains(scoped, `2 files indexed under "internal".`) {
		t.Errorf("scoped map does not state its own total:\n%s", scoped)
	}

	// Singular reads as "1 file", both in the header and on a folder line.
	one := renderProjectMap(buildProjectTree("/proj", entries[3:], nil, nil), "")
	if !strings.Contains(one, "1 file indexed in this project.") || strings.Contains(one, "1 files") {
		t.Errorf("singular total misspelled:\n%s", one)
	}
}
