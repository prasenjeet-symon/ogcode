package indexer

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/docindex"
)

func TestAssembleBatches_PDFSeparation(t *testing.T) {
	idx := New("/tmp", nil, nil)
	items := []docItem{
		{path: "/a.pdf", corpora: []PageCorpus{{PageNum: 1, Keywords: []string{"kw1"}}}, isPDF: true, keywordCount: 1},
		{path: "/b.go", corpora: []PageCorpus{{PageNum: 1, Keywords: []string{"kw1"}}}, isPDF: false, keywordCount: 1},
		{path: "/c.go", corpora: []PageCorpus{{PageNum: 1, Keywords: []string{"kw2"}}}, isPDF: false, keywordCount: 1},
	}

	batches := idx.assembleBatches(items)

	// PDF should be in its own batch
	if len(batches) < 1 {
		t.Fatalf("expected at least 1 batch, got %d", len(batches))
	}
	foundPDF := false
	foundText := false
	for _, b := range batches {
		if b.isPDF {
			foundPDF = true
			if len(b.items) != 1 {
				t.Errorf("PDF batch should have exactly 1 item, got %d", len(b.items))
			}
			if b.items[0].path != "/a.pdf" {
				t.Errorf("PDF batch should contain /a.pdf, got %s", b.items[0].path)
			}
		} else {
			foundText = true
		}
	}
	if !foundPDF {
		t.Error("expected a PDF batch")
	}
	if !foundText {
		t.Error("expected at least one text batch")
	}
}

func TestAssembleBatches_TextFilesPacked(t *testing.T) {
	idx := New("/tmp", nil, nil).WithMaxKeywordsBatch(100)

	// 5 small text files with 20 keywords each — should fit in one batch
	items := []docItem{
		{path: "/a.go", corpora: []PageCorpus{{PageNum: 1, Keywords: make([]string, 20)}}, isPDF: false, keywordCount: 20},
		{path: "/b.go", corpora: []PageCorpus{{PageNum: 1, Keywords: make([]string, 20)}}, isPDF: false, keywordCount: 20},
		{path: "/c.go", corpora: []PageCorpus{{PageNum: 1, Keywords: make([]string, 20)}}, isPDF: false, keywordCount: 20},
		{path: "/d.go", corpora: []PageCorpus{{PageNum: 1, Keywords: make([]string, 20)}}, isPDF: false, keywordCount: 20},
		{path: "/e.go", corpora: []PageCorpus{{PageNum: 1, Keywords: make([]string, 20)}}, isPDF: false, keywordCount: 20},
	}

	batches := idx.assembleBatches(items)

	// 100 keywords total, limit is 100, should be 1 batch
	textBatches := 0
	for _, b := range batches {
		if !b.isPDF {
			textBatches++
		}
	}
	if textBatches != 1 {
		t.Errorf("expected 1 text batch for 5 small files, got %d", textBatches)
	}
}

func TestAssembleBatches_SplitOnKeywordLimit(t *testing.T) {
	idx := New("/tmp", nil, nil).WithMaxKeywordsBatch(50)

	// 3 files with 30 keywords each — each file exceeds 50/2 so they
	// can't pair up, resulting in 3 batches of 1 file each.
	items := []docItem{
		{path: "/a.go", corpora: []PageCorpus{{PageNum: 1, Keywords: make([]string, 30)}}, isPDF: false, keywordCount: 30},
		{path: "/b.go", corpora: []PageCorpus{{PageNum: 1, Keywords: make([]string, 30)}}, isPDF: false, keywordCount: 30},
		{path: "/c.go", corpora: []PageCorpus{{PageNum: 1, Keywords: make([]string, 30)}}, isPDF: false, keywordCount: 30},
	}

	batches := idx.assembleBatches(items)

	textBatches := 0
	totalItems := 0
	for _, b := range batches {
		if !b.isPDF {
			textBatches++
			totalItems += len(b.items)
		}
	}
	if totalItems != 3 {
		t.Errorf("expected 3 total items across batches, got %d", totalItems)
	}
	// Each file has 30 keywords; adding a second would reach 60 > 50 limit.
	// So each file lands in its own batch: 3 batches.
	if textBatches != 3 {
		t.Errorf("expected 3 text batches (each file solo), got %d", textBatches)
	}
}

