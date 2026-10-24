package filechange

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return Open(database, t.TempDir())
}

// seedSession inserts a session row so PruneBookmarks has something to keep.
func seedSession(t *testing.T, s *Store, id string) {
	t.Helper()
	if _, err := s.database.ExecContext(context.Background(),
		`INSERT INTO session (id, project_id, directory, time_created, time_updated) VALUES (?, ?, ?, ?, ?)`,
		id, s.dir, s.dir, 1, 1); err != nil {
		t.Fatalf("seed session %s: %v", id, err)
	}
}

func TestAppendListRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	at := time.Unix(1_700_000_000, 0)
	want := Change{
		Path:    "/proj/a.go",
		Rev:     3,
		Session: "sess-1",
		Agent:   "build",
		Tool:    "edit",
		Note:    "split the parser\nsecond line of the note",
		At:      at,
		Diff:    "@@ -1,2 +1,3 @@\n package a\n+var x = 1\n",
	}
	if err := s.Append(ctx, want); err != nil {
		t.Fatalf("append: %v", err)
	}

	got, err := s.List(ctx, Query{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("list: got %d changes, want 1", len(got))
	}
	c := got[0]
	if c.ID == 0 {
		t.Error("id not assigned")
	}
	if c.Path != want.Path || c.Rev != want.Rev || c.Session != want.Session ||
		c.Agent != want.Agent || c.Tool != want.Tool || c.Note != want.Note || c.Diff != want.Diff {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", c, want)
	}
	if !c.At.Equal(at) {
		t.Errorf("at = %v, want %v", c.At, at)
	}
	if c.DiffOmitted {
		t.Error("diff_omitted set for a small diff")
	}
	if c.Directory != s.Dir() {
		t.Errorf("directory = %q, want %q", c.Directory, s.Dir())
	}
}

func TestAppendCapsLongDiff(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	long := strings.Repeat("+ a fairly long changed line\n", 1000)
	if err := s.Append(ctx, Change{Path: "/proj/a.go", Rev: 1, Tool: "write", Diff: long}); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := s.List(ctx, Query{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("list: got %d changes, want 1", len(got))
	}
	if !got[0].DiffOmitted {
		t.Error("diff_omitted not set for an oversized diff")
	}
	if len(got[0].Diff) > DiffMaxBytes+len("\n… (truncated)") {
		t.Errorf("diff = %d bytes, want at most %d", len(got[0].Diff), DiffMaxBytes)
	}
}

func TestListFiltersByPathAndPages(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	for i := 1; i <= 3; i++ {
		if err := s.Append(ctx, Change{Path: "/proj/a.go", Rev: int64(i), Tool: "edit"}); err != nil {
			t.Fatalf("append a: %v", err)
		}
	}
	if err := s.Append(ctx, Change{Path: "/proj/b.go", Rev: 1, Tool: "edit"}); err != nil {
		t.Fatalf("append b: %v", err)
	}

	all, err := s.List(ctx, Query{Path: "/proj/a.go"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("list a: got %d, want 3", len(all))
	}
	if all[0].Rev != 3 {
		t.Errorf("newest first: got rev %d first", all[0].Rev)
	}

	older, err := s.List(ctx, Query{Path: "/proj/a.go", Before: all[0].ID})
	if err != nil {
		t.Fatalf("list before: %v", err)
	}
	if len(older) != 2 || older[0].Rev != 2 {
		t.Errorf("page: got %d changes starting at rev %d, want 2 starting at 2", len(older), older[0].Rev)
	}
}

func TestAfterReturnsOldestFirstAboveRev(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	for i := 1; i <= 4; i++ {
		if err := s.Append(ctx, Change{Path: "/proj/a.go", Rev: int64(i), Tool: "edit", Note: "n"}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	got, err := s.After(ctx, "/proj/a.go", 2, 10)
	if err != nil {
		t.Fatalf("after: %v", err)
	}
	if len(got) != 2 || got[0].Rev != 3 || got[1].Rev != 4 {
		t.Fatalf("after: got %+v, want revs 3 then 4", got)
	}
}

func TestRevisionsReseedsPerPath(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	for _, c := range []Change{
		{Path: "/proj/a.go", Rev: 4},
		{Path: "/proj/a.go", Rev: 7},
		{Path: "/proj/b.go", Rev: 2},
	} {
		if err := s.Append(ctx, Change{Path: c.Path, Rev: c.Rev, Tool: "edit"}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	revs, err := s.Revisions(ctx)
	if err != nil {
		t.Fatalf("revisions: %v", err)
	}
	if revs["/proj/a.go"] != 7 || revs["/proj/b.go"] != 2 {
		t.Fatalf("revisions = %v, want a=7 b=2", revs)
	}
}

func TestSaveBookmarkUpsertsAndKeepsLatest(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if err := s.SaveBookmark(ctx, Bookmark{Path: "/proj/a.go", Session: "sess-1", Rev: 2}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.SaveBookmark(ctx, Bookmark{Path: "/proj/a.go", Session: "sess-1", Rev: 5}); err != nil {
		t.Fatalf("save again: %v", err)
	}
	if err := s.SaveBookmark(ctx, Bookmark{Path: "/proj/a.go", Session: "sess-2", Rev: 3}); err != nil {
		t.Fatalf("save other session: %v", err)
	}

	bookmarks, err := s.Bookmarks(ctx)
	if err != nil {
		t.Fatalf("bookmarks: %v", err)
	}
	if got := bookmarks["/proj/a.go"]["sess-1"]; got != 5 {
		t.Errorf("sess-1 rev = %d, want 5", got)
	}
	if got := bookmarks["/proj/a.go"]["sess-2"]; got != 3 {
		t.Errorf("sess-2 rev = %d, want 3", got)
	}
}

func TestPruneBookmarksKeepsLiveSessions(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	seedSession(t, s, "sess-live")
	for _, b := range []Bookmark{
		{Path: "/proj/a.go", Session: "sess-live", Rev: 1},
		{Path: "/proj/a.go", Session: "sess-gone", Rev: 1},
	} {
		if err := s.SaveBookmark(ctx, b); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	n, err := s.PruneBookmarks(ctx)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned %d rows, want 1", n)
	}
	bookmarks, err := s.Bookmarks(ctx)
	if err != nil {
		t.Fatalf("bookmarks: %v", err)
	}
	if _, ok := bookmarks["/proj/a.go"]["sess-gone"]; ok {
		t.Error("dead session's bookmark survived the sweep")
	}
	if _, ok := bookmarks["/proj/a.go"]["sess-live"]; !ok {
		t.Error("live session's bookmark was swept")
	}
}

func TestStoreIsScopedToOneDirectory(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	one, two := Open(database, "/proj/one"), Open(database, "/proj/two")

	if err := one.Append(ctx, Change{Path: "a.go", Rev: 1, Tool: "edit", Note: "from one"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := two.List(ctx, Query{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("other directory saw %d changes, want 0", len(got))
	}
}
