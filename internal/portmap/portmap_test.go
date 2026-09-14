package portmap

import (
	"path/filepath"
	"testing"
)

func TestSaveAndLookupRoundTrip(t *testing.T) {
	reg := filepath.Join(t.TempDir(), "ports.json")
	dir := t.TempDir()

	if _, ok := lookupIn(reg, dir); ok {
		t.Fatal("lookup on an empty registry should miss")
	}
	if err := saveIn(reg, dir, 9601); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok := lookupIn(reg, dir)
	if !ok || got != 9601 {
		t.Fatalf("lookup = (%d,%v), want (9601,true)", got, ok)
	}

	// A later Save replaces the entry (e.g. an explicit --port override).
	if err := saveIn(reg, dir, 9602); err != nil {
		t.Fatalf("save overwrite: %v", err)
	}
	if got, _ := lookupIn(reg, dir); got != 9602 {
		t.Fatalf("after overwrite = %d, want 9602", got)
	}
}

func TestSuggestStartAvoidsOtherProjects(t *testing.T) {
	reg := filepath.Join(t.TempDir(), "ports.json")
	a, b, c := t.TempDir(), t.TempDir(), t.TempDir()
	_ = saveIn(reg, a, 9595)
	_ = saveIn(reg, b, 9596)

	// c is new; 9595 and 9596 belong to other projects, so it should land on 9597.
	if got := suggestIn(reg, c, 9595); got != 9597 {
		t.Fatalf("suggest = %d, want 9597 (skips other projects' ports)", got)
	}
	// A project may reuse its OWN recorded port as a base — that is not a clash.
	if got := suggestIn(reg, a, 9595); got != 9595 {
		t.Fatalf("suggest for the owner = %d, want its own base 9595", got)
	}
}

func TestLookupMissingRegistryIsEmptyNotError(t *testing.T) {
	reg := filepath.Join(t.TempDir(), "nope", "ports.json")
	if _, ok := lookupIn(reg, t.TempDir()); ok {
		t.Fatal("a missing registry should miss, not blow up")
	}
}

func TestKeyIsStableAcrossEquivalentSpellings(t *testing.T) {
	d := t.TempDir()
	if key(d) != key(filepath.Join(d, ".")) {
		t.Errorf("key not stable: %q vs %q", key(d), key(filepath.Join(d, ".")))
	}
	if key(d) != key(d+string(filepath.Separator)) {
		t.Errorf("key not stable with trailing separator: %q vs %q", key(d), key(d+string(filepath.Separator)))
	}
}

func TestSaveIgnoresNonPositivePort(t *testing.T) {
	reg := filepath.Join(t.TempDir(), "ports.json")
	dir := t.TempDir()
	if err := saveIn(reg, dir, 0); err != nil {
		t.Fatalf("save(0): %v", err)
	}
	if _, ok := lookupIn(reg, dir); ok {
		t.Error("port 0 should not be recorded")
	}
}