func TestAssembleBatches_SplitOnKeywordLimit_Paired(t *testing.T) {
	idx := New("/tmp", nil, nil).WithMaxKeywordsBatch(60)

	// 4 files with 25 keywords each — should split into 2 batches:
	// Batch 1: a.go + b.go = 50 keywords (≤ 60)
	// Batch 2: c.go + d.go = 50 keywords (≤ 60)
	items := []docItem{
		{path: "/a.go", corpora: []PageCorpus{{PageNum: 1, Keywords: make([]string, 25)}}, isPDF: false, keywordCount: 25},
		{path: "/b.go", corpora: []PageCorpus{{PageNum: 1, Keywords: make([]string, 25)}}, isPDF: false, keywordCount: 25},
		{path: "/c.go", corpora: []PageCorpus{{PageNum: 1, Keywords: make([]string, 25)}}, isPDF: false, keywordCount: 25},
		{path: "/d.go", corpora: []PageCorpus{{PageNum: 1, Keywords: make([]string, 25)}}, isPDF: false, keywordCount: 25},
	}

	batches := idx.assembleBatches(items)

	textBatches := 0
	totalItems := 0
	for _, b := range batches {
		if !b.isPDF {
			textBatches++
			totalItems += len(b.items)
		}
	}
	if totalItems != 4 {
		t.Errorf("expected 4 total items across batches, got %d", totalItems)
	}
	// 25+25=50 ≤ 60, so first two fit in batch 1.
	// 25+25=50 ≤ 60, so next two fit in batch 2.
	if textBatches != 2 {
		t.Errorf("expected 2 text batches, got %d", textBatches)
	}
}

func TestAssembleBatches_EmptyItems(t *testing.T) {
	idx := New("/tmp", nil, nil)
	batches := idx.assembleBatches(nil)
	if len(batches) != 0 {
		t.Errorf("expected 0 batches for nil items, got %d", len(batches))
	}
}

func TestAssembleBatches_DocxSeparation(t *testing.T) {
	idx := New("/tmp", nil, nil)
	items := []docItem{
		{path: "/report.docx", corpora: []PageCorpus{{PageNum: 1, Keywords: []string{"database", "optimization"}}, {PageNum: 2, Keywords: []string{"concurrency", "patterns"}}}, isDocx: true, keywordCount: 4},
		{path: "/main.go", corpora: []PageCorpus{{PageNum: 1, Keywords: []string{"kw1"}}}, isPDF: false, keywordCount: 1},
	}

	batches := idx.assembleBatches(items)

	// DOCX should be in its own batch (like PDFs)
	foundDocx := false
	foundText := false
	for _, b := range batches {
		if b.isDocx {
			foundDocx = true
			if len(b.items) != 1 {
				t.Errorf("DOCX batch should have exactly 1 item, got %d", len(b.items))
			}
			if b.items[0].path != "/report.docx" {
				t.Errorf("DOCX batch should contain /report.docx, got %s", b.items[0].path)
			}
			if !b.items[0].isDocx {
				t.Error("DOCX docItem should have isDocx=true")
			}
		} else {
			foundText = true
		}
	}
	if !foundDocx {
		t.Error("expected a DOCX batch")
	}
	if !foundText {
		t.Error("expected at least one text batch")
	}
}

func TestAssembleBatches_DocxAndPdfSeparate(t *testing.T) {
	idx := New("/tmp", nil, nil)
	items := []docItem{
		{path: "/doc.pdf", corpora: []PageCorpus{{PageNum: 1, Keywords: []string{"kw1"}}}, isPDF: true, keywordCount: 1},
		{path: "/doc.docx", corpora: []PageCorpus{{PageNum: 1, Keywords: []string{"kw2"}}}, isDocx: true, keywordCount: 1},
		{path: "/main.go", corpora: []PageCorpus{{PageNum: 1, Keywords: []string{"kw3"}}}, isPDF: false, keywordCount: 1},
	}

	batches := idx.assembleBatches(items)

	// Both PDF and DOCX should be in separate batches
	foundPDF := false
	foundDocx := false
	for _, b := range batches {
		if b.isPDF && !b.isDocx {
			foundPDF = true
		}
		if b.isDocx && !b.isPDF {
			foundDocx = true
		}
	}
	if !foundPDF {
		t.Error("expected a PDF batch")
	}
	if !foundDocx {
		t.Error("expected a DOCX batch")
	}
}

