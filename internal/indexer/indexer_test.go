package indexer

import (
	"context"
	"path/filepath"
	"testing"

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

// A vendored tree-sitter grammar is a generated parser.c running to tens of
// megabytes, and .c is an indexed extension — so the directory skip is the only
// thing keeping it out of an index. These two facts are what make each other
// matter, so they are asserted together: drop either and a single generated
// file dominates the workspace index.
func TestSkipDirsCoversVendoredGrammars(t *testing.T) {
	if !IsTextFile(".c") {
		t.Fatal("IsTextFile(\".c\") = false; the grammars skip below is guarding nothing")
	}
	if _, ok := skipDirs["grammars"]; !ok {
		t.Error(`skipDirs is missing "grammars"; a vendored generated parser.c would be indexed`)
	}
}
