package docindex

import "testing"

// Consent is per project and off until granted. The auto-index guard reads this
// to decide whether a project over the file limit may be indexed without asking.
func TestAutoIndexAllowedRoundTrips(t *testing.T) {
	s := newTestStore(t)

	// A project with no row has not consented.
	allowed, err := s.AutoIndexAllowed("/proj")
	if err != nil {
		t.Fatalf("AutoIndexAllowed: %v", err)
	}
	if allowed {
		t.Error("a project with no consent row must default to disallowed")
	}

	if err := s.SetAutoIndexAllowed("/proj", true); err != nil {
		t.Fatalf("SetAutoIndexAllowed: %v", err)
	}
	if allowed, err = s.AutoIndexAllowed("/proj"); err != nil || !allowed {
		t.Fatalf("consent must persist once granted: allowed=%v err=%v", allowed, err)
	}

	// Consent for one project must not leak to another.
	if other, err := s.AutoIndexAllowed("/other"); err != nil || other {
		t.Fatalf("consent leaked across projects: allowed=%v err=%v", other, err)
	}

	// Revoking overwrites the row in place.
	if err := s.SetAutoIndexAllowed("/proj", false); err != nil {
		t.Fatalf("SetAutoIndexAllowed(false): %v", err)
	}
	if allowed, err := s.AutoIndexAllowed("/proj"); err != nil || allowed {
		t.Fatalf("revoking consent must stick: allowed=%v err=%v", allowed, err)
	}
}

func TestConsentNilSafe(t *testing.T) {
	var s *Store
	if allowed, err := s.AutoIndexAllowed("/proj"); err != nil || allowed {
		t.Errorf("nil store must read as disallowed with no error, got %v, %v", allowed, err)
	}
	if err := s.SetAutoIndexAllowed("/proj", true); err != nil {
		t.Errorf("nil store must be a no-op, got %v", err)
	}
}
