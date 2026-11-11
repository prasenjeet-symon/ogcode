package indexer

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/agent"
	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/docindex"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
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

// TestWordExtensionsAreIndexable pins the pipeline-level half of Defect G: the
// walk and the preview treat every Word extension as a document — .docx, .docm
// and .dotx are the same OOXML ZIP — while .doc, which no reader can open,
// stays invisible to both.
func TestWordExtensionsAreIndexable(t *testing.T) {
	root := indexTree(t, map[string]string{
		"spec.docx":     "zip",
		"macro.docm":    "zip",
		"template.dotx": "zip",
		"legacy.doc":    "ole",
		"notes.md":      "# notes\n",
	})

	got := collected(t, New(root, nil, nil), root)
	for _, want := range []string{"macro.docm", "spec.docx", "template.dotx"} {
		if !slicesContains(got, want) {
			t.Errorf("collectFiles missed %q: %v", want, got)
		}
	}
	if slicesContains(got, "legacy.doc") {
		t.Errorf("collectFiles picked up a .doc, which no reader handles: %v", got)
	}

	plan, err := New(root, nil, nil).Preview()
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if plan.Docx != 3 {
		t.Errorf("plan.Docx = %d, want 3 Word documents", plan.Docx)
	}
	if plan.PendingDocx != 3 {
		t.Errorf("plan.PendingDocx = %d, want 3", plan.PendingDocx)
	}
	if plan.Total != 4 {
		t.Errorf("plan.Total = %d, want 4 (three Word documents and notes.md)", plan.Total)
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

// A file whose mtime moved backwards — git checkout, cp -p, rsync -a, a
// restored backup — must count as changed: the doc_index tool refuses a row set
// whose recorded time is not the file's own. With a strict ">" the two
// disagreed, so the index stayed stale forever while the tool told the user to
// re-run a command that skipped the file every time.
func TestFileChangedSinceTreatsBackwardsModTimeAsChanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if fileChangedSince(path, info.ModTime().UnixMilli()) {
		t.Error("fileChangedSince = true for an unchanged file, want false")
	}

	past := info.ModTime().Add(-time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	if !fileChangedSince(path, info.ModTime().UnixMilli()) {
		t.Error("fileChangedSince = false for a file whose mtime moved backwards, want true")
	}

	// A stat that fails is still "unchanged": there is no mtime to compare.
	if fileChangedSince(filepath.Join(t.TempDir(), "missing.go"), 0) {
		t.Error("fileChangedSince = true for a missing file, want false")
	}
}

// The selection path uses the same predicate, so a backwards mtime is re-indexed
// and its rows are dropped before the new run, like any other change.
func TestFilesToIndex_PicksUpBackwardsModTime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := newTestDocStore(t)
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

	past := info.ModTime().Add(-time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}

	toIndex, changed, err := New(dir, store, nil).filesToIndex(context.Background(), []string{path})
	if err != nil {
		t.Fatalf("filesToIndex: %v", err)
	}
	if len(toIndex) != 1 || toIndex[0] != path {
		t.Fatalf("file with a backwards mtime was not selected for re-indexing: %v", toIndex)
	}
	if changed != 1 {
		t.Errorf("changed count = %d, want 1", changed)
	}
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

// The file limit defaults to 500, and an unparseable or non-positive override is
// ignored rather than silently uncapping the guard that protects big projects.
func TestAutoIndexFileLimit(t *testing.T) {
	t.Setenv("OGCODE_AUTO_INDEX_MAX_FILES", "")
	if got := AutoIndexFileLimit(); got != defaultAutoIndexFileLimit {
		t.Errorf("unset limit = %d, want %d", got, defaultAutoIndexFileLimit)
	}
	if defaultAutoIndexFileLimit != 500 {
		t.Errorf("documented default = %d, want 500", defaultAutoIndexFileLimit)
	}

	t.Setenv("OGCODE_AUTO_INDEX_MAX_FILES", "250")
	if got := AutoIndexFileLimit(); got != 250 {
		t.Errorf("limit = %d, want 250", got)
	}

	for _, raw := range []string{"0", "-3", "abc", "  "} {
		t.Setenv("OGCODE_AUTO_INDEX_MAX_FILES", raw)
		if got := AutoIndexFileLimit(); got != defaultAutoIndexFileLimit {
			t.Errorf("limit for %q = %d, want %d", raw, got, defaultAutoIndexFileLimit)
		}
	}
}

// IndexableCount stops counting at the limit, so the caller can ask "more than
// N?" without walking a huge tree to the end.
func TestIndexableCount(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 12; i++ {
		files["f"+strconv.Itoa(i)+".go"] = "package main\n"
	}
	root := indexTree(t, files)
	idx := New(root, nil, nil)

	// limit 0 counts every indexable file, the way collectFiles does.
	if n, err := idx.IndexableCount(0); err != nil || n != 12 {
		t.Fatalf("IndexableCount(0) = %d, %v; want 12, nil", n, err)
	}
	// A limit below the count stops there.
	if n, err := idx.IndexableCount(5); err != nil || n != 5 {
		t.Fatalf("IndexableCount(5) = %d, %v; want 5, nil", n, err)
	}
	// A limit above the count returns the true count.
	if n, err := idx.IndexableCount(50); err != nil || n != 12 {
		t.Fatalf("IndexableCount(50) = %d, %v; want 12, nil", n, err)
	}
}

// pageCorpora builds the expected-page list a batch carries, one entry per page.
func pageCorpora(nums ...int) []PageCorpus {
	out := make([]PageCorpus, len(nums))
	for i, n := range nums {
		out[i] = PageCorpus{PageNum: n}
	}
	return out
}

// storedPages builds what the store returns for a document: one row per page.
func storedPages(docPath string, nums ...int) []*docindex.PageEntry {
	out := make([]*docindex.PageEntry, len(nums))
	for i, n := range nums {
		out[i] = &docindex.PageEntry{DocPath: docPath, PageNum: n}
	}
	return out
}

// upsertPages writes len(item.corpora) page rows for item, as a completed
// batch of that document would have left them.
func upsertPages(t *testing.T, store *docindex.Store, path string, nums ...int) {
	t.Helper()
	for _, n := range nums {
		if err := store.Upsert(&docindex.PageEntry{DocPath: path, PageNum: n, Keywords: []string{"k"}, Labels: []string{"L"}}); err != nil {
			t.Fatalf("upsert %s page %d: %v", path, n, err)
		}
	}
}

// TestMissingPageNums pins the set comparison a batch's validation rests on:
// only the pages the run expected can be missing, and a stored page the run did
// not expect is not a failure.
func TestMissingPageNums(t *testing.T) {
	tests := []struct {
		name     string
		expected []PageCorpus
		got      []*docindex.PageEntry
		want     []int
	}{
		{name: "every expected page stored", expected: pageCorpora(1, 2, 3), got: storedPages("/proj/a.pdf", 1, 2, 3)},
		{name: "middle page missing", expected: pageCorpora(1, 2, 3), got: storedPages("/proj/a.pdf", 1, 3), want: []int{2}},
		{name: "nothing stored", expected: pageCorpora(4, 5), got: nil, want: []int{4, 5}},
		{name: "extra stored pages are ignored", expected: pageCorpora(1), got: storedPages("/proj/a.pdf", 1, 2, 99)},
		{name: "no expected pages", expected: nil, got: storedPages("/proj/a.pdf", 1)},
		{name: "nil stored entry is skipped", expected: pageCorpora(1, 2), got: []*docindex.PageEntry{nil, {PageNum: 2}}, want: []int{1}},
		{name: "duplicate expected page counted once", expected: pageCorpora(1, 1), got: storedPages("/proj/a.pdf", 1)},
		{name: "tail of a split document missing", expected: pageCorpora(101, 102), got: storedPages("/proj/a.pdf", 101), want: []int{102}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := missingPageNums(tc.expected, tc.got)
			if len(got) != len(tc.want) {
				t.Fatalf("missingPageNums = %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("missingPageNums[%d] = %d, want %d", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestFormatPageNums(t *testing.T) {
	tests := []struct {
		nums []int
		want string
	}{
		{nil, ""},
		{[]int{4}, "4"},
		{[]int{5, 6}, "5-6"},
		{[]int{1, 2, 3, 7, 9, 10}, "1-3,7,9-10"},
	}
	for _, tc := range tests {
		if got := formatPageNums(tc.nums); got != tc.want {
			t.Errorf("formatPageNums(%v) = %q, want %q", tc.nums, got, tc.want)
		}
	}
}

func TestBatchFailureError(t *testing.T) {
	if err := batchFailureError(0, 5); err != nil {
		t.Fatalf("batchFailureError(0, 5) = %v, want nil", err)
	}
	err := batchFailureError(2, 5)
	if err == nil {
		t.Fatal("batchFailureError(2, 5) = nil, want an error")
	}
	want := "2 of 5 index batches failed; affected documents were dropped from the index and will be retried on the next run"
	if err.Error() != want {
		t.Errorf("batchFailureError(2, 5) = %q, want %q", err.Error(), want)
	}
}

// TestFinishDocumentDropsPartialDocument is the regression for the run that
// printed "Indexing complete" over 100 of 137 pages: pages 1-2 of a three-page
// document arrived, page 3 never did, and the whole document must go so the
// next run re-indexes it instead of skipping it as already done.
func TestFinishDocumentDropsPartialDocument(t *testing.T) {
	const path = "/workspace/spec.docx"
	store := newTestDocStore(t)
	idx := New("/workspace", store, nil)
	item := docItem{path: path, corpora: pageCorpora(1, 2, 3), isDocx: true}
	upsertPages(t, store, path, 1, 2)

	err := idx.finishDocument(item, nil)
	if err == nil {
		t.Fatal("finishDocument = nil, want a missing-pages error")
	}
	if want := "missing pages 3"; !strings.Contains(err.Error(), want) {
		t.Errorf("finishDocument error = %q, want it to name %q", err, want)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("finishDocument error = %q, want it to name the document %q", err, path)
	}
	if got := idx.progress.Failed.Load(); got != 1 {
		t.Errorf("progress.Failed = %d, want 1", got)
	}
	if got := idx.progress.Completed.Load(); got != 0 {
		t.Errorf("progress.Completed = %d, want 0 for a failed document", got)
	}
	rows, err := store.GetByDoc(path)
	if err != nil {
		t.Fatalf("GetByDoc: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("document still has %d stored rows after a failed batch, want 0", len(rows))
	}
}

func TestFinishDocumentCompletesFullyStoredDocument(t *testing.T) {
	const path = "/workspace/spec.docx"
	store := newTestDocStore(t)
	idx := New("/workspace", store, nil)
	item := docItem{path: path, corpora: pageCorpora(1, 2, 3), isDocx: true}
	upsertPages(t, store, path, 1, 2, 3)

	if err := idx.finishDocument(item, nil); err != nil {
		t.Fatalf("finishDocument = %v, want nil", err)
	}
	if got := idx.progress.Completed.Load(); got != 1 {
		t.Errorf("progress.Completed = %d, want 1", got)
	}
	if got := idx.progress.Failed.Load(); got != 0 {
		t.Errorf("progress.Failed = %d, want 0", got)
	}
	rows, err := store.GetByDoc(path)
	if err != nil {
		t.Fatalf("GetByDoc: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("document has %d stored rows, want 3 kept", len(rows))
	}
}

// TestFinishDocumentLoopErrorDropsWholeDocument: a loop error fails the
// document even when every expected page happens to be stored — the pages came
// from an earlier batch of the same document, and keeping them would make the
// next run skip the document the failed batch was supposed to finish.
func TestFinishDocumentLoopErrorDropsWholeDocument(t *testing.T) {
	const path = "/workspace/spec.docx"
	store := newTestDocStore(t)
	idx := New("/workspace", store, nil)
	item := docItem{path: path, corpora: pageCorpora(1, 2, 3), isDocx: true}
	upsertPages(t, store, path, 1, 2, 3)

	err := idx.finishDocument(item, errors.New("provider exploded"))
	if err == nil || !strings.Contains(err.Error(), "provider exploded") {
		t.Fatalf("finishDocument = %v, want the loop error", err)
	}
	if got := idx.progress.Failed.Load(); got != 1 {
		t.Errorf("progress.Failed = %d, want 1", got)
	}
	rows, err := store.GetByDoc(path)
	if err != nil {
		t.Fatalf("GetByDoc: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("document still has %d stored rows after a loop error, want 0", len(rows))
	}
}

// TestFinishDocumentDefersDropWhileSiblingChunkRuns is the other half of the
// split-document race: a chunk that fails while a sibling is still to settle
// must not delete the document out from under it, or the sibling's stored pages
// vanish before it validates and the run reports a failure that never happened.
func TestFinishDocumentDefersDropWhileSiblingChunkRuns(t *testing.T) {
	const path = "/workspace/long.docx"
	store := newTestDocStore(t)
	idx := New("/workspace", store, nil)

	item := splitDocItem(path)
	for n := 1; n <= batchSize; n++ {
		upsertPages(t, store, path, n)
	}
	batches := idx.assembleBatches([]docItem{item})
	if len(batches) != 3 {
		t.Fatalf("assembled %d batches, want 3", len(batches))
	}
	idx.settler = newDocSettler(batches, idx.progress)
	t.Cleanup(func() { idx.settler = nil })

	// Chunk 2 fails first, while chunks 1 and 3 are still running: the pages
	// chunk 1 stored must survive until it has validated.
	if err := idx.finishDocument(batches[1].items[0], nil); err == nil {
		t.Fatal("chunk 2: finishDocument = nil, want a missing-pages error")
	}
	if got := storedRowCount(t, store, path); got != batchSize {
		t.Fatalf("document has %d rows after a mid-run failure, want the %d chunk 1 stored kept", got, batchSize)
	}

	if err := idx.finishDocument(batches[0].items[0], nil); err != nil {
		t.Fatalf("chunk 1: %v", err)
	}
	// The document still fails as a whole, and its final chunk drops it.
	if err := idx.finishDocument(batches[2].items[0], nil); err == nil {
		t.Fatal("chunk 3: finishDocument = nil, want a missing-pages error")
	}
	if got := storedRowCount(t, store, path); got != 0 {
		t.Errorf("failed split document kept %d rows, want it dropped entirely", got)
	}
	if got := idx.progress.Failed.Load(); got != 1 {
		t.Errorf("progress.Failed = %d, want 1", got)
	}
	if got := idx.progress.Completed.Load(); got != 0 {
		t.Errorf("progress.Completed = %d, want 0", got)
	}
}

// TestFinishDocumentNilStore pins the contract the existing tests rely on: an
// Indexer built without a store still reports per-file progress and never
// touches a database.
func TestFinishDocumentNilStore(t *testing.T) {
	idx := New("/workspace", nil, nil)
	item := docItem{path: "/workspace/a.go", corpora: pageCorpora(1), keywordCount: 1}

	if err := idx.finishDocument(item, nil); err != nil {
		t.Fatalf("finishDocument(nil store) = %v, want nil", err)
	}
	if got := idx.progress.Completed.Load(); got != 1 {
		t.Errorf("progress.Completed = %d, want 1", got)
	}
	if err := idx.finishDocument(item, errors.New("loop failed")); err == nil {
		t.Fatal("finishDocument(nil store, loop error) = nil, want the loop error")
	}
	if got := idx.progress.Failed.Load(); got != 1 {
		t.Errorf("progress.Failed = %d, want 1", got)
	}

	// The post-run sweep must be a no-op rather than a panic without a store.
	idx.dropIncompleteDocuments([]docItem{item})
}

// TestDropIncompleteDocumentsSweepsRacingBatches pins the sweep that closes the
// split-document race: batch 1 (pages 1-100) completed, batch 2 (page 101)
// failed and deleted the document, but a straggler from batch 1 wrote its pages
// back afterwards. The sweep runs once every batch has finished, so the partial
// document cannot survive the run.
func TestDropIncompleteDocumentsSweepsRacingBatches(t *testing.T) {
	const spec = "/workspace/spec.docx"
	const notes = "/workspace/notes.md"
	store := newTestDocStore(t)
	idx := New("/workspace", store, nil)

	full := docItem{path: spec, isDocx: true}
	for n := 1; n <= 100; n++ {
		full.corpora = append(full.corpora, PageCorpus{PageNum: n})
	}
	full.corpora = append(full.corpora, PageCorpus{PageNum: 101})
	upsertPages(t, store, spec, 1, 2, 3)

	done := docItem{path: notes, corpora: pageCorpora(1)}
	upsertPages(t, store, notes, 1)

	idx.dropIncompleteDocuments([]docItem{full, done})

	specRows, err := store.GetByDoc(spec)
	if err != nil {
		t.Fatalf("GetByDoc(spec): %v", err)
	}
	if len(specRows) != 0 {
		t.Errorf("partial document kept %d rows, want it dropped entirely", len(specRows))
	}
	noteRows, err := store.GetByDoc(notes)
	if err != nil {
		t.Fatalf("GetByDoc(notes): %v", err)
	}
	if len(noteRows) != 1 {
		t.Errorf("complete document lost its row: %d stored, want 1", len(noteRows))
	}
}

// TestSettleBatchesSweepsBeforeReportingCancellation pins the ordering that
// makes the drop-on-failure guarantee hold on a cancelled run: a batch failed
// and the run was cancelled, and the sweep still has to remove the partially
// stored document (a sibling batch of the same long document can have written
// pages after the failing batch's delete). The complete document must survive,
// and the caller still learns the run was cancelled.
func TestSettleBatchesSweepsBeforeReportingCancellation(t *testing.T) {
	const partial = "/workspace/partial.docx"
	const complete = "/workspace/complete.md"
	store := newTestDocStore(t)
	idx := New("/workspace", store, &agent.LoopRunner{})

	partialItem := docItem{path: partial, corpora: pageCorpora(1, 2, 3), isDocx: true}
	completeItem := docItem{path: complete, corpora: pageCorpora(1)}
	upsertPages(t, store, partial, 1, 2)
	upsertPages(t, store, complete, 1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := idx.settleBatches(ctx, []docItem{partialItem, completeItem}, 2, 1, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("settleBatches = %v, want context.Canceled", err)
	}
	if got := storedRowCount(t, store, partial); got != 0 {
		t.Errorf("partial document kept %d rows on a cancelled run, want it dropped", got)
	}
	if got := storedRowCount(t, store, complete); got != 1 {
		t.Errorf("complete document lost its row: %d stored, want 1", got)
	}
}

// TestSettleBatchesCancelledWithoutFailuresDeletesNothing pins the other half
// of the ordering: cancellation alone is not a reason to drop documents. The
// sweep is driven by a failed batch, so an incomplete document survives a run
// that failed nothing and was cancelled before it could finish.
func TestSettleBatchesCancelledWithoutFailuresDeletesNothing(t *testing.T) {
	const path = "/workspace/spec.docx"
	store := newTestDocStore(t)
	idx := New("/workspace", store, &agent.LoopRunner{})
	item := docItem{path: path, corpora: pageCorpora(1, 2), isDocx: true}
	upsertPages(t, store, path, 1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := idx.settleBatches(ctx, []docItem{item}, 1, 0, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("settleBatches = %v, want context.Canceled", err)
	}
	if got := storedRowCount(t, store, path); got != 1 {
		t.Errorf("cancelled run without a failed batch deleted rows: %d stored, want 1 kept", got)
	}
}

// TestSettleBatchesDropsPartialDocumentOnFailure pins the uncancelled failure
// path: the partial document goes, the complete one stays, and the run reports
// its batch failure so the caller exits non-zero instead of printing
// "Indexing complete".
func TestSettleBatchesDropsPartialDocumentOnFailure(t *testing.T) {
	const partial = "/workspace/partial.docx"
	const complete = "/workspace/complete.md"
	store := newTestDocStore(t)
	idx := New("/workspace", store, &agent.LoopRunner{})

	partialItem := docItem{path: partial, corpora: pageCorpora(1, 2, 3), isDocx: true}
	completeItem := docItem{path: complete, corpora: pageCorpora(1)}
	upsertPages(t, store, partial, 1, 2)
	upsertPages(t, store, complete, 1)

	err := idx.settleBatches(context.Background(), []docItem{partialItem, completeItem}, 2, 1, nil)
	if err == nil {
		t.Fatal("settleBatches = nil, want a batch failure error")
	}
	if !strings.Contains(err.Error(), "index batches failed") {
		t.Errorf("settleBatches error = %q, want it to mention %q", err, "index batches failed")
	}
	if got := storedRowCount(t, store, partial); got != 0 {
		t.Errorf("partial document kept %d rows after a failed batch, want it dropped", got)
	}
	if got := storedRowCount(t, store, complete); got != 1 {
		t.Errorf("complete document lost its row: %d stored, want 1", got)
	}
}

// TestSettleBatchesSweepsOnlyFailedDocuments pins the narrowing of the sweep: a
// failed batch names the documents it touched, and only those are re-read and
// dropped. A document whose batch succeeded was written whole — an incomplete
// one had its batch fail, so it is named — and re-reading every page row of the
// run would only be a slower way to reach the same index.
func TestSettleBatchesSweepsOnlyFailedDocuments(t *testing.T) {
	const failedDoc = "/workspace/failed.docx"
	const untouched = "/workspace/untouched.docx"
	store := newTestDocStore(t)
	idx := New("/workspace", store, &agent.LoopRunner{})

	failedItem := docItem{path: failedDoc, corpora: pageCorpora(1, 2, 3), isDocx: true}
	untouchedItem := docItem{path: untouched, corpora: pageCorpora(1, 2), isDocx: true}
	upsertPages(t, store, failedDoc, 1)
	upsertPages(t, store, untouched, 1)

	err := idx.settleBatches(context.Background(), []docItem{failedItem, untouchedItem}, 2, 1,
		map[string]struct{}{failedDoc: {}})
	if err == nil {
		t.Fatal("settleBatches = nil, want a batch failure error")
	}
	if got := storedRowCount(t, store, failedDoc); got != 0 {
		t.Errorf("failed document kept %d rows, want it dropped", got)
	}
	if got := storedRowCount(t, store, untouched); got != 1 {
		t.Errorf("document outside the failed batches has %d rows, want its row untouched", got)
	}
}

// storedRowCount reports how many page rows the store holds for docPath.
func storedRowCount(t *testing.T, store *docindex.Store, docPath string) int {
	t.Helper()
	rows, err := store.GetByDoc(docPath)
	if err != nil {
		t.Fatalf("GetByDoc(%s): %v", docPath, err)
	}
	return len(rows)
}

// TestIsDocxExt pins the indexer's Word gate to the shared list: the three
// OOXML containers pass, and the two formats no reader handles do not.
func TestIsDocxExt(t *testing.T) {
	for _, ext := range []string{".docx", ".docm", ".dotx", ".DOCX", ".Docm"} {
		if !isDocxExt(ext) {
			t.Errorf("isDocxExt(%q) = false, want true", ext)
		}
	}
	for _, ext := range []string{".doc", ".dotm", ".pdf", "", ".docxx"} {
		if isDocxExt(ext) {
			t.Errorf("isDocxExt(%q) = true, want false", ext)
		}
	}
}

// TestIsPDFExt pins the indexer's PDF gate to the shared list, so the store and
// the extractor cannot disagree about what a PDF is.
func TestIsPDFExt(t *testing.T) {
	for _, ext := range []string{".pdf", ".PDF"} {
		if !isPDFExt(ext) {
			t.Errorf("isPDFExt(%q) = false, want true", ext)
		}
	}
	for _, ext := range []string{".docx", ".doc", "", ".pdfx", ".pxdf"} {
		if isPDFExt(ext) {
			t.Errorf("isPDFExt(%q) = true, want false", ext)
		}
	}
}

// writeTestDocx writes a minimal DOCX archive whose word/document.xml holds
// body as the contents of <w:body>. An empty body yields a document with no
// paragraphs, and therefore no pages.
func writeTestDocx(t *testing.T, docxPath, body string) {
	t.Helper()
	f, err := os.Create(docxPath)
	if err != nil {
		t.Fatalf("create %s: %v", docxPath, err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatalf("create document.xml: %v", err)
	}
	doc := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		body + `</w:body></w:document>`
	if _, err := w.Write([]byte(doc)); err != nil {
		t.Fatalf("write document.xml: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close docx archive: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", docxPath, err)
	}
}

// indexFakeProvider stands in for a model in the run tests: it answers every
// step with one text delta and a "stop" finish, and writes the page rows the
// run expects to find, so a document's validation succeeds without a real
// indexing agent. It never emits a tool call.
type indexFakeProvider struct {
	mu    sync.Mutex
	store *docindex.Store
	pages map[string][]int // doc path -> page numbers to store on each step
}

func (p *indexFakeProvider) ID() string { return "mock" }

func (p *indexFakeProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", ProviderID: "mock"}}
}

func (p *indexFakeProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	p.mu.Lock()
	for docPath, nums := range p.pages {
		for _, n := range nums {
			if err := p.store.Upsert(&docindex.PageEntry{
				DocPath: docPath, PageNum: n, Keywords: []string{"k"}, Labels: []string{"L"},
			}); err != nil {
				slog.Error("fake provider upsert failed", "path", docPath, "page", n, "err", err)
			}
		}
	}
	p.mu.Unlock()

	ch := make(chan provider.StreamEvent, 4)
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "done"}
		fr := "stop"
		ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
	}()
	return ch, nil
}

// newIndexRunFixture builds an Indexer whose loop runner is backed by
// indexFakeProvider, so Run can be exercised end to end against a temp dir.
// pages says which page rows the fake model stores when a batch runs.
func newIndexRunFixture(t *testing.T, dir string, pages map[string][]int) (*Indexer, *docindex.Store) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if err := session.SetModelCapability(database, &session.ModelCapability{
		ModelID: "mock-model", SupportsImages: false, ProbedAt: session.Now(),
	}); err != nil {
		t.Fatalf("set model capability: %v", err)
	}

	docStore := docindex.NewStore(database)
	reg := provider.NewRegistry()
	reg.Register(&indexFakeProvider{store: docStore, pages: pages})
	lr := &agent.LoopRunner{
		Store:    session.NewStore(database),
		Bus:      bus.New(64),
		Registry: reg,
		Tools:    tool.NewRegistry(),
		Dir:      dir,
		MaxSteps: 4,
	}
	return New(dir, docStore, lr).WithModel("mock-model"), docStore
}

// TestRunReportsUnreadableDocument is the regression for a run whose only
// candidate could not be read: it must fail loudly, naming the file, instead of
// printing "Indexing complete" over an index the run has already emptied.
func TestRunReportsUnreadableDocument(t *testing.T) {
	dir := t.TempDir()
	corrupt := filepath.Join(dir, "broken.docx")
	if err := os.WriteFile(corrupt, []byte("not a zip"), 0o644); err != nil {
		t.Fatalf("write %s: %v", corrupt, err)
	}

	idx := New(dir, newTestDocStore(t), &agent.LoopRunner{})
	err := idx.Run(context.Background())
	if err == nil {
		t.Fatal("Run = nil, want an error naming the unreadable document")
	}
	if !strings.Contains(err.Error(), corrupt) {
		t.Errorf("Run error = %q, want it to name %q", err, corrupt)
	}
	if got := idx.progress.Total.Load(); got != 1 {
		t.Errorf("progress.Total = %d, want 1: the selected file counts even though it failed", got)
	}
	if got := idx.progress.Failed.Load(); got != 1 {
		t.Errorf("progress.Failed = %d, want 1", got)
	}
	if got := idx.progress.Completed.Load(); got != 0 {
		t.Errorf("progress.Completed = %d, want 0", got)
	}
}

// TestRunCountsZeroPageDocumentAsFailed pins the other silent drop: a document
// that parses but yields no pages was selected and cannot be indexed, so the
// run fails and the counters show it.
func TestRunCountsZeroPageDocumentAsFailed(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.docx")
	writeTestDocx(t, empty, "")

	idx := New(dir, newTestDocStore(t), &agent.LoopRunner{})
	err := idx.Run(context.Background())
	if err == nil {
		t.Fatal("Run = nil, want an error naming the zero-page document")
	}
	if !strings.Contains(err.Error(), empty) {
		t.Errorf("Run error = %q, want it to name %q", err, empty)
	}
	if !strings.Contains(err.Error(), "no extractable pages") {
		t.Errorf("Run error = %q, want it to report the document had no pages", err)
	}
	if got := idx.progress.Total.Load(); got != 1 {
		t.Errorf("progress.Total = %d, want 1", got)
	}
	if got := idx.progress.Failed.Load(); got != 1 {
		t.Errorf("progress.Failed = %d, want 1", got)
	}
	if got := idx.progress.Completed.Load(); got != 0 {
		t.Errorf("progress.Completed = %d, want 0", got)
	}
}

// TestRunReportsUnreadableDocumentAlongsideIndexedOnes pins the partial run:
// the readable document is indexed and counted, and the run still fails naming
// the one it could not read.
func TestRunReportsUnreadableDocumentAlongsideIndexedOnes(t *testing.T) {
	dir := t.TempDir()
	corrupt := filepath.Join(dir, "broken.docx")
	if err := os.WriteFile(corrupt, []byte("not a zip"), 0o644); err != nil {
		t.Fatalf("write %s: %v", corrupt, err)
	}
	good := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(good, []byte("index me\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", good, err)
	}

	idx, store := newIndexRunFixture(t, dir, map[string][]int{good: {1}})
	err := idx.Run(context.Background())
	if err == nil {
		t.Fatal("Run = nil, want an error naming the unreadable document")
	}
	if !strings.Contains(err.Error(), corrupt) {
		t.Errorf("Run error = %q, want it to name %q", err, corrupt)
	}
	if got := idx.progress.Total.Load(); got != 2 {
		t.Errorf("progress.Total = %d, want 2", got)
	}
	if got := idx.progress.Completed.Load(); got != 1 {
		t.Errorf("progress.Completed = %d, want 1 for the readable document", got)
	}
	if got := idx.progress.Failed.Load(); got != 1 {
		t.Errorf("progress.Failed = %d, want 1 for the unreadable document", got)
	}
	if got := storedRowCount(t, store, good); got != 1 {
		t.Errorf("readable document has %d stored rows, want 1", got)
	}
	if got := storedRowCount(t, store, corrupt); got != 0 {
		t.Errorf("unreadable document has %d stored rows, want 0", got)
	}
}

// lastProgressPhase drains events and reports the phase of the last
// docindex.progress event on it, or "" when the channel holds none. Run
// publishes synchronously into the bus's subscriber buffers, so draining after
// Run returns sees every event of a small run.
func lastProgressPhase(t *testing.T, events <-chan bus.Event) string {
	t.Helper()
	phase := ""
	for {
		select {
		case evt := <-events:
			if evt.Type != "docindex.progress" {
				continue
			}
			var props struct {
				Phase string `json:"phase"`
			}
			if err := json.Unmarshal(evt.Properties, &props); err != nil {
				t.Fatalf("decode %s properties: %v", evt.Type, err)
			}
			phase = props.Phase
		default:
			return phase
		}
	}
}

// TestRunPublishesTerminalPhaseOnEveryExit pins the contract a phase-driven
// consumer relies on: whichever path Run leaves by — success, a document that
// cannot be read, or a cancelled context — its last progress event carries the
// terminal "done" phase. Only the success path used to publish it, so a
// consumer waiting for the run to finish waited forever on a failed one.
func TestRunPublishesTerminalPhaseOnEveryExit(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		dir := t.TempDir()
		good := filepath.Join(dir, "notes.txt")
		if err := os.WriteFile(good, []byte("index me\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", good, err)
		}
		idx, _ := newIndexRunFixture(t, dir, map[string][]int{good: {1}})
		events := idx.loopRunner.Bus.SubscribeAll()
		if err := idx.Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := lastProgressPhase(t, events); got != "done" {
			t.Errorf("last progress phase = %q, want %q", got, "done")
		}
	})

	t.Run("unreadable document", func(t *testing.T) {
		dir := t.TempDir()
		corrupt := filepath.Join(dir, "broken.docx")
		if err := os.WriteFile(corrupt, []byte("not a zip"), 0o644); err != nil {
			t.Fatalf("write %s: %v", corrupt, err)
		}
		idx, _ := newIndexRunFixture(t, dir, nil)
		events := idx.loopRunner.Bus.SubscribeAll()
		if err := idx.Run(context.Background()); err == nil {
			t.Fatal("Run = nil, want the unreadable document reported")
		}
		if got := lastProgressPhase(t, events); got != "done" {
			t.Errorf("last progress phase = %q, want %q", got, "done")
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		dir := t.TempDir()
		doc := filepath.Join(dir, "notes.docx")
		writeTestDocx(t, doc, `<w:p><w:r><w:t>Page one body.</w:t></w:r></w:p>`)
		idx, _ := newIndexRunFixture(t, dir, map[string][]int{doc: {1}})
		events := idx.loopRunner.Bus.SubscribeAll()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := idx.Run(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v, want context.Canceled", err)
		}
		if got := lastProgressPhase(t, events); got != "done" {
			t.Errorf("last progress phase = %q, want %q", got, "done")
		}
	})
}

// TestRunDropsDocumentWhenAChunkFails exercises the whole run over a document
// large enough to split into three batches: the fake model stores only the
// first chunk's pages, so the other two fail validation, and the document must
// leave the index empty rather than keeping the one chunk that landed.
func TestRunDropsDocumentWhenAChunkFails(t *testing.T) {
	dir := t.TempDir()
	doc := filepath.Join(dir, "long.docx")
	var body strings.Builder
	for n := 1; n <= 2*batchSize+50; n++ {
		if n > 1 {
			body.WriteString(`<w:p><w:r><w:br w:type="page"/></w:r></w:p>`)
		}
		fmt.Fprintf(&body, `<w:p><w:r><w:t>Page %d body.</w:t></w:r></w:p>`, n)
	}
	writeTestDocx(t, doc, body.String())

	// The model stores pages 1..batchSize on every batch it runs, so only the
	// chunk that expects exactly those pages validates.
	firstChunk := make([]int, batchSize)
	for n := range firstChunk {
		firstChunk[n] = n + 1
	}
	idx, store := newIndexRunFixture(t, dir, map[string][]int{doc: firstChunk})

	err := idx.Run(context.Background())
	if err == nil {
		t.Fatal("Run = nil, want the failed chunks reported")
	}
	if !strings.Contains(err.Error(), "2 of 3 index batches failed") {
		t.Errorf("Run error = %q, want it to report 2 of 3 batches failed", err)
	}
	if got := storedRowCount(t, store, doc); got != 0 {
		t.Errorf("document has %d stored rows, want 0: a partially indexed document must be dropped", got)
	}
	if got := idx.progress.Total.Load(); got != 1 {
		t.Errorf("progress.Total = %d, want 1", got)
	}
	if got := idx.progress.Failed.Load(); got != 1 {
		t.Errorf("progress.Failed = %d, want 1", got)
	}
	if got := idx.progress.Completed.Load(); got != 0 {
		t.Errorf("progress.Completed = %d, want 0", got)
	}
}

// splitDocItem builds a DOCX item carrying 2*batchSize+50 pages — 250 at the
// current batch size — which assembleBatches splits into three chunks of one
// document.
func splitDocItem(docPath string) docItem {
	nums := make([]int, 0, 2*batchSize+50)
	for n := 1; n <= 2*batchSize+50; n++ {
		nums = append(nums, n)
	}
	return docItem{path: docPath, corpora: pageCorpora(nums...), isDocx: true}
}

// TestDocSettlerCountsSplitDocumentOnce pins the progress unit: Total counts
// documents, so a document split across three chunks is counted once, and only
// after its last chunk has settled.
func TestDocSettlerCountsSplitDocumentOnce(t *testing.T) {
	const path = "/workspace/long.docx"
	store := newTestDocStore(t)
	idx := New("/workspace", store, nil)

	item := splitDocItem(path)
	nums := make([]int, 0, len(item.corpora))
	for _, c := range item.corpora {
		nums = append(nums, c.PageNum)
	}
	upsertPages(t, store, path, nums...)

	batches := idx.assembleBatches([]docItem{item})
	if len(batches) != 3 {
		t.Fatalf("assembled %d batches, want 3", len(batches))
	}
	idx.settler = newDocSettler(batches, idx.progress)
	t.Cleanup(func() { idx.settler = nil })
	// Total comes from the settler's own document count, the unit Run uses
	// (len(toIndex)); a hard-coded 1 asserted nothing but itself.
	idx.progress.Total.Store(int32(len(idx.settler.remaining)))
	if got := idx.progress.Total.Load(); got != 1 {
		t.Fatalf("progress.Total = %d derived from the settler, want 1 document for %d chunks", got, len(batches))
	}

	for i, b := range batches {
		if err := idx.finishDocument(b.items[0], nil); err != nil {
			t.Fatalf("chunk %d: %v", i+1, err)
		}
		if i < len(batches)-1 {
			if got := idx.progress.Completed.Load(); got != 0 {
				t.Errorf("progress.Completed = %d after chunk %d, want 0 until the last chunk settles", got, i+1)
			}
			if got := idx.progress.Failed.Load(); got != 0 {
				t.Errorf("progress.Failed = %d after chunk %d, want 0", got, i+1)
			}
		}
	}
	if got := idx.progress.Completed.Load(); got != 1 {
		t.Errorf("progress.Completed = %d, want 1 for one fully indexed document", got)
	}
	if got := idx.progress.Failed.Load(); got != 0 {
		t.Errorf("progress.Failed = %d, want 0", got)
	}
	if got := idx.progress.Total.Load(); got != 1 {
		t.Errorf("progress.Total = %d, want 1", got)
	}
}

// TestDocSettlerCountsSplitDocumentFailedOnce pins the other half: a chunk
// succeeding is not the document succeeding. One failed chunk fails the whole
// document once, and its surviving pages are dropped.
func TestDocSettlerCountsSplitDocumentFailedOnce(t *testing.T) {
	const path = "/workspace/long.docx"
	store := newTestDocStore(t)
	idx := New("/workspace", store, nil)

	item := splitDocItem(path)
	// Only the first chunk's pages land, so chunks 2 and 3 fail validation.
	for n := 1; n <= batchSize; n++ {
		upsertPages(t, store, path, n)
	}

	batches := idx.assembleBatches([]docItem{item})
	if len(batches) != 3 {
		t.Fatalf("assembled %d batches, want 3", len(batches))
	}
	idx.settler = newDocSettler(batches, idx.progress)
	t.Cleanup(func() { idx.settler = nil })
	// Total comes from the settler's own document count, the unit Run uses
	// (len(toIndex)); a hard-coded 1 asserted nothing but itself.
	idx.progress.Total.Store(int32(len(idx.settler.remaining)))
	if got := idx.progress.Total.Load(); got != 1 {
		t.Fatalf("progress.Total = %d derived from the settler, want 1 document for %d chunks", got, len(batches))
	}

	for i, b := range batches {
		err := idx.finishDocument(b.items[0], nil)
		if i == 0 {
			if err != nil {
				t.Fatalf("chunk 1: %v", err)
			}
			if got := idx.progress.Completed.Load(); got != 0 {
				t.Errorf("progress.Completed = %d after a successful chunk of a split document, want 0", got)
			}
			continue
		}
		if err == nil {
			t.Fatalf("chunk %d: finishDocument = nil, want a missing-pages error", i+1)
		}
	}
	if got := idx.progress.Failed.Load(); got != 1 {
		t.Errorf("progress.Failed = %d, want 1 for one failed document", got)
	}
	if got := idx.progress.Completed.Load(); got != 0 {
		t.Errorf("progress.Completed = %d, want 0", got)
	}
	if got := idx.progress.Total.Load(); got != 1 {
		t.Errorf("progress.Total = %d, want 1", got)
	}
	if got := storedRowCount(t, store, path); got != 0 {
		t.Errorf("failed split document kept %d rows, want it dropped entirely", got)
	}
}

// closedIndexerStore returns a store and an Indexer over it whose database has
// been closed, so every read and delete through the store fails.
func closedIndexerStore(t *testing.T) *Indexer {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	store := docindex.NewStore(database)
	idx := New("/workspace", store, &agent.LoopRunner{})
	if err := database.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	return idx
}

// TestDropIncompleteDocumentsReportsDeleteFailure pins B3: a partial document
// the sweep cannot drop must be reported, naming the file, so the run does not
// claim the index is consistent while a row keeps the document "complete"
// forever.
func TestDropIncompleteDocumentsReportsDeleteFailure(t *testing.T) {
	const path = "/workspace/spec.docx"
	idx := closedIndexerStore(t)

	err := idx.dropIncompleteDocuments([]docItem{{path: path, corpora: pageCorpora(1, 2), isDocx: true}})
	if err == nil {
		t.Fatal("dropIncompleteDocuments = nil, want the delete failure reported")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("dropIncompleteDocuments error = %q, want it to name %q", err, path)
	}
	if !strings.Contains(err.Error(), "drop incomplete document") {
		t.Errorf("dropIncompleteDocuments error = %q, want it to describe the failed drop", err)
	}
}

// TestSettleBatchesJoinsSweepFailure pins that the sweep failure reaches the
// caller alongside the batch failure, rather than only being logged.
func TestSettleBatchesJoinsSweepFailure(t *testing.T) {
	const path = "/workspace/spec.docx"
	idx := closedIndexerStore(t)

	err := idx.settleBatches(context.Background(), []docItem{{path: path, corpora: pageCorpora(1, 2), isDocx: true}}, 1, 1, nil)
	if err == nil {
		t.Fatal("settleBatches = nil, want the batch and sweep failures reported")
	}
	if !strings.Contains(err.Error(), "index batches failed") {
		t.Errorf("settleBatches error = %q, want it to mention %q", err, "index batches failed")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("settleBatches error = %q, want it to name %q", err, path)
	}
}
