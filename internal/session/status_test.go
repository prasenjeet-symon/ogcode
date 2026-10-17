package session

import "testing"

// TestSessionStatusRoundTrips pins that the turn-status marker is persisted and
// read back on both the single-row and the list path — the session list draws
// its dot from these rows on a fresh load, so a marker that only lived in the
// running tab's memory would show the wrong thing in every other tab.
func TestSessionStatusRoundTrips(t *testing.T) {
	store, id := utilityTestStore(t)

	if err := store.SetStatus(id, StatusRunning); err != nil {
		t.Fatalf("set running: %v", err)
	}
	got, err := store.Get(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != StatusRunning {
		t.Errorf("Status = %q, want %q", got.Status, StatusRunning)
	}

	if err := store.SetStatus(id, StatusFailed); err != nil {
		t.Fatalf("set failed: %v", err)
	}
	list, err := store.List("/p")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Status != StatusFailed {
		t.Fatalf("list = %+v, want one row with status %q", list, StatusFailed)
	}
}

// TestResetRunningStatusesClearsOnlyRunning pins the startup sweep: a marker
// left behind by a process that died mid-turn must not outlive it, but a failed
// row is a real outcome the user should still see.
func TestResetRunningStatusesClearsOnlyRunning(t *testing.T) {
	store, running := utilityTestStore(t)
	failed := NewSessionID()
	if err := store.Create(&Session{
		ID: failed, ProjectID: "/p", Directory: "/p", Title: "f",
		SessionType: "build", CreatedAt: Now(), UpdatedAt: Now(),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.SetStatus(running, StatusRunning); err != nil {
		t.Fatalf("set running: %v", err)
	}
	if err := store.SetStatus(failed, StatusFailed); err != nil {
		t.Fatalf("set failed: %v", err)
	}

	if err := store.ResetRunningStatuses(); err != nil {
		t.Fatalf("reset: %v", err)
	}

	if got, _ := store.Get(running); got.Status != "" {
		t.Errorf("running status = %q, want cleared", got.Status)
	}
	if got, _ := store.Get(failed); got.Status != StatusFailed {
		t.Errorf("failed status = %q, want %q", got.Status, StatusFailed)
	}
}
