package docindex

import (
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