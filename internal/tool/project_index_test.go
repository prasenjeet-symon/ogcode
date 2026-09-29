package tool

import (
	"context"
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
	// criterion, being a folder is. The count and its subfolders lead, then its
	// labels, drawn up from the whole branch.
	if !strings.Contains(out, "internal/ (2 files; subfolders: tool) — string replace; file reading; line offsets") {
		t.Errorf("folder not collapsed to a single line:\n%s", out)
	}
	// A file sitting at the rendered level is listed with its own labels.
	if !strings.Contains(out, "main.go — entrypoint") {
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

// A folder's line merges the labels of everything beneath it, each label once.
// This is the contract that bounds the map by how wide a level is rather than
// by how many files the project holds.
func TestProjectIndex_CollapsedFolderMergesLabels(t *testing.T) {
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

	line := folderLine(t, out, "pkg/")
	if !strings.HasPrefix(line, "pkg/ (12 files) — ") {
		t.Errorf("large folder not summarized as one line:\n%s", out)
	}
	if got := lineLabels(line); len(got) != 3 {
		t.Errorf("folder line labels = %q, want the three distinct labels once each", got)
	}
	if strings.Contains(out, "file00.go") {
		t.Errorf("collapsed folder still lists loose files:\n%s", out)
	}
}

// A folder line samples every part of the folder, not its alphabetical head.
// An index's labels are nearly all unique, so a ranking by how many files carry
// a label ties everywhere and its tie-break picks: that is how a folder of
// sixty files came to be described by the twenty that sorted first.
func TestProjectIndex_FolderLineSamplesTheWholeFolder(t *testing.T) {
	entries := make([]*docindex.PageEntry, 0, 60)
	for i := 0; i < 60; i++ {
		entries = append(entries, textEntry(fmt.Sprintf("/proj/pkg/f%02d.go", i), fmt.Sprintf("topic%02d", i)))
	}
	line := folderLine(t, renderProjectMap(buildProjectTree("/proj", entries, nil, nil), ""), "pkg/")

	labels := lineLabels(line)
	if len(labels) != folderLabelCap {
		t.Fatalf("folder line carries %d labels, want %d:\n%s", len(labels), folderLabelCap, line)
	}
	var early, middle, late bool
	for _, l := range labels {
		var n int
		fmt.Sscanf(l, "topic%d", &n)
		switch {
		case n < 20:
			early = true
		case n < 40:
			middle = true
		default:
			late = true
		}
	}
	if !early || !middle || !late {
		t.Errorf("labels come from only part of the folder (early %v, middle %v, late %v):\n%s", early, middle, late, line)
	}
}

// Slots go by size, but not all of them: a 20-file subfolder takes most of
// the line, and the two one-file subfolders beside it still get a word in —
// a small but distinctive child is not crowded out by a big one.
func TestProjectIndex_SmallSubfolderStillGetsAWord(t *testing.T) {
	entries := make([]*docindex.PageEntry, 0, 22)
	for i := 0; i < 20; i++ {
		entries = append(entries, textEntry(fmt.Sprintf("/proj/pkg/a/f%02d.go", i), fmt.Sprintf("bulk topic %02d", i)))
	}
	entries = append(entries, textEntry("/proj/pkg/b/one.go", "small b topic"))
	entries = append(entries, textEntry("/proj/pkg/c/two.go", "small c topic"))

	line := folderLine(t, renderProjectMap(buildProjectTree("/proj", entries, nil, nil), ""), "pkg/")
	labels := lineLabels(line)

	bulk := 0
	for _, l := range labels {
		if strings.HasPrefix(l, "bulk") {
			bulk++
		}
	}
	if !strings.Contains(line, "small b topic") || !strings.Contains(line, "small c topic") {
		t.Errorf("a one-file subfolder was crowded out:\n%s", line)
	}
	if bulk <= len(labels)/2 {
		t.Errorf("the 20-file subfolder got %d of %d labels, want most of the line:\n%s", bulk, len(labels), line)
	}
}

// A folder line is capped at folderLabelCap labels however many distinct labels
// its branch holds: one folder line stands for a whole branch, and the per-file
// detail arrives on drill-down. What is dropped is not the tail: the last file
// is as likely to be heard as the first.
func TestProjectIndex_FolderLineCappedAtTheFolderCeiling(t *testing.T) {
	// One distinct label per file, so the folder sees one more distinct label
	// than the cap allows.
	entries := make([]*docindex.PageEntry, 0, folderLabelCap+1)
	for i := 0; i < folderLabelCap+1; i++ {
		entries = append(entries, textEntry(fmt.Sprintf("/proj/pkg/f%03d.go", i), fmt.Sprintf("topic%02d", i)))
	}
	tree := buildProjectTree("/proj", entries, nil, nil)
	out := renderProjectMap(tree, "")

	line := folderLine(t, out, "pkg/")
	if got := len(lineLabels(line)); got != folderLabelCap {
		t.Errorf("folder line carries %d labels, want the cap of %d:\n%s", got, folderLabelCap, line)
	}
	if !strings.Contains(line, "topic00") || !strings.Contains(line, fmt.Sprintf("topic%02d", folderLabelCap)) {
		t.Errorf("the first or the last file went unheard:\n%s", line)
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
	if !strings.Contains(out, "pkg/ (11 files)\n") {
		t.Errorf("unlabeled collapsed folder lost its file count, or gained a dangling dash:\n%s", out)
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
	if !strings.Contains(out, "f00.go — tool topic") {
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

	if !strings.Contains(out, "report.pdf — paper topic") || !strings.Contains(out, "notes.docx — paper topic") {
		t.Errorf("document leaves missing from the map:\n%s", out)
	}
	if !strings.Contains(out, "paper topic; extra topic") {
		t.Errorf("text file labels missing:\n%s", out)
	}

	// One level up they are a single folder line whose count spans all types.
	rolled := renderProjectMap(buildProjectTree("/proj", texts, pdf, docx), "")
	if !strings.Contains(rolled, "docs/") || !strings.Contains(rolled, "(4 files)") {
		t.Errorf("mixed document types did not roll up into one folder line:\n%s", rolled)
	}
}

// Labels are the bulk of this output, so a file is capped at the index's own
// per-page ceiling — but no lower. A stored label the model paid a step to
// produce must not be hidden by the render, so textLabelCap equals
// MaxLabelsPerPage.
func TestProjectIndex_TextFileLabelsCappedAtTheIndexCeiling(t *testing.T) {
	// One more label than the ceiling: the last must be dropped.
	many := make([]string, MaxLabelsPerPage+1)
	for i := range many {
		many[i] = fmt.Sprintf("topic%02d", i)
	}
	tree := buildProjectTree("/proj", []*docindex.PageEntry{textEntry("/proj/a.go", many...)}, nil, nil)
	out := renderProjectMap(tree, "")

	// The first MaxLabelsPerPage labels appear, in order; the extra one does not.
	for i := 0; i < MaxLabelsPerPage; i++ {
		if !strings.Contains(out, fmt.Sprintf("topic%02d", i)) {
			t.Errorf("label topic%02d within the cap was dropped:\n%s", i, out)
		}
	}
	if strings.Contains(out, fmt.Sprintf("topic%02d", MaxLabelsPerPage)) {
		t.Errorf("label past the cap of %d was kept:\n%s", MaxLabelsPerPage, out)
	}

	// A file at exactly the ceiling shows every label — the cap hides nothing
	// the indexer produced.
	exact := make([]string, MaxLabelsPerPage)
	for i := range exact {
		exact[i] = fmt.Sprintf("kept%02d", i)
	}
	full := renderProjectMap(buildProjectTree("/proj", []*docindex.PageEntry{textEntry("/proj/b.go", exact...)}, nil, nil), "")
	for i := 0; i < MaxLabelsPerPage; i++ {
		if !strings.Contains(full, fmt.Sprintf("kept%02d", i)) {
			t.Errorf("a file at the ceiling lost label kept%02d:\n%s", i, full)
		}
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

// With collapsing, the map must stay inside its own byte budget at every size —
// the 2000-file render now costs a few KB, not tens of KB — and folders
// summarize rather than leak loose file names.
func TestProjectIndex_StaysUnderOutputCap(t *testing.T) {
	for _, files := range []int{100, 300, 800, 2000} {
		t.Run(fmt.Sprintf("%dfiles", files), func(t *testing.T) {
			tree := buildProjectTree("/proj", bigIndex(files), nil, nil)
			out := renderProjectMap(tree, "")

			if len(out) > projectMapBudget {
				t.Errorf("map is %d bytes, over the %d budget — it would degrade",
					len(out), projectMapBudget)
			}
			// With one call = one expanded level, the root's large child
			// (internal/, many packages) is itself summarized — expanding
			// further is what a subdir call is for. What must survive is the
			// summary structure itself, at every size.
			if !strings.Contains(out, fmt.Sprintf("internal/ (%d files; subfolders: ", files)) {
				t.Errorf("map lost its folder summaries at %d files", files)
			}
		})
	}
}

// A level too wide for every file's full label set must degrade by showing
// fewer labels per file, not by dropping labels wholesale. This is the case the
// raised caps make reachable: a real directory of ~60 files at the store's
// ceiling would otherwise cross the budget and lose all its labels, which is
// exactly the depth the map exists to carry.
func TestProjectIndex_WideLevelShowsFewerLabelsRatherThanNone(t *testing.T) {
	// Many loose files, each at the store ceiling, with realistically long
	// labels — wide enough that full depth overflows the budget but a shallower
	// rung fits. 140 files renders ~125 KB at full depth, past the 100 KB budget.
	labels := make([]string, MaxLabelsPerPage)
	for i := range labels {
		labels[i] = fmt.Sprintf("subsystem behaviour topic %d", i)
	}
	const files = 140
	entries := make([]*docindex.PageEntry, 0, files)
	for i := 0; i < files; i++ {
		entries = append(entries, textEntry(fmt.Sprintf("/proj/pkg/f%03d.go", i), labels...))
	}

	// Sanity: at full depth this level must overflow, or the test proves nothing.
	var probe strings.Builder
	renderProjectLevel(buildProjectTree("/proj/pkg", entries, nil, nil), &probe, true, textLabelCap, folderLabelCap)
	if probe.Len() <= projectMapBudget {
		t.Skipf("fixture is only %d bytes at full depth — widen it to exercise degradation", probe.Len())
	}

	out := renderProjectMap(buildProjectTree("/proj/pkg", entries, nil, nil), "pkg")

	if len(out) > projectMapBudget {
		t.Errorf("map is %d bytes, over the %d budget", len(out), projectMapBudget)
	}
	// Labels survive — the whole point. If degradation dropped them, the line
	// would carry the file name alone.
	if !strings.Contains(out, "subsystem behaviour topic") {
		t.Errorf("a wide level lost every label instead of showing fewer:\n%s", out[:400])
	}
	// And they survive at reduced depth: rung 2 caps every entry at 10 labels,
	// so a level that overflows at full depth must show no more than that.
	atFolderRung := "subsystem behaviour topic 0; subsystem behaviour topic 1; subsystem behaviour topic 2; subsystem behaviour topic 3; subsystem behaviour topic 4; subsystem behaviour topic 5; subsystem behaviour topic 6; subsystem behaviour topic 7; subsystem behaviour topic 8; subsystem behaviour topic 9; subsystem behaviour topic 10"
	if strings.Contains(out, atFolderRung) {
		t.Error("expected a cap below 10 labels per file for a level this wide")
	}
}

// Past every rung the labels go, not the structure — and the agent is told how
// to get them back rather than being left with a silently shorter tree. This is
// the last resort: a level where even one label per file will not fit, so the
// fabricated index is one flat directory of 3000 loose files — past the 100 KB
// budget even once the render is down to a single label each.
func TestProjectIndex_LargeProjectDropsLabelsWithGuidance(t *testing.T) {
	flat := make([]*docindex.PageEntry, 0, 3001)
	for i := 0; i < 3000; i++ {
		flat = append(flat, textEntry(fmt.Sprintf("/proj/f%04d.go", i), "subsystem behaviour topic 0-0"))
	}
	// One folder beside them, so there is somewhere to narrow into — and the
	// guidance names it, from the project root.
	flat = append(flat, textEntry("/proj/pkg/inner/x.go", "inner topic"))

	smallHasLabels := renderProjectMap(buildProjectTree("/proj", bigIndex(50), nil, nil), "")
	large := renderProjectMap(buildProjectTree("/proj", flat, nil, nil), "")

	if !strings.Contains(smallHasLabels, "subsystem behaviour topic 0-0") {
		t.Error("a small project should still show labels")
	}
	if strings.Contains(large, "subsystem behaviour topic 0-0") {
		t.Error("an oversized project should drop labels, not keep them")
	}
	if !strings.Contains(large, `subdir="pkg/inner"`) {
		t.Errorf("oversized map does not tell the agent how to get labels back:\n%s", large[:400])
	}
	if !strings.Contains(large, "pkg/ (1 file; subfolders: inner)") {
		t.Errorf("the label-less outline dropped a folder's count or subfolders:\n%s", large[:400])
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
		t.Errorf("a 280-file project dropped its labels at %d bytes, well inside the %d budget",
			len(out), projectMapBudget)
	}
	if !strings.Contains(out, "subsystem behaviour topic 0-0") {
		t.Error("labels missing from a repo-sized map")
	}
}

// The map's byte budget sits above MaxToolOutputBytes, the generic cap on any
// tool result. That is only safe because Execute opts the result out of the
// loop's backstop (Result.Truncated): without the flag the backstop would
// head-truncate a large map mid-branch — the very thing the budget exists to
// prevent. Pinned at the Execute boundary, since a direct renderProjectMap call
// never touches the flag.
func TestProjectIndex_ResultOptsOutOfTheGenericOutputCap(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	store := docindex.NewStore(database)

	// Enough loose files at one level to render past the generic 50 KB cap on
	// their own, so the flag — not the size — is what keeps the tree intact.
	const dir = "/proj"
	for i := 0; i < 700; i++ {
		labels := make([]string, 8)
		for j := range labels {
			labels[j] = fmt.Sprintf("subsystem behaviour topic %d-%d", i, j)
		}
		if err := store.Upsert(&docindex.PageEntry{
			DocPath: fmt.Sprintf("%s/f%04d.go", dir, i), PageNum: 1, Labels: labels,
		}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	res, err := NewProjectIndexTool(store).Execute(context.Background(), nil, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(res.Output) <= MaxToolOutputBytes {
		t.Fatalf("fixture renders only %d bytes, under the %d generic cap — widen it to exercise the opt-out",
			len(res.Output), MaxToolOutputBytes)
	}
	if !res.Truncated {
		t.Errorf("map is %d bytes but the result is not marked Truncated — the loop's backstop would cut the tree mid-branch",
			len(res.Output))
	}
}

// A label repeated within one file's stored array, or across the files of a
// folder, renders once — on the file's line and on the folder's.
func TestProjectIndex_DuplicateLabelsShownOnce(t *testing.T) {
	entries := []*docindex.PageEntry{
		textEntry("/proj/pkg/a.go", "shared topic", "shared topic", "other topic"),
		textEntry("/proj/pkg/b.go", "shared topic"),
	}
	// Rooted at pkg/ so the files are this level's loose files and their own
	// lines are visible.
	out := renderProjectMap(buildProjectTree("/proj/pkg", entries, nil, nil), "pkg")

	if strings.Contains(out, "a.go — shared topic; shared topic") {
		t.Errorf("duplicate label rendered twice on the file line:\n%s", out)
	}
	if !strings.Contains(out, "a.go — shared topic; other topic") {
		t.Errorf("dedup reordered or dropped unique labels:\n%s", out)
	}

	rolled := renderProjectMap(buildProjectTree("/proj", entries, nil, nil), "")
	if !strings.Contains(rolled, "pkg/ (2 files) — shared topic; other topic") {
		t.Errorf("folder summary repeated a shared label:\n%s", rolled)
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

// folderLine returns the rendered line for a folder at this level, failing the
// test when it is missing.
func folderLine(t *testing.T, out, folder string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, folder+" (") {
			return l
		}
	}
	t.Fatalf("no line for %s:\n%s", folder, out)
	return ""
}

// lineLabels splits the labels off a folder or file line.
func lineLabels(line string) []string {
	_, labels, ok := strings.Cut(line, " — ")
	if !ok {
		return nil
	}
	return strings.Split(labels, "; ")
}

// A folder line names its subfolders — each one a subdir the agent can jump
// straight to — and past the cap keeps the largest and counts the rest.
func TestProjectIndex_FolderLineNamesItsSubfolders(t *testing.T) {
	entries := []*docindex.PageEntry{
		textEntry("/proj/pkg/zeta/a.go", "z"),
		textEntry("/proj/pkg/alpha/b.go", "a"),
		textEntry("/proj/pkg/loose.go", "l"),
	}
	line := folderLine(t, renderProjectMap(buildProjectTree("/proj", entries, nil, nil), ""), "pkg/")
	if !strings.HasPrefix(line, "pkg/ (3 files; subfolders: alpha, zeta) — ") {
		t.Errorf("folder line does not name its subfolders, alphabetically:\n%s", line)
	}

	// Past the cap: the largest are named, the rest counted.
	var wide []*docindex.PageEntry
	for i := 0; i < subfolderNameCap+3; i++ {
		files := 1
		if i < subfolderNameCap {
			files = 2 // the ones that must be kept
		}
		for f := 0; f < files; f++ {
			wide = append(wide, textEntry(fmt.Sprintf("/proj/big/s%02d/f%d.go", i, f), "t"))
		}
	}
	line = folderLine(t, renderProjectMap(buildProjectTree("/proj", wide, nil, nil), ""), "big/")
	if !strings.Contains(line, ", +3 more)") {
		t.Errorf("subfolders past the cap are not counted:\n%s", line)
	}
	for i := subfolderNameCap; i < subfolderNameCap+3; i++ {
		if strings.Contains(line, fmt.Sprintf("s%02d", i)) {
			t.Errorf("a smaller subfolder s%02d was named over a larger one:\n%s", i, line)
		}
	}
}

// Labels are separated by semicolons, so a label with commas of its own reads
// as one label — and a label that carries a semicolon or a line break is
// cleaned rather than split or allowed to break the line.
func TestProjectIndex_LabelsSeparatedUnambiguously(t *testing.T) {
	tree := buildProjectTree("/proj", []*docindex.PageEntry{
		textEntry("/proj/tunnel.go", "Yamux Tunnel, Reverse Proxy, Subdomains", "Auth; Sessions", "Line\nBreak"),
	}, nil, nil)
	out := renderProjectMap(tree, "")

	if !strings.Contains(out, "tunnel.go — Yamux Tunnel, Reverse Proxy, Subdomains; Auth, Sessions; Line Break\n") {
		t.Errorf("labels not joined unambiguously:\n%s", out)
	}
}

// The header's next-step example is a real folder at this level — and from the
// project root, because a drill-down lists "agent/" but the call that opens it
// names "internal/agent".
func TestProjectIndex_NextStepExampleIsFromTheProjectRoot(t *testing.T) {
	entries := []*docindex.PageEntry{
		textEntry("/proj/internal/agent/loop/run.go", "x"),
		textEntry("/proj/internal/agent/loop/step.go", "x"),
		textEntry("/proj/internal/bus/bus.go", "y"),
	}
	root := renderProjectMap(buildProjectTree("/proj", entries, nil, nil), "")
	if !strings.Contains(root, `(e.g. subdir="internal/agent")`) {
		t.Errorf("root map's example is not a real path from the root:\n%s", root)
	}
	drill := renderProjectMap(buildProjectTree("/proj/internal", entries, nil, nil), "internal")
	if !strings.Contains(drill, `(e.g. subdir="internal/agent/loop")`) {
		t.Errorf("drill-down's example is not from the project root:\n%s", drill)
	}
	// A level with no folders has nowhere to send the agent, and says nothing.
	leaf := renderProjectMap(buildProjectTree("/proj/internal/bus", entries[2:], nil, nil), "internal/bus")
	if strings.Contains(leaf, "e.g. subdir=") {
		t.Errorf("a level without folders offered a subdir example:\n%s", leaf)
	}
}

// Test files speak after the code they cover: at half weight, a folder line
// leads with what the package does, not with what its tests check.
func TestProjectIndex_TestFilesSpeakAfterCode(t *testing.T) {
	entries := []*docindex.PageEntry{
		textEntry("/proj/pkg/a_test.go", "A Unit Tests"),
		textEntry("/proj/pkg/b_test.go", "B Unit Tests"),
		textEntry("/proj/pkg/a.go", "Alpha Behaviour"),
		textEntry("/proj/pkg/b.go", "Beta Behaviour"),
	}
	labels := lineLabels(folderLine(t, renderProjectMap(buildProjectTree("/proj", entries, nil, nil), ""), "pkg/"))
	if len(labels) != 4 {
		t.Fatalf("labels = %q, want all four", labels)
	}
	for _, l := range labels[:2] {
		if strings.Contains(l, "Tests") {
			t.Errorf("a test label led the folder line: %q", labels)
		}
	}
}

// spreadOrder is a permutation whose every prefix is spread across the range —
// the property that turns "take the first k" into an even sample.
func TestSpreadOrder(t *testing.T) {
	for m := 0; m <= 40; m++ {
		order := spreadOrder(m)
		if len(order) != m {
			t.Fatalf("spreadOrder(%d) has %d entries", m, len(order))
		}
		seen := make(map[int]bool, m)
		for _, i := range order {
			if i < 0 || i >= m || seen[i] {
				t.Fatalf("spreadOrder(%d) = %v is not a permutation", m, order)
			}
			seen[i] = true
		}
	}
	// The first two of sixteen are the ends of the range's halves, the first
	// four its quarters: 0, 8, 4, 12.
	if got := spreadOrder(16)[:4]; got[0] != 0 || got[1] != 8 || got[2] != 4 || got[3] != 12 {
		t.Errorf("spreadOrder(16) starts %v, want [0 8 4 12]", got)
	}
}
