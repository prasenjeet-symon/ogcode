package server

import (
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/docindex"
)

// The defaults have to be in force for a project that never opens the panel.
// They are seeded on the run path — every path that reads the exclude list,
// which is what excludePatternsFor is — so a headless run, a per-turn
// auto-index and the panel all read one list rather than the panel alone
// carrying the defaults.
func TestExcludePatternsFor_SeedsTheDefaults(t *testing.T) {
	srv := autoIndexServer(t)

	patterns := srv.excludePatternsFor(srv.dir)
	if len(patterns) != len(docindex.DefaultExcludePatterns()) {
		t.Fatalf("got %d patterns (%v), want the shipped defaults", len(patterns), patterns)
	}
	seen := make(map[string]bool, len(patterns))
	for _, p := range patterns {
		seen[p] = true
	}
	for _, want := range docindex.DefaultExcludePatterns() {
		if !seen[want] {
			t.Errorf("default %q is not in force for a run", want)
		}
	}
}

// A default the user removes stays removed across runs. Every run reseeds, so a
// seed that keyed on how many rows exist would read the shorter list as "never
// seeded" and put the pattern back — turning a removal into a no-op the user
// cannot make stick.
func TestExcludePatternsFor_KeepsARemovedDefaultRemoved(t *testing.T) {
	srv := autoIndexServer(t)
	srv.excludePatternsFor(srv.dir)

	entries, err := srv.docindexStore.ListExcludes(srv.dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var victim *docindex.ExcludeEntry
	for _, e := range entries {
		if e.Pattern == "node_modules" {
			victim = e
		}
	}
	if victim == nil {
		t.Fatal("node_modules was not seeded; this test cannot run")
	}
	if err := srv.docindexStore.DeleteExclude(victim.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// A later run, on the same list.
	for _, p := range srv.excludePatternsFor(srv.dir) {
		if p == "node_modules" {
			t.Error("a default the user removed came back on the next run")
		}
	}
}