func TestProgressTracker(t *testing.T) {
	idx := New("/tmp", nil, nil)
	p := idx.Progress()

	p.Total.Store(100)
	p.Completed.Add(10)
	p.Failed.Add(2)

	if p.Total.Load() != 100 {
		t.Errorf("expected total 100, got %d", p.Total.Load())
	}
	if p.Completed.Load() != 10 {
		t.Errorf("expected completed 10, got %d", p.Completed.Load())
	}
	if p.Failed.Load() != 2 {
		t.Errorf("expected failed 2, got %d", p.Failed.Load())
	}
}

func newTestDocStore(t *testing.T) *docindex.Store {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	return docindex.NewStore(database)
}

func TestPurgeDeletedDocs_RemovesStaleEntries(t *testing.T) {
	dir := "/workspace"
	store := newTestDocStore(t)
	idx := New(dir, store, nil)

	// Simulate three previously-indexed files.
	indexedPaths := []string{
		filepath.Join(dir, "a.go"),
		filepath.Join(dir, "b.go"),
		filepath.Join(dir, "c.go"),
	}
	for _, p := range indexedPaths {
		if err := store.Upsert(&docindex.PageEntry{DocPath: p, PageNum: 1, Keywords: []string{"kw"}, Labels: []string{"L"}}); err != nil {
			t.Fatalf("upsert %s: %v", p, err)
		}
	}

	// Only a.go and b.go still exist on disk; c.go was deleted.
	currentFiles := []string{
		filepath.Join(dir, "a.go"),
		filepath.Join(dir, "b.go"),
	}

	if err := idx.purgeDeletedDocs(context.Background(), currentFiles); err != nil {
		t.Fatalf("purgeDeletedDocs: %v", err)
	}

	// c.go should be gone.
	if indexed, _ := store.IsDocIndexed(filepath.Join(dir, "c.go")); indexed {
		t.Error("expected deleted file c.go to be purged from index")
	}
	// a.go and b.go should still be indexed.
	if indexed, _ := store.IsDocIndexed(filepath.Join(dir, "a.go")); !indexed {
		t.Error("expected a.go to still be indexed")
	}
	if indexed, _ := store.IsDocIndexed(filepath.Join(dir, "b.go")); !indexed {
		t.Error("expected b.go to still be indexed")
	}
}

