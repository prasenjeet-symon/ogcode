package worker

import (
	"os"
	"path/filepath"
	"testing"
)

// isolateHome points os.UserHomeDir at a temp dir so a test never reads or
// writes the real ~/.ogcode/worker-id.
func isolateHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
}

func TestLoadOrCreateWorkerID_PersistsAcrossCalls(t *testing.T) {
	isolateHome(t)

	first, err := loadOrCreateWorkerID()
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if first == "" {
		t.Fatal("first call returned empty id")
	}

	// A second call (a fresh process in the real world) must return the SAME id.
	second, err := loadOrCreateWorkerID()
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if second != first {
		t.Fatalf("id not stable across calls: got %q then %q", first, second)
	}

	// The id must be DNS-safe (the master uses it as a subdomain label).
	if !validWorkerIDForTest(second) {
		t.Fatalf("id %q is not DNS-safe", second)
	}
}

func TestLoadOrCreateWorkerID_FileIsPrivate(t *testing.T) {
	isolateHome(t)

	if _, err := loadOrCreateWorkerID(); err != nil {
		t.Fatalf("loadOrCreateWorkerID: %v", err)
	}
	home, _ := os.UserHomeDir()
	info, err := os.Stat(filepath.Join(home, ".ogcode", workerIDFile))
	if err != nil {
		t.Fatalf("stat worker-id file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("worker-id file perms = %o, want 600", perm)
	}
}

func TestLoadOrCreateWorkerID_TruncatedFileIsReplaced(t *testing.T) {
	isolateHome(t)

	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".ogcode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash-mid-write: an empty/truncated id file.
	if err := os.WriteFile(filepath.Join(dir, workerIDFile), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	id, err := loadOrCreateWorkerID()
	if err != nil {
		t.Fatalf("loadOrCreateWorkerID: %v", err)
	}
	if id == "" {
		t.Fatal("empty id after replacing truncated file")
	}
}

// validWorkerIDForTest mirrors the master's RFC 1123 label check so the worker
// test can assert its minted id is acceptable to the master.
func validWorkerIDForTest(id string) bool {
	if len(id) == 0 || len(id) > 63 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-':
			if i == 0 || i == len(id)-1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
