package docindex

import (
	"testing"
)

// The shipped defaults exist, and they are the generated names a project should
// not have to name itself. Pinned as a set rather than a count so the assertion
// says what the list is for: a name belongs only if a project would otherwise
// index it, and these do.
func TestDefaultExcludes_AreTheGeneratedNames(t *testing.T) {
	want := map[string]bool{
		"node_modules": true, "vendor": true, "bower_components": true, "Pods": true,
		".venv": true, "venv": true, ".tox": true, ".nox": true, ".dart_tool": true,
		"dist": true, "build": true, "out": true, "target": true, "obj": true, "_build": true,
		".next": true, ".nuxt": true, ".output": true, ".svelte-kit": true, ".astro": true,
		".gradle": true, ".terraform": true, "DerivedData": true, "htmlcov": true, "coverage": true,
		".mypy_cache": true, ".pytest_cache": true, ".ruff_cache": true, "*.egg-info": true, ".eggs": true,
		".idea": true, ".vscode": true,
		"*.min.js": true, "*.min.css": true, "*.map": true, "*.lock": true, "*.sum": true,
		"package-lock.json": true, "yarn.lock": true,
	}
	got := DefaultExcludePatterns()
	if len(got) != len(want) {
		t.Errorf("shipped %d patterns (%v), want %d", len(got), got, len(want))
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("unexpected default %q", p)
		}
	}
}

// A name whose contents can never be indexed is not a default, because it
// excludes nothing: it only makes the list look thorough. Pinned so the list is
// not padded with entries the allowlist already handles.
func TestDefaultExcludes_OmitPatternsThatCouldNotIndexAnything(t *testing.T) {
	for _, p := range []string{"__pycache__", "*.pyc", "*.log", ".DS_Store", ".env", ".git", ".ogcode"} {
		if IsDefaultExclude(p) {
			t.Errorf("%q is a no-op default: nothing under it was indexable to begin with", p)
		}
	}
}

// The seeded list is the shipped list, marked as such. The mark is what the
// panel shows and what the user acts on, so a row that ships has to carry it.
func TestSeedDefaultExcludes_MarksTheRowsItInserts(t *testing.T) {
	store := newTestStore(t)
	const dir = "/workspace"

	if err := store.SeedDefaultExcludes(dir); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := store.ListExcludes(dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != len(defaultExcludePatterns) {
		t.Fatalf("seeded %d rows, want %d", len(got), len(defaultExcludePatterns))
	}
	for _, e := range got {
		if !e.Default {
			t.Errorf("seeded row %q is not marked as a default", e.Pattern)
		}
	}
}

// Removal is the whole reason the defaults are rows rather than a rule in the
// walk: a project that means to index one clicks it away. It has to stay away —
// a later run must not read the shorter list as "never seeded" and put the
// pattern back, which is exactly what keying the seed on row count would do.
func TestSeedDefaultExcludes_DoesNotRestoreARemovedDefault(t *testing.T) {
	store := newTestStore(t)
	const dir = "/workspace"

	if err := store.SeedDefaultExcludes(dir); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before, err := store.ListExcludes(dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var victim *ExcludeEntry
	for _, e := range before {
		if e.Pattern == "node_modules" {
			victim = e
		}
	}
	if victim == nil {
		t.Fatal("node_modules was not seeded; this test cannot run")
	}
	if err := store.DeleteExclude(victim.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// A later run on the same directory seeds again — and must add nothing back.
	if err := store.SeedDefaultExcludes(dir); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	after, err := store.ListExcludes(dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, e := range after {
		if e.Pattern == "node_modules" {
			t.Error("a default the user deleted came back on the next seed")
		}
	}
	if len(after) != len(before)-1 {
		t.Errorf("re-seeding changed the list: %d rows, want %d", len(after), len(before)-1)
	}
}

// Seeding only adds. A pattern the user wrote survives it untouched, and — the
// case that matters — so does one they wrote that happens to match a default:
// they keep the rule they wrote rather than it being silently rewritten into
// ogcode's.
func TestSeedDefaultExcludes_LeavesAUserRuleAlone(t *testing.T) {
	store := newTestStore(t)
	const dir = "/workspace"

	mine, err := store.AddExclude(dir, "node_modules")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := store.SeedDefaultExcludes(dir); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := store.ListExcludes(dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, e := range got {
		if e.Pattern != "node_modules" {
			continue
		}
		if e.ID != mine.ID {
			t.Error("seeding replaced the user's own rule with its own copy")
		}
	}
	if len(got) != len(defaultExcludePatterns) {
		t.Errorf("got %d rows, want %d — the shared pattern counted once", len(got), len(defaultExcludePatterns))
	}
}

// Idempotent: seeding twice is the same as seeding once. Every run path seeds
// before it reads, so this is the common case, not an edge one.
func TestSeedDefaultExcludes_IsIdempotent(t *testing.T) {
	store := newTestStore(t)
	const dir = "/workspace"

	if err := store.SeedDefaultExcludes(dir); err != nil {
		t.Fatalf("seed: %v", err)
	}
	first, err := store.ListExcludes(dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if err := store.SeedDefaultExcludes(dir); err != nil {
		t.Fatalf("seed again: %v", err)
	}
	second, err := store.ListExcludes(dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(first) != len(second) {
		t.Errorf("seeding twice changed the list: %d then %d", len(first), len(second))
	}
}

// Seeding is per directory, because the database is shared across the projects a
// server has open. A default removed in one must not come back because another
// project was seeded.
func TestSeedDefaultExcludes_IsScopedToTheDirectory(t *testing.T) {
	store := newTestStore(t)

	if err := store.SeedDefaultExcludes("/a"); err != nil {
		t.Fatalf("seed /a: %v", err)
	}
	got, err := store.ListExcludes("/b")
	if err != nil {
		t.Fatalf("list /b: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("seeding /a put %d rows against /b", len(got))
	}
}

// A user's own pattern is theirs, and is not labelled as one ogcode ships.
func TestAddExclude_IsNotMarkedAsADefault(t *testing.T) {
	store := newTestStore(t)
	const dir = "/workspace"

	if _, err := store.AddExclude(dir, "*.generated.ts"); err != nil {
		t.Fatalf("add: %v", err)
	}
	got, err := store.ListExcludes(dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want the one the user added", len(got))
	}
	if got[0].Default {
		t.Error("a pattern the user wrote was labelled as a default")
	}
}
