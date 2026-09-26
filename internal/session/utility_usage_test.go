package session

import (
	"path/filepath"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

func utilityTestStore(t *testing.T) (*Store, SessionID) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	store := NewStore(database)
	id := NewSessionID()
	if err := store.Create(&Session{
		ID: id, ProjectID: "/p", Directory: "/p", Title: "t",
		SessionType: "build", CreatedAt: Now(), UpdatedAt: Now(),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return store, id
}

// TestUtilityUsageAccumulates pins the increment: utility usage is spent by
// several calls (title, risk check, compaction), each adding its own, and the
// session row must hold the running total rather than the last one.
func TestUtilityUsageAccumulates(t *testing.T) {
	store, id := utilityTestStore(t)

	if err := store.AddUtilityUsage(id, TokenCounts{Input: 10, Output: 5, CacheWrite: 2}); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if err := store.AddUtilityUsage(id, TokenCounts{Input: 100, Output: 20, Reasoning: 8}); err != nil {
		t.Fatalf("second add: %v", err)
	}

	got, err := store.Get(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.UtilityTokens == nil {
		t.Fatal("UtilityTokens is nil, want the accumulated counts")
	}
	u := got.UtilityTokens
	if u.Input != 110 || u.Output != 25 || u.Reasoning != 8 || u.CacheWrite != 2 {
		t.Errorf("components = %+v, want input=110 output=25 reasoning=8 cacheWrite=2", u)
	}
	// Total excludes cache read and equals input + cache write + output.
	if want := 110 + 2 + 25; u.Total != want {
		t.Errorf("Total = %d, want %d", u.Total, want)
	}
}

// TestUtilityUsageNilWhileZero pins that an untouched session marshals without
// the field, so the web's optional type does not see a zeroed object it would
// have to special-case.
func TestUtilityUsageNilWhileZero(t *testing.T) {
	store, id := utilityTestStore(t)

	got, err := store.Get(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.UtilityTokens != nil {
		t.Errorf("UtilityTokens = %+v, want nil for a session with no utility usage", got.UtilityTokens)
	}
}

// TestUtilityUsageAddZeroIsNoop pins that adding nothing is a no-op, so a call
// that reported no usage never flips the field on.
func TestUtilityUsageAddZeroIsNoop(t *testing.T) {
	store, id := utilityTestStore(t)

	if err := store.AddUtilityUsage(id, TokenCounts{}); err != nil {
		t.Fatalf("zero add: %v", err)
	}
	got, err := store.Get(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.UtilityTokens != nil {
		t.Errorf("UtilityTokens = %+v, want nil after a zero add", got.UtilityTokens)
	}
}

// TestUtilityUsageSurvivesUpdate pins that a full-row Update — the shape every
// title/model/permission write uses — leaves the utility columns alone. A
// read-modify-write there would clobber an increment that landed between the
// caller's Get and its Update.
func TestUtilityUsageSurvivesUpdate(t *testing.T) {
	store, id := utilityTestStore(t)

	// Load, then accumulate behind the loaded copy's back, then save the stale
	// copy the way a title update would.
	loaded, err := store.Get(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := store.AddUtilityUsage(id, TokenCounts{Input: 7, Output: 3}); err != nil {
		t.Fatalf("add: %v", err)
	}
	loaded.Title = "renamed"
	loaded.UpdatedAt = Now()
	if err := store.Update(loaded); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := store.Get(id)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Title != "renamed" {
		t.Errorf("Title = %q, want renamed", got.Title)
	}
	if got.UtilityTokens == nil || got.UtilityTokens.Input != 7 || got.UtilityTokens.Output != 3 {
		t.Errorf("UtilityTokens = %+v, want input=7 output=3 preserved across Update", got.UtilityTokens)
	}
}
