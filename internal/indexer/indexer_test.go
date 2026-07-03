package indexer

import (
	"testing"
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