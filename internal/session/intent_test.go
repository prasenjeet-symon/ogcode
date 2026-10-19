package session

import "testing"

// TestSessionIntentRoundTrips pins that the turn's plan is persisted and read
// back on both the single-row and the list path — a tab that was not the one
// running the turn learns the plan from these rows, so a plan that only lived
// in the running tab's memory would be missing everywhere else.
func TestSessionIntentRoundTrips(t *testing.T) {
	store, id := utilityTestStore(t)

	if got, _ := store.Get(id); got.Intent != nil {
		t.Fatalf("a fresh session carries a plan: %+v", got.Intent)
	}

	plan := &SessionIntent{Steps: []IntentStep{
		{Step: "read the spec", Status: IntentDone},
		{Step: "write the tool", Status: IntentActive},
		{Step: "add tests", Status: IntentPending},
	}}
	if err := store.SetIntent(id, plan); err != nil {
		t.Fatalf("set intent: %v", err)
	}

	got, err := store.Get(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Intent == nil || len(got.Intent.Steps) != 3 {
		t.Fatalf("Intent = %+v, want 3 steps", got.Intent)
	}
	if got.Intent.Steps[1].Status != IntentActive {
		t.Errorf("step 1 status = %q, want %q", got.Intent.Steps[1].Status, IntentActive)
	}

	list, err := store.List("/p")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Intent == nil || len(list[0].Intent.Steps) != 3 {
		t.Fatalf("list = %+v, want one row carrying the plan", list)
	}
}

// TestSetIntentReplacesAndClears pins the last-write-wins contract the tool
// relies on: a new plan replaces the old whole, and a nil plan clears the
// column (which is how the next turn starts with no stale plan showing).
func TestSetIntentReplacesAndClears(t *testing.T) {
	store, id := utilityTestStore(t)

	if err := store.SetIntent(id, &SessionIntent{Steps: []IntentStep{
		{Step: "old one", Status: IntentActive},
		{Step: "old two", Status: IntentPending},
	}}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := store.SetIntent(id, &SessionIntent{Steps: []IntentStep{
		{Step: "new one", Status: IntentActive},
	}}); err != nil {
		t.Fatalf("replace: %v", err)
	}

	got, _ := store.Get(id)
	if got.Intent == nil || len(got.Intent.Steps) != 1 || got.Intent.Steps[0].Step != "new one" {
		t.Fatalf("Intent after replace = %+v, want a single \"new one\" step", got.Intent)
	}

	if err := store.SetIntent(id, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got, _ := store.Get(id); got.Intent != nil {
		t.Errorf("Intent = %+v, want nil after clear", got.Intent)
	}
}

func TestParseIntentRejectsUnusableValues(t *testing.T) {
	if got := parseIntent("   "); got != nil {
		t.Errorf("blank -> %+v, want nil", got)
	}
	if got := parseIntent("not json"); got != nil {
		t.Errorf("garbage -> %+v, want nil", got)
	}
	if got := parseIntent(`{"steps":[]}`); got != nil {
		t.Errorf("empty steps -> %+v, want nil", got)
	}
}

func TestValidIntentStatus(t *testing.T) {
	for _, s := range []string{IntentPending, IntentActive, IntentDone, IntentDropped} {
		if !ValidIntentStatus(s) {
			t.Errorf("ValidIntentStatus(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "doing", "DONE", "active "} {
		if ValidIntentStatus(s) {
			t.Errorf("ValidIntentStatus(%q) = true, want false", s)
		}
	}
}