func TestPurgeDeletedDocs_NoStaleEntries(t *testing.T) {
	dir := "/workspace"
	store := newTestDocStore(t)
	idx := New(dir, store, nil)

	// Two indexed files, both still present.
	for _, p := range []string{filepath.Join(dir, "a.go"), filepath.Join(dir, "b.go")} {
		if err := store.Upsert(&docindex.PageEntry{DocPath: p, PageNum: 1, Keywords: []string{"kw"}, Labels: []string{}}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	currentFiles := []string{
		filepath.Join(dir, "a.go"),
		filepath.Join(dir, "b.go"),
	}
	if err := idx.purgeDeletedDocs(context.Background(), currentFiles); err != nil {
		t.Fatalf("purgeDeletedDocs: %v", err)
	}

	// Both should still be indexed.
	for _, p := range currentFiles {
		if indexed, _ := store.IsDocIndexed(p); !indexed {
			t.Errorf("expected %s to still be indexed", p)
		}
	}
}

func TestPurgeDeletedDocs_EmptyWalkPurgesAll(t *testing.T) {
	dir := "/workspace"
	store := newTestDocStore(t)
	idx := New(dir, store, nil)

	// Three indexed files, none exist on disk anymore.
	for _, p := range []string{
		filepath.Join(dir, "a.go"),
		filepath.Join(dir, "b.go"),
		filepath.Join(dir, "c.go"),
	} {
		if err := store.Upsert(&docindex.PageEntry{DocPath: p, PageNum: 1, Keywords: []string{"kw"}, Labels: []string{}}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	if err := idx.purgeDeletedDocs(context.Background(), nil); err != nil {
		t.Fatalf("purgeDeletedDocs: %v", err)
	}

	paths, err := store.ListDocPaths(dir)
	if err != nil {
		t.Fatalf("ListDocPaths: %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("expected all entries purged, got %d remaining: %v", len(paths), paths)
	}
}

func TestPurgeDeletedDocs_NilStore(t *testing.T) {
	idx := New("/workspace", nil, nil)
	// Should be a no-op, not a panic.
	if err := idx.purgeDeletedDocs(context.Background(), nil); err != nil {
		t.Errorf("expected nil error with nil store, got %v", err)
	}
}

// The contract that replaced the hardcoded skip list: a directory is excluded
// because the project says so, never because of what it is called.
//
// These names — node_modules, vendor, build, grammars — were all skipped by
// name once. A project that tracks one of them was silently missing it from its
// index with nothing to explain why. Now the same names are indexed unless
// .gitignore excludes them, and the second half of this test shows the same
// tree going quiet the moment it does. (Ogcode's own state directory is the one
// by-name exception, and it is not a name a project has any business tracking —
// see TestCollectFiles_SkipsOgcodeStateDirectory.)
func TestCollectFiles_NoDirectoryIsExcludedByName(t *testing.T) {
	files := map[string]string{
		"node_modules/pkg/index.js": "module.exports = {}",
		"vendor/lib/helper.go":      "package lib",
		"build/generated.go":        "package generated",
		"grammars/swift/parser.c":   "int main(void){}",
		"src/main.go":               "package main",
	}
	root := indexTree(t, files)

	got := collected(t, New(root, nil, nil), root)
	for want := range files {
		if !slicesContains(got, want) {
			t.Errorf("%s was excluded with no .gitignore saying so; collected %v", want, got)
		}
	}

	// Now the project says so, and every one of them goes.
	if err := os.WriteFile(filepath.Join(root, ".gitignore"),
		[]byte("node_modules/\nvendor/\nbuild/\ngrammars/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got = collected(t, New(root, nil, nil), root)
	want := []string{"src/main.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("collected %v, want %v", got, want)
	}
}

// The repository's own metadata is excluded, and not as a policy of the
// indexer's: git does not treat .git as part of the working tree, so a matcher
// that let a walk descend into it would be modelling git wrongly. It holds no
// indexable file on any real repository — this one has 549 files in .git and
// zero of them index — so the exclusion costs nothing but the walk.
//
// Ogcode's state directory (.ogcode/) is the other unconditional skip, for the
// same shape of reason: it is not part of the working set. See
// TestCollectFiles_SkipsOgcodeStateDirectory.
func TestCollectFiles_ExcludesTheGitDirectory(t *testing.T) {
	root := indexTree(t, map[string]string{
		".git/config":            "[core]",
		".git/hooks/pre-push.sh": "#!/bin/sh",
		"src/main.go":            "package main",
	})

	got := collected(t, New(root, nil, nil), root)
	want := []string{"src/main.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("collected %v, want %v", got, want)
	}
}

// indexTree materialises files under a temp dir and returns the root.
func indexTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// collected returns the indexable files under root as slash-separated paths
// relative to it, so a test can state its expectations the way a person would.
//
// Every fixture below uses an extension the indexer actually indexes. Files
// like .gitignore, .log or .env are absent from textExtensions, so a fixture
// built from those would pass whether or not the ignore rules worked at all.
func collected(t *testing.T, idx *Indexer, root string) []string {
	t.Helper()
	files, err := idx.collectFiles()
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		rel, err := filepath.Rel(root, f)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, filepath.ToSlash(rel))
	}
	sort.Strings(out)
	return out
}

// The feature: whatever the repository already declares as noise stays out of
// the index. Nobody should have to maintain the same exclusion list twice.
func TestCollectFiles_RespectsGitignore(t *testing.T) {
	root := indexTree(t, map[string]string{
		".gitignore":         "*.json\nbuild/\nsecret.txt\n!keep.json\n",
		"src/main.go":        "package main",
		"src/data.json":      "{}",
		"keep.json":          "{}",
		"build/generated.go": "package generated",
		"build/deep/more.go": "package more",
		"secret.txt":         "TOKEN=x",
		"docs/readme.md":     "# docs",
	})

	got := collected(t, New(root, nil, nil), root)
	want := []string{"docs/readme.md", "keep.json", "src/main.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("collected %v, want %v", got, want)
	}
}

// A .gitignore inside a subdirectory governs that subtree, and can re-include
// what the root excluded.
func TestCollectFiles_RespectsNestedGitignore(t *testing.T) {
	root := indexTree(t, map[string]string{
		".gitignore":             "*.json\n",
		"service/.gitignore":     "!important.json\nlocal/\n",
		"service/important.json": "{}",
		"service/scratch.json":   "{}",
		"service/local/x.go":     "package x",
		"service/main.go":        "package main",
		"other/scratch.json":     "{}",
	})

	got := collected(t, New(root, nil, nil), root)
	for _, want := range []string{"service/important.json", "service/main.go"} {
		if !slicesContains(got, want) {
			t.Errorf("%s should have been indexed; got %v", want, got)
		}
	}
	for _, unwanted := range []string{"service/scratch.json", "other/scratch.json", "service/local/x.go"} {
		if slicesContains(got, unwanted) {
			t.Errorf("%s should have been ignored; got %v", unwanted, got)
		}
	}
}

// The user's configured excludes and .gitignore are independent filters, and a
// file need only be caught by one of them.
func TestCollectFiles_GitignoreAndExcludesBothApply(t *testing.T) {
	root := indexTree(t, map[string]string{
		".gitignore":  "*.json\n",
		"app.json":    "caught by gitignore",
		"notes.txt":   "caught by the configured excludes",
		"src/main.go": "package main",
	})

	got := collected(t, New(root, nil, nil).WithExcludes([]string{"notes.txt"}), root)
	want := []string{"src/main.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("collected %v, want %v", got, want)
	}
}

// A workspace with no .gitignore must index exactly what it did before.
func TestCollectFiles_NoGitignoreIndexesEverything(t *testing.T) {
	root := indexTree(t, map[string]string{
		"src/main.go": "package main",
		"README.md":   "# hi",
	})

	got := collected(t, New(root, nil, nil), root)
	want := []string{"README.md", "src/main.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("collected %v, want %v", got, want)
	}
}

// ogcode's own state directory is skipped whether or not the project has a
// .gitignore, because it is not part of the project. It holds the project
// database, notes and plan archives, and — the reason this cannot be left to a
// default .gitignore entry — the git worktrees tasks are checked out into,
// which are a full second copy of every file in the tree. Indexing that would
// import the whole repository twice, under paths that vanish when the task
// ends.
func TestCollectFiles_SkipsOgcodeStateDirectory(t *testing.T) {
	root := indexTree(t, map[string]string{
		// Deliberately no .gitignore: the exclusion must not depend on one.
		"src/main.go":                              "package main",
		".ogcode/memory/turn.md":                   "# memory",
		".ogcode/notes/note.md":                    "# note",
		".ogcode/archives/plan.md":                 "# plan",
		".ogcode/worktrees/task/t1/src/main.go":    "package main",
		".ogcode/worktrees/task/t1/nested/deep.go": "package deep",
	})

	got := collected(t, New(root, nil, nil), root)
	want := []string{"src/main.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("collected %v, want just %v — ogcode's state directory is not the project", got, want)
	}
}

// Like .git, the state directory cannot be brought back by a rule. The prune
// happens before .gitignore is consulted, so a ! re-include aimed at it is
// simply never reached — which is the correct reading, since the directory was
// never inside the project's working set to begin with.
func TestCollectFiles_StateDirectoryCannotBeReincluded(t *testing.T) {
	root := indexTree(t, map[string]string{
		// An explicit attempt to re-include the memory subtree after excluding it.
		".gitignore":             ".ogcode/\n!.ogcode/memory/\n",
		".ogcode/memory/turn.md": "# memory",
		"src/main.go":            "package main",
	})

	got := collected(t, New(root, nil, nil), root)
	want := []string{"src/main.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("collected %v, want just %v — no rule can re-include ogcode's state directory", got, want)
	}
}

// An ignored directory is pruned, not walked. On a real repository the ignored
// directories are the enormous ones — node_modules, build output, virtualenvs —
// so descending into them to reject each file individually would cost most of
// the walk.
func TestCollectFiles_PrunesIgnoredDirectories(t *testing.T) {
	files := map[string]string{".gitignore": "heavy/\n", "src/main.go": "package main"}
	for i := 0; i < 200; i++ {
		files["heavy/pkg"+strconv.Itoa(i)+"/index.go"] = "package x"
	}

	root := indexTree(t, files)
	got := collected(t, New(root, nil, nil), root)
	want := []string{"src/main.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("collected %d files (%v), want just %v", len(got), got, want)
	}
}

func slicesContains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// The round trip the feature is judged on: the index has to track .gitignore in
// both directions, not just filter new work.
//
// Adding a path to .gitignore has to remove what was already indexed under it,
// or the index keeps answering questions about a directory the project has
// since declared noise — and nothing in a later run would ever revisit it,
// because already-indexed files are skipped.
func TestReindex_PurgesFilesNewlyAddedToGitignore(t *testing.T) {
	root := indexTree(t, map[string]string{
		"src/main.go":     "package main",
		"generated/db.go": "package generated",
	})
	store := newTestDocStore(t)

	// First run: no .gitignore, so both files are collected and indexed.
	first := New(root, store, nil)
	files := collected(t, first, root)
	if len(files) != 2 {
		t.Fatalf("first pass collected %v, want both files", files)
	}
	for _, rel := range files {
		if err := store.Upsert(&docindex.PageEntry{
			DocPath: filepath.Join(root, filepath.FromSlash(rel)), PageNum: 1,
			Keywords: []string{"kw"}, Labels: []string{"L"},
		}); err != nil {
			t.Fatal(err)
		}
	}

	// The project now declares generated/ as noise.
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("generated/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Second run: a fresh Indexer, as the server builds one per index request.
	second := New(root, store, nil)
	allFiles, err := second.collectFiles()
	if err != nil {
		t.Fatal(err)
	}
	if err := second.purgeDeletedDocs(context.Background(), allFiles); err != nil {
		t.Fatalf("purge: %v", err)
	}

	if indexed, _ := store.IsDocIndexed(filepath.Join(root, "generated", "db.go")); indexed {
		t.Error("a file added to .gitignore is still in the index after re-indexing")
	}
	if indexed, _ := store.IsDocIndexed(filepath.Join(root, "src", "main.go")); !indexed {
		t.Error("a file that is still allowed was purged")
	}
}

// The other direction. Taking a path out of .gitignore has to bring it back:
// the file was never indexed while ignored, so the re-run has to collect it.
func TestReindex_PicksUpFilesRemovedFromGitignore(t *testing.T) {
	root := indexTree(t, map[string]string{
		".gitignore":      "generated/\n",
		"src/main.go":     "package main",
		"generated/db.go": "package generated",
	})

	if got := collected(t, New(root, nil, nil), root); strings.Join(got, ",") != "src/main.go" {
		t.Fatalf("while ignored, collected %v, want just src/main.go", got)
	}

	// The project changes its mind.
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := collected(t, New(root, nil, nil), root)
	if !slicesContains(got, "generated/db.go") {
		t.Errorf("a file removed from .gitignore was not picked back up; collected %v", got)
	}
}

// Parsed .gitignore files are cached for the life of a Matcher, which is the
// life of its Indexer. That is only safe because a run builds its own — editing
// .gitignore and re-indexing through a reused Indexer would consult the rules
// as they were. Pinned so the ownership does not quietly change.
func TestIndexer_ReadsGitignoreFreshPerRun(t *testing.T) {
	root := indexTree(t, map[string]string{
		".gitignore":  "*.json\n",
		"config.json": "{}",
		"src/main.go": "package main",
	})

	stale := New(root, nil, nil)
	if got := collected(t, stale, root); slicesContains(got, "config.json") {
		t.Fatalf("config.json should start out ignored; got %v", got)
	}

	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The same Indexer keeps its cached rules — that is the documented contract,
	// and the reason a run must not reuse one.
	if got := collected(t, stale, root); slicesContains(got, "config.json") {
		t.Error("the cached matcher re-read .gitignore mid-run, which makes a run's filtering non-deterministic")
	}
	// A fresh one sees the new rules, which is what every real run gets.
	if got := collected(t, New(root, nil, nil), root); !slicesContains(got, "config.json") {
		t.Errorf("a fresh Indexer did not see the updated .gitignore; collected %v", got)
	}
}

func TestPreviewCountsWork(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string) string {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return full
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("skipped/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := write("a.go")
	write("b.go")
	write("c.go")
	write("skipped/d.go") // excluded by .gitignore, so absent from every count

	store := newTestDocStore(t)
	// One file already in the index, and one entry whose file is gone: the two
	// cases a run treats differently and a preview has to tell apart. a.go is
	// recorded at its own mtime, which is what an indexed file carries now, so it
	// reads as unchanged; deleted.go has no mtime and no file, so it is stale.
	aInfo, err := os.Stat(a)
	if err != nil {
		t.Fatalf("stat a.go: %v", err)
	}
	if err := store.Upsert(&docindex.PageEntry{
		DocPath: a, PageNum: 1, Keywords: []string{"kw"}, Labels: []string{"L"},
		ModTime: aInfo.ModTime().UnixMilli(),
	}); err != nil {
		t.Fatalf("upsert %s: %v", a, err)
	}
	if err := store.Upsert(&docindex.PageEntry{
		DocPath: filepath.Join(dir, "deleted.go"), PageNum: 1, Keywords: []string{"kw"}, Labels: []string{"L"},
	}); err != nil {
		t.Fatalf("upsert deleted.go: %v", err)
	}

	plan, err := New(dir, store, nil).Preview()
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}

	// .gitignore itself has no extension the indexer treats as text, so the
	// three .go files are the whole indexable set.
	if plan.Total != 3 {
		t.Errorf("Total = %d, want 3 (skipped/ is gitignored)", plan.Total)
	}
	if plan.Indexed != 1 {
		t.Errorf("Indexed = %d, want 1", plan.Indexed)
	}
	if plan.Pending != 2 {
		t.Errorf("Pending = %d, want 2", plan.Pending)
	}
	if plan.Stale != 1 {
		t.Errorf("Stale = %d, want 1 — the entry for a file no longer on disk", plan.Stale)
	}
	// The pending breakdown must describe the pending set, not the whole tree:
	// the dialog prints it next to the pending count.
	if plan.PendingText != 2 {
		t.Errorf("PendingText = %d, want 2", plan.PendingText)
	}
	if plan.Text != 3 {
		t.Errorf("Text = %d, want 3", plan.Text)
	}
}

func TestFileList_MarksIndexedAndSkipsGitignored(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string) string {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return full
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("skipped/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := write("a.go")
	write("b.go")
	write("skipped/d.go") // excluded by .gitignore — must not appear in the list

	store := newTestDocStore(t)
	// One file already in the index, plus a stale entry for a file no longer on disk.
	if err := store.Upsert(&docindex.PageEntry{
		DocPath: a, PageNum: 1, Keywords: []string{"kw"}, Labels: []string{"L"},
	}); err != nil {
		t.Fatalf("upsert %s: %v", a, err)
	}
	if err := store.Upsert(&docindex.PageEntry{
		DocPath: filepath.Join(dir, "deleted.go"), PageNum: 1, Keywords: []string{"kw"}, Labels: []string{"L"},
	}); err != nil {
		t.Fatalf("upsert stale: %v", err)
	}

	files, err := New(dir, store, nil).FileList()
	if err != nil {
		t.Fatalf("FileList: %v", err)
	}

	// The gitignored file must be absent; the stale entry must not appear (it is
	// not on disk). Both on-disk files show up, with exactly one marked indexed.
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2: %+v", len(files), files)
	}
	indexed := 0
	seenA := false
	for _, f := range files {
		if f.Indexed {
			indexed++
		}
		if f.Path == a {
			seenA = true
			if !f.Indexed {
				t.Errorf("a.go should be marked indexed")
			}
		}
	}
	if indexed != 1 {
		t.Errorf("indexed count = %d, want 1", indexed)
	}
	if !seenA {
		t.Errorf("a.go missing from the list")
	}
}

// A file the agent (or the user) rewrote after it was indexed must be picked
// back up by the next run. Before the index recorded a modification time, the
// path alone decided, so an edited file was skipped forever and only a full
// rebuild ever refreshed it.
func TestFilesToIndex_PicksUpModifiedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := newTestDocStore(t)
	idx := New(dir, store, nil)

	// Indexed as of a moment ago: the file has not changed since, so it is
	// skipped.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(&docindex.PageEntry{
		DocPath: path, PageNum: 1, Keywords: []string{"kw"}, Labels: []string{"L"},
		ModTime: info.ModTime().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}

	toIndex, changed, err := idx.filesToIndex(context.Background(), []string{path})
	if err != nil {
		t.Fatalf("filesToIndex: %v", err)
	}
	if len(toIndex) != 0 || changed != 0 {
		t.Errorf("unchanged file was selected for indexing: %v (changed=%d)", toIndex, changed)
	}

	// Rewrite the file with a mtime ahead of the recorded one, the way any
	// editor or the agent's own write would.
	future := info.ModTime().Add(time.Hour)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	toIndex, changed, err = idx.filesToIndex(context.Background(), []string{path})
	if err != nil {
		t.Fatalf("filesToIndex after edit: %v", err)
	}
	if len(toIndex) != 1 || toIndex[0] != path {
		t.Fatalf("modified file was not selected for re-indexing: %v", toIndex)
	}
	if changed != 1 {
		t.Errorf("changed count = %d, want 1", changed)
	}
	// Its stale rows are dropped so the re-index starts clean rather than
	// stacking a second set of pages on the first.
	if indexed, _ := store.IsDocIndexed(path); indexed {
		t.Error("stale rows survived; the re-index would land on top of them")
	}
}

// An untouched indexed file is still skipped, and a brand-new file is still
// taken: the modification-time check must not turn every run into a full one.
func TestFilesToIndex_SkipsUnchangedAndTakesNew(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.go")
	newFile := filepath.Join(dir, "new.go")
	for _, p := range []string{old, newFile} {
		if err := os.WriteFile(p, []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	store := newTestDocStore(t)
	info, err := os.Stat(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(&docindex.PageEntry{
		DocPath: old, PageNum: 1, Keywords: []string{"kw"}, Labels: []string{"L"},
		ModTime: info.ModTime().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}

	toIndex, changed, err := New(dir, store, nil).filesToIndex(context.Background(), []string{old, newFile})
	if err != nil {
		t.Fatalf("filesToIndex: %v", err)
	}
	if changed != 0 {
		t.Errorf("changed = %d, want 0 — nothing was modified", changed)
	}
	if len(toIndex) != 1 || toIndex[0] != newFile {
		t.Errorf("toIndex = %v, want just the new file", toIndex)
	}
}

// A row written before mod_time existed carries 0, which must read as stale so
// every pre-existing project refreshes once instead of never.
func TestFilesToIndex_ZeroModTimeReadsAsStale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := newTestDocStore(t)
	if err := store.Upsert(&docindex.PageEntry{
		DocPath: path, PageNum: 1, Keywords: []string{"kw"}, Labels: []string{"L"},
	}); err != nil {
		t.Fatal(err)
	}

	toIndex, changed, err := New(dir, store, nil).filesToIndex(context.Background(), []string{path})
	if err != nil {
		t.Fatalf("filesToIndex: %v", err)
	}
	if len(toIndex) != 1 || changed != 1 {
		t.Errorf("a row with no recorded mod time must refresh: toIndex=%v changed=%d", toIndex, changed)
	}
}

// The gate is off by default and turned off by the documented values, matching
// memfile.TurnMemoryEnabled so the two per-turn jobs are turned off the same way.
func TestAutoIndexEnabled(t *testing.T) {
	t.Setenv("OGCODE_AUTO_INDEX", "")
	if !AutoIndexEnabled() {
		t.Error("auto-index must be on when the variable is unset")
	}
	for _, off := range []string{"0", "false", "no", "off", "OFF", " False "} {
		t.Setenv("OGCODE_AUTO_INDEX", off)
		if AutoIndexEnabled() {
			t.Errorf("OGCODE_AUTO_INDEX=%q should disable auto-index", off)
		}
	}
	for _, on := range []string{"1", "true", "yes", "on", "anything"} {
		t.Setenv("OGCODE_AUTO_INDEX", on)
		if !AutoIndexEnabled() {
			t.Errorf("OGCODE_AUTO_INDEX=%q should leave auto-index on", on)
		}
	}
}
