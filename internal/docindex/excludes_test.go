package docindex

import "testing"

// Nothing is excluded on a project's behalf. The seed used to guess at a
// project's noise from directory names — node_modules, vendor, dist — and the
// guess was invisible: it landed in the user's own store, indistinguishable
// from a rule they had written, so a project that tracked one of those names
// found it silently missing from every search with nothing to say why.
//
// .gitignore is where a project has already recorded this, deliberately and
// under review. Pinned so a well-meaning default cannot creep back in.
func TestSeedDefaultExcludes_AddsNothing(t *testing.T) {
	store := newTestStore(t)
	const dir = "/workspace"

	if err := store.SeedDefaultExcludes(dir); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := store.ListExcludes(dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 {
		var patterns []string
		for _, e := range got {
			patterns = append(patterns, e.Pattern)
		}
		t.Errorf("seeding added %d patterns (%v); the project's .gitignore decides, not a built-in guess",
			len(got), patterns)
	}
}

// The excludes mechanism itself stays: a pattern the user adds deliberately is
// their own decision, and unlike a seeded default it is visible and removable.
func TestAddExclude_StillWorksAfterSeedingWasEmptied(t *testing.T) {
	store := newTestStore(t)
	const dir = "/workspace"

	if _, err := store.AddExclude(dir, "*.generated.ts"); err != nil {
		t.Fatalf("add: %v", err)
	}
	got, err := store.ListExcludes(dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Pattern != "*.generated.ts" {
		t.Errorf("got %v, want the one pattern the user added", got)
	}
}
