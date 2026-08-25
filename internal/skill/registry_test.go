package skill

import "testing"

// Sources are registered least-specific first, so a project skill shadows a
// global or built-in one of the same name instead of colliding with it.
func TestRegistry_LaterRegistrationWins(t *testing.T) {
	reg := NewRegistry(nil)
	reg.Register(Skill{Name: "release", Description: "built-in", Source: SourceEmbedded})
	reg.Register(Skill{Name: "release", Description: "the project's own", Source: SourceProject})

	got, ok := reg.Get("release")
	if !ok {
		t.Fatal("release not registered")
	}
	if got.Source != SourceProject || got.Description != "the project's own" {
		t.Errorf("got %+v, want the project's skill to win", got)
	}
	if reg.Len() != 1 {
		t.Errorf("Len = %d; the same name must not register twice", reg.Len())
	}
}

// Listing a denied skill would advertise a call that is refused the moment it
// is made.
func TestRegistry_VisibleWithholdsDeniedSkills(t *testing.T) {
	reg := NewRegistry(Rules{"internal-*": "deny", "deploy-prod": "ask"})
	for _, name := range []string{"internal-docs", "deploy-prod", "git-release"} {
		reg.Register(Skill{Name: name})
	}

	var names []string
	for _, s := range reg.Visible() {
		names = append(names, s.Name)
	}
	// Sorted, denied dropped, ask kept — an ask skill is listed because the user
	// is prompted at call time, not hidden.
	want := []string{"deploy-prod", "git-release"}
	if len(names) != len(want) {
		t.Fatalf("Visible() = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("Visible() = %v, want %v", names, want)
		}
	}
	if reg.Len() != 3 {
		t.Errorf("Len = %d; denied skills stay registered so a call naming one can be refused by name", reg.Len())
	}
}
