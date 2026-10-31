package docindex

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	return NewStore(database)
}

func TestListDocPaths(t *testing.T) {
	s := newTestStore(t)

	// Insert entries for three documents under /proj.
	entries := []*PageEntry{
		{DocPath: "/proj/a.go", PageNum: 1, Keywords: []string{"foo"}, Labels: []string{"L1"}},
		{DocPath: "/proj/b.go", PageNum: 1, Keywords: []string{"bar"}, Labels: []string{"L2"}},
		{DocPath: "/proj/doc.pdf", PageNum: 1, Keywords: []string{"pdf"}, Labels: []string{"L3"}},
		{DocPath: "/proj/doc.pdf", PageNum: 2, Keywords: []string{"pdf2"}, Labels: []string{"L4"}},
		{DocPath: "/other/c.go", PageNum: 1, Keywords: []string{"other"}, Labels: []string{"L5"}},
	}
	for _, e := range entries {
		if err := s.Upsert(e); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	paths, err := s.ListDocPaths("/proj")
	if err != nil {
		t.Fatalf("ListDocPaths: %v", err)
	}

	want := []string{"/proj/a.go", "/proj/b.go", "/proj/doc.pdf"}
	if len(paths) != len(want) {
		t.Fatalf("expected %d paths, got %d: %v", len(want), len(paths), paths)
	}
	for i, p := range want {
		if paths[i] != p {
			t.Errorf("path[%d] = %q, want %q", i, paths[i], p)
		}
	}
}

func TestDeleteByDoc(t *testing.T) {
	s := newTestStore(t)

	if err := s.Upsert(&PageEntry{DocPath: "/proj/a.go", PageNum: 1, Keywords: []string{"foo"}, Labels: []string{}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.Upsert(&PageEntry{DocPath: "/proj/a.go", PageNum: 2, Keywords: []string{"bar"}, Labels: []string{}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Confirm it exists.
	indexed, err := s.IsDocIndexed("/proj/a.go")
	if err != nil {
		t.Fatalf("IsDocIndexed: %v", err)
	}
	if !indexed {
		t.Fatal("expected doc to be indexed before delete")
	}

	if err := s.DeleteByDoc("/proj/a.go"); err != nil {
		t.Fatalf("DeleteByDoc: %v", err)
	}

	indexed, err = s.IsDocIndexed("/proj/a.go")
	if err != nil {
		t.Fatalf("IsDocIndexed after delete: %v", err)
	}
	if indexed {
		t.Fatal("expected doc to NOT be indexed after delete")
	}
}

// ListDocModTimes is what tells an incremental run whether a file has been
// rewritten since it was indexed, so it must report one time per path — the
// newest — and only for paths under the given directory.
func TestListDocModTimes(t *testing.T) {
	s := newTestStore(t)

	entries := []*PageEntry{
		{DocPath: "/proj/a.go", PageNum: 1, Keywords: []string{"k"}, Labels: []string{"L"}, ModTime: 1000},
		// A multi-page document carries the same file time on every page; the
		// map must collapse them to one entry, not report the path twice.
		{DocPath: "/proj/doc.pdf", PageNum: 1, Keywords: []string{"k"}, Labels: []string{"L"}, ModTime: 2000},
		{DocPath: "/proj/doc.pdf", PageNum: 2, Keywords: []string{"k"}, Labels: []string{"L"}, ModTime: 2000},
		{DocPath: "/other/c.go", PageNum: 1, Keywords: []string{"k"}, Labels: []string{"L"}, ModTime: 3000},
	}
	for _, e := range entries {
		if err := s.Upsert(e); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	got, err := s.ListDocModTimes("/proj")
	if err != nil {
		t.Fatalf("ListDocModTimes: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d paths, want 2: %v", len(got), got)
	}
	if got["/proj/a.go"] != 1000 {
		t.Errorf("a.go mod time = %d, want 1000", got["/proj/a.go"])
	}
	if got["/proj/doc.pdf"] != 2000 {
		t.Errorf("doc.pdf mod time = %d, want 2000", got["/proj/doc.pdf"])
	}
	// A sibling whose name shares the string prefix must not be swept in by the
	// directory-boundary filter.
	if _, exists := got["/other/c.go"]; exists {
		t.Error("/other/c.go must not appear under the /proj prefix")
	}
}

// A row written before the column existed carries 0, which has to survive the
// round trip — that is what makes it read as stale and refresh once.
func TestModTimeRoundTrips(t *testing.T) {
	s := newTestStore(t)

	if err := s.Upsert(&PageEntry{DocPath: "/proj/a.go", PageNum: 1, ModTime: 1234567}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	entries, err := s.GetByDoc("/proj/a.go")
	if err != nil {
		t.Fatalf("GetByDoc: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].ModTime != 1234567 {
		t.Errorf("ModTime = %d, want 1234567", entries[0].ModTime)
	}

	// A row with no time reads back as 0.
	if err := s.Upsert(&PageEntry{DocPath: "/proj/b.go", PageNum: 1}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	entries, err = s.GetByDoc("/proj/b.go")
	if err != nil {
		t.Fatalf("GetByDoc: %v", err)
	}
	if entries[0].ModTime != 0 {
		t.Errorf("ModTime = %d, want 0", entries[0].ModTime)
	}
}

// TestSheetExtensionsPinned keeps the shared list exactly what the whole
// pipeline (indexer dispatch, store queries, internal/sheet's leaf copy)
// agrees on. .xls/.ods belong to no list at all — excelize cannot read
// BIFF/ODF, so they are indexed by neither the sheet path nor the text path.
func TestSheetExtensionsPinned(t *testing.T) {
	want := []string{".xlsx", ".xlsm", ".csv", ".tsv"}
	if len(SheetExtensions) != len(want) {
		t.Fatalf("SheetExtensions = %v, want %v", SheetExtensions, want)
	}
	for i := range want {
		if SheetExtensions[i] != want[i] {
			t.Errorf("SheetExtensions[%d] = %q, want %q", i, SheetExtensions[i], want[i])
		}
	}
	for _, absent := range []string{".xls", ".ods"} {
		for _, ext := range SheetExtensions {
			if ext == absent {
				t.Errorf("%q must stay out of SheetExtensions", absent)
			}
		}
	}
}

// TestListSheetFilesEveryPage pins the pair of listings against each other:
// every page (not just the first) of a document with a sheet extension lands
// in the sheet listing, and none of its pages — including page 1 — leaks into
// the text listing.
func TestListSheetFilesEveryPage(t *testing.T) {
	s := newTestStore(t)

	var entries []*PageEntry
	for i, ext := range SheetExtensions {
		path := fmt.Sprintf("/proj/book%d%s", i, ext)
		entries = append(entries,
			&PageEntry{DocPath: path, PageNum: 1, Keywords: []string{"k"}, Labels: []string{"L"}},
			&PageEntry{DocPath: path, PageNum: 2, Keywords: []string{"k"}, Labels: []string{"L"}},
		)
	}
	for _, e := range entries {
		if err := s.Upsert(e); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	sheets, err := s.ListSheetFiles("/proj")
	if err != nil {
		t.Fatalf("ListSheetFiles: %v", err)
	}
	if len(sheets) != 2*len(SheetExtensions) {
		t.Fatalf("ListSheetFiles returned %d rows, want one per page of every sheet extension (=%d)", len(sheets), 2*len(SheetExtensions))
	}

	text, err := s.ListTextFiles("/proj")
	if err != nil {
		t.Fatalf("ListTextFiles: %v", err)
	}
	for _, e := range text {
		t.Errorf("ListTextFiles leaked sheet page %q (page %d)", e.DocPath, e.PageNum)
	}
}

// TestListTextFilesOnlyFirstPages pins the page_num = 1 filter the sheet
// listing already relies on: a multi-page document kind whose extension
// carries no exclusion clause yet (a kind added after this one) can only ever
// contribute its first page to the text listing, never its later pages —
// otherwise a caller counting rows as files resurrects the last-write-wins
// bug across pages.
func TestListTextFilesOnlyFirstPages(t *testing.T) {
	s := newTestStore(t)

	entries := []*PageEntry{
		{DocPath: "/proj/notes.md", PageNum: 1, Keywords: []string{"notes"}, Labels: []string{"L"}},
		// The known four are excluded by extension wholesale: no page appears.
		{DocPath: "/proj/book.xlsx", PageNum: 1, Keywords: []string{"k"}, Labels: []string{"L"}},
		{DocPath: "/proj/book.xlsx", PageNum: 2, Keywords: []string{"k"}, Labels: []string{"L"}},
		// A hypothetical future multi-page kind with no exclusion clause: page
		// 1 is indistinguishable from a single-entry file and stays listed;
		// page 2 must not leak in.
		{DocPath: "/proj/fut.parquet", PageNum: 1, Keywords: []string{"k"}, Labels: []string{"L"}},
		{DocPath: "/proj/fut.parquet", PageNum: 2, Keywords: []string{"k"}, Labels: []string{"L"}},
	}
	for _, e := range entries {
		if err := s.Upsert(e); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	got, err := s.ListTextFiles("/proj")
	if err != nil {
		t.Fatalf("ListTextFiles: %v", err)
	}
	want := map[string]int{
		"/proj/notes.md":    1,
		"/proj/fut.parquet": 1,
	}
	if len(got) != len(want) {
		t.Fatalf("ListTextFiles returned %d rows, want %d: %v", len(got), len(want), got)
	}
	for _, e := range got {
		if want[e.DocPath] != e.PageNum {
			t.Errorf("ListTextFiles returned %q page %d, only first pages are allowed", e.DocPath, e.PageNum)
		}
	}
}
