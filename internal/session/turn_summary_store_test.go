package session

import (
	"fmt"
	"sync"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

func turnSummaryTestStore(t *testing.T) (*Store, SessionID) {
	t.Helper()
	database, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store := NewStore(database)
	sess := &Session{ID: NewSessionID(), ProjectID: "proj_1", Directory: "/p", Title: "T", CreatedAt: Now(), UpdatedAt: Now()}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return store, sess.ID
}

func TestInsertTurnSummaryRoundTrip(t *testing.T) {
	store, id := turnSummaryTestStore(t)

	ts, err := store.InsertTurnSummary(id, "Decided to use timeout + B-fallback.", "should we block on summary?")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if ts.TurnIndex != 1 {
		t.Errorf("turn_index = %d, want 1", ts.TurnIndex)
	}
	if ts.Summary == "" || ts.UserText == "" {
		t.Error("summary and user text must be stored")
	}

	recent, err := store.RecentTurnSummaries(id, RecentTurnSummariesWindow)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(recent) != 1 || recent[0].ID != ts.ID || recent[0].TurnIndex != 1 {
		t.Fatalf("recent = %+v, want the inserted row", recent)
	}
}

func TestRecentTurnSummariesWindowAndOrder(t *testing.T) {
	store, id := turnSummaryTestStore(t)
	for i := 1; i <= 5; i++ {
		if _, err := store.InsertTurnSummary(id, string(rune('a'+i)), "u"); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	recent, err := store.RecentTurnSummaries(id, RecentTurnSummariesWindow)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("len(recent) = %d, want 2", len(recent))
	}
	// Oldest first within the window: turns 4, 5.
	for i, want := range []int{4, 5} {
		if recent[i].TurnIndex != want {
			t.Errorf("recent[%d].TurnIndex = %d, want %d", i, recent[i].TurnIndex, want)
		}
	}
	if all, err := store.RecentTurnSummaries(id, 100); err != nil || len(all) != 5 {
		t.Errorf("recent len = %d, %v; want 5, nil", len(all), err)
	}
}

func TestTurnSummarySurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/test.db"
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	store := NewStore(database)
	sess := &Session{ID: NewSessionID(), ProjectID: "proj_1", Directory: "/p", Title: "T", CreatedAt: Now(), UpdatedAt: Now()}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := store.InsertTurnSummary(sess.ID, "Persisted across restart.", "u"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	database2, err := db.Open(path)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer func() { _ = database2.Close() }()
	recent, err := NewStore(database2).RecentTurnSummaries(sess.ID, RecentTurnSummariesWindow)
	if err != nil {
		t.Fatalf("recent after reopen: %v", err)
	}
	if len(recent) != 1 || recent[0].Summary != "Persisted across restart." {
		t.Fatalf("after reopen got %+v", recent)
	}
}

func TestTurnSummaryEmptyAndUnknown(t *testing.T) {
	store, id := turnSummaryTestStore(t)
	if _, err := store.InsertTurnSummary(id, "", "u"); err == nil {
		t.Error("empty summary must error")
	}
	recent, err := store.RecentTurnSummaries(NewSessionID(), RecentTurnSummariesWindow)
	if err != nil || len(recent) != 0 {
		t.Errorf("unknown session recent = %v, %v; want empty, nil", recent, err)
	}
	var nilStore *Store
	if _, err := nilStore.InsertTurnSummary(id, "x", "u"); err == nil {
		t.Error("nil store must error")
	}
	if r, err := nilStore.RecentTurnSummaries(id, 3); err != nil || len(r) != 0 {
		t.Errorf("nil store recent = %v, %v; want empty, nil", r, err)
	}
}

// TestInsertTurnSummaryConcurrentDistinctIndices pins the atomic assignment:
// concurrent inserts on one session must land distinct turn indices. The
// UNIQUE(session_id, turn_index) index rejects a duplicate, and MAX+1 is
// computed inside the INSERT statement, so two goroutines cannot read the
// same max and collide.
func TestInsertTurnSummaryConcurrentDistinctIndices(t *testing.T) {
	store, id := turnSummaryTestStore(t)
	const n = 20
	var wg sync.WaitGroup
	idxCh := make(chan int, n)
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ts, err := store.InsertTurnSummary(id, fmt.Sprintf("summary %d", i), "u")
			if err != nil {
				errCh <- err
				return
			}
			idxCh <- ts.TurnIndex
		}(i)
	}
	wg.Wait()
	close(idxCh)
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent insert: %v", err)
	}
	seen := map[int]bool{}
	for idx := range idxCh {
		if seen[idx] {
			t.Errorf("duplicate turn_index %d", idx)
		}
		seen[idx] = true
	}
	if len(seen) != n {
		t.Errorf("got %d distinct indices, want %d", len(seen), n)
	}
}
