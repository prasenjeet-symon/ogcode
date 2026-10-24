package filechange

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFingerprintFollowsTheFile pins what the comparison can see: a rewrite of
// different length moves the fingerprint, and a deletion is a distinct state
// rather than an error.
func TestFingerprintFollowsTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	before := FingerprintOf(path)
	if !before.Exists || before.Size != 4 {
		t.Fatalf("fingerprint = %+v, want a 4-byte file that exists", before)
	}
	if again := FingerprintOf(path); again != before {
		t.Fatalf("an unchanged file's fingerprint moved: %+v then %+v", before, again)
	}

	if err := os.WriteFile(path, []byte("one two\n"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if after := FingerprintOf(path); after == before {
		t.Fatalf("a rewrite kept the fingerprint: %+v", after)
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if gone := FingerprintOf(path); gone.Exists || gone != (Fingerprint{}) {
		t.Fatalf("fingerprint of a removed file = %+v, want the zero value", gone)
	}
}
