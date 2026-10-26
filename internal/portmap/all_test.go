package portmap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAllReturnsEveryRegistryEntryAndACopy(t *testing.T) {
	reg := filepath.Join(t.TempDir(), "ports.json")
	a, b := t.TempDir(), t.TempDir()
	if err := saveIn(reg, a, 9601); err != nil {
		t.Fatalf("save a: %v", err)
	}
	if err := saveIn(reg, b, 9602); err != nil {
		t.Fatalf("save b: %v", err)
	}

	all := AllIn(reg)
	if len(all) != 2 {
		t.Fatalf("AllIn = %d entries, want 2", len(all))
	}
	if all[key(a)] != 9601 || all[key(b)] != 9602 {
		t.Fatalf("AllIn = %v, want the saved ports", all)
	}

	// AllIn is a copy: mutating one read must not reach the next read.
	all[key(a)] = 1
	if again := AllIn(reg); again[key(a)] != 9601 {
		t.Fatalf("mutation leaked into the registry: %d, want 9601", again[key(a)])
	}
}

func TestAllInReadsACorruptFileAsEmptyNotAnError(t *testing.T) {
	reg := filepath.Join(t.TempDir(), "ports.json")
	if err := os.WriteFile(reg, []byte("not json at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if all := AllIn(reg); len(all) != 0 {
		t.Fatalf("AllIn on a corrupt file = %v, want empty", all)
	}
}
