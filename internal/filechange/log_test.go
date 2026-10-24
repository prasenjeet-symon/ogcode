package filechange

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// memoryLog is the gate without a store: no sink, no persistence, no restore.
func memoryLog(t *testing.T) *Log {
	t.Helper()
	return NewLog(context.Background(), nil)
}

// record stamps one change as session made it.
func record(l *Log, path string, s session.SessionID) int64 {
	return l.Record(context.Background(), path, Change{Session: s, Agent: "build", Tool: "edit"})
}

func TestRevIsMonotonicAndReadStoresRevision(t *testing.T) {
	ctx := context.Background()
	l := memoryLog(t)
	if rev := record(l, "/a", "s1"); rev != 1 {
		t.Fatalf("first rev = %d, want 1", rev)
	}
	rev := record(l, "/a", "s1")
	if rev != 2 {
		t.Fatalf("second rev = %d, want 2", rev)
	}
	// Record already catches the author up; a fresh session has no bookmark,
	// which Seen reports as -1 rather than as a read at revision 0.
	if got := l.Seen("/a", "s2"); got != -1 {
		t.Fatalf("unread session seen = %d, want -1", got)
	}
	l.Read(ctx, "/a", "s2", rev)
	if got := l.Seen("/a", "s2"); got != rev {
		t.Fatalf("seen = %d, want %d", got, rev)
	}
	if changes, dropped := l.Stale(ctx, "/a", "s2", rev); len(changes) != 0 || dropped != 0 {
		t.Fatalf("fresh bookmark stale: %d changes, %d dropped", len(changes), dropped)
	}
}

func TestStaleReportsOnlyOtherSessionsChanges(t *testing.T) {
	ctx := context.Background()
	l := memoryLog(t)
	l.Read(ctx, "/a", "s1", record(l, "/a", "s1"))
	mine := record(l, "/a", "s1")
	theirs := l.Record(ctx, "/a", Change{Session: "s2", Agent: "build", Tool: "edit", Note: "their change"})

	changes, dropped := l.Stale(ctx, "/a", "s1", mine)
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	if len(changes) != 1 || changes[0].Rev != theirs || changes[0].Note != "their change" {
		t.Fatalf("stale = %+v, want the other session's change at rev %d", changes, theirs)
	}

	// The author of the only change in the window is not stale against itself.
	l.Read(ctx, "/a", "s2", theirs)
	if changes, _ := l.Stale(ctx, "/a", "s2", theirs); len(changes) != 0 {
		t.Fatalf("author saw %d of its own changes", len(changes))
	}
}

func TestStaleIsEmptyWithoutABookmark(t *testing.T) {
	ctx := context.Background()
	l := memoryLog(t)
	record(l, "/a", "s2")
	if changes, dropped := l.Stale(ctx, "/a", "s1", -1); len(changes) != 0 || dropped != 0 {
		t.Fatalf("unread session stale: %d changes, %d dropped", len(changes), dropped)
	}
}

// TestBookmarkAtRevisionZeroStillRefuses pins the case a zero meant to miss: a
// file no tool has mutated sits at revision 0, so the session that read it
// there holds a bookmark of 0 — and the first foreign change to it must still
// refuse. A fresh checkout read by two agents is exactly this shape.
func TestBookmarkAtRevisionZeroStillRefuses(t *testing.T) {
	ctx := context.Background()
	l := memoryLog(t)
	l.Read(ctx, "/a", "s1", l.Rev("/a"))
	theirs := l.Record(ctx, "/a", Change{Session: "s2", Tool: "bash", Note: "sed -i"})

	changes, dropped := l.Stale(ctx, "/a", "s1", l.Seen("/a", "s1"))
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	if len(changes) != 1 || changes[0].Rev != theirs {
		t.Fatalf("stale = %+v, want s2's change at rev %d", changes, theirs)
	}
}

func TestRingKeepsFiftyAndReportsTruncation(t *testing.T) {
	ctx := context.Background()
	l := memoryLog(t)
	l.Read(ctx, "/a", "s1", record(l, "/a", "s1"))
	for i := 0; i < RingPerFile+10; i++ {
		record(l, "/a", "s2")
	}
	changes, dropped := l.Stale(ctx, "/a", "s1", 1)
	if len(changes) > RingPerFile {
		t.Fatalf("ring returned %d changes, want at most %d", len(changes), RingPerFile)
	}
	if dropped != 10 {
		t.Fatalf("dropped = %d, want 10 (the changes the ring no longer holds)", dropped)
	}
}

func TestRecordCallsSinkOnceAndWarnsOnError(t *testing.T) {
	ctx := context.Background()
	l := memoryLog(t)
	var got []Change
	l.Sink = func(_ context.Context, c Change) error {
		got = append(got, c)
		return errors.New("disk gone")
	}
	rev := record(l, "/a", "s1")
	if len(got) != 1 {
		t.Fatalf("sink calls = %d, want 1", len(got))
	}
	if got[0].Rev != rev || got[0].Session != "s1" {
		t.Fatalf("sink change = %+v, want rev %d from s1", got[0], rev)
	}
	// The failed write is a warning, not a lost change.
	if changes, _ := l.Stale(ctx, "/a", "s2", -1); len(changes) != 0 {
		t.Fatalf("no bookmark should refuse nothing, got %d", len(changes))
	}
	l.Read(ctx, "/a", "s2", 1)
	if changes, _ := l.Stale(ctx, "/a", "s2", 1); len(changes) != 0 {
		t.Fatalf("author's own change refused it: %d", len(changes))
	}
}

func TestPersistFiresOncePerRevisionAndRetriesAfterFailure(t *testing.T) {
	ctx := context.Background()
	l := memoryLog(t)
	var calls []int64
	fail := true
	l.Persist = func(_ context.Context, b Bookmark) error {
		calls = append(calls, b.Rev)
		if fail {
			return errors.New("db busy")
		}
		return nil
	}

	l.Read(ctx, "/a", "s1", 3)
	if len(calls) != 1 {
		t.Fatalf("persist calls = %v, want one", calls)
	}
	// The failed write left saved untouched, so the same revision tries again.
	l.Read(ctx, "/a", "s1", 3)
	if len(calls) != 2 {
		t.Fatalf("persist calls = %v, want a retry after the failure", calls)
	}
	fail = false
	l.Read(ctx, "/a", "s1", 3)
	l.Read(ctx, "/a", "s1", 3)
	if len(calls) != 3 {
		t.Fatalf("persist calls = %v, want one more once the write succeeded", calls)
	}
}

// TestRestartKeepsBookmarkAndRefusal is the requirement: a restart re-reads the
// history, so a stale session still refuses after the process is thrown away.
func TestRestartKeepsBookmarkAndRefusal(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	store := Open(database, t.TempDir())

	first := NewLog(ctx, store)
	first.Read(ctx, "/a", "s1", record(first, "/a", "s1"))
	first.Read(ctx, "/a", "s1", record(first, "/a", "s1"))
	first.Read(ctx, "/a", "s1", record(first, "/a", "s1"))
	other := first.Record(ctx, "/a", Change{Session: "s2", Tool: "edit", Note: "moved the helper"})
	if other != 4 {
		t.Fatalf("other rev = %d, want 4", other)
	}

	second := NewLog(ctx, store)
	if got := second.Seen("/a", "s1"); got != 3 {
		t.Fatalf("restored seen = %d, want 3", got)
	}
	if got := second.Rev("/a"); got != 4 {
		t.Fatalf("restored rev = %d, want 4", got)
	}
	changes, dropped := second.Stale(ctx, "/a", "s1", second.Seen("/a", "s1"))
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	if len(changes) != 1 || changes[0].Note != "moved the helper" || changes[0].Session != "s2" {
		t.Fatalf("stale after restart = %+v, want s2's change", changes)
	}
	// And the next revision continues from the table rather than restarting at 1.
	if rev := record(second, "/a", "s1"); rev != 5 {
		t.Fatalf("next rev = %d, want 5", rev)
	}
}

// TestHistoryCoversWhatTheRingLost checks the fallback a restart leaves behind:
// the ring is empty, the table is not.
func TestHistoryCoversWhatTheRingLost(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	store := Open(database, t.TempDir())

	first := NewLog(ctx, store)
	first.Read(ctx, "/a", "s1", record(first, "/a", "s1"))
	first.Record(ctx, "/a", Change{Session: "s2", Tool: "edit", Note: "first"})
	first.Record(ctx, "/a", Change{Session: "s2", Tool: "edit", Note: "second"})

	second := NewLog(ctx, store)
	changes, dropped := second.Stale(ctx, "/a", "s1", 1)
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	if len(changes) != 2 {
		t.Fatalf("changes = %d, want the two rows from the table", len(changes))
	}
	if !strings.Contains(changes[1].Note, "second") {
		t.Fatalf("changes out of order: %+v", changes)
	}
}

// TestRestartRefusesABookmarkOfRevisionZero pins the case an empty ring used to
// swallow: a file seeded outside the tools sits at revision 0, so a session that
// read it before the restart holds a bookmark of 0 and the restored ring covers
// nothing. The table still holds the row, so the read must be refused rather
// than counted as dropped.
func TestRestartRefusesABookmarkOfRevisionZero(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	store := Open(database, t.TempDir())

	first := NewLog(ctx, store)
	first.Read(ctx, "/a", "s1", 0)
	first.Record(ctx, "/a", Change{Session: "s2", Tool: "bash", Note: "printf 'two' > a"})

	second := NewLog(ctx, store)
	if got := second.Seen("/a", "s1"); got != 0 {
		t.Fatalf("restored seen = %d, want 0", got)
	}
	changes, dropped := second.Stale(ctx, "/a", "s1", 0)
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	if len(changes) != 1 || changes[0].Session != "s2" || !strings.Contains(changes[0].Note, "printf") {
		t.Fatalf("stale after restart = %+v, want s2's shell change", changes)
	}
}
