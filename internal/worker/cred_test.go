package worker

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPersistLoadCred_Roundtrip(t *testing.T) {
	isolateHome(t)

	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	token := "tok-abc"
	if err := persistCred(token, exp); err != nil {
		t.Fatalf("persistCred: %v", err)
	}

	got, gotExp, ok := loadCred()
	if !ok {
		t.Fatal("loadCred returned ok=false after a successful persist")
	}
	if got != token {
		t.Fatalf("token = %q, want %q", got, token)
	}
	if !gotExp.Equal(exp) {
		t.Fatalf("expiry = %v, want %v", gotExp, exp)
	}
}

func TestLoadCred_NoFile(t *testing.T) {
	isolateHome(t)

	if _, _, ok := loadCred(); ok {
		t.Fatal("loadCred ok=true with no persisted file")
	}
}

func TestPersistCred_OverwritesPrevious(t *testing.T) {
	isolateHome(t)

	if err := persistCred("old", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("persistCred old: %v", err)
	}
	newExp := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	if err := persistCred("new", newExp); err != nil {
		t.Fatalf("persistCred new: %v", err)
	}

	got, gotExp, ok := loadCred()
	if !ok {
		t.Fatal("loadCred ok=false after overwrite")
	}
	if got != "new" {
		t.Fatalf("token = %q, want %q", got, "new")
	}
	if !gotExp.Equal(newExp) {
		t.Fatalf("expiry = %v, want %v", gotExp, newExp)
	}
}

func TestLoadCred_TruncatedFileRejected(t *testing.T) {
	isolateHome(t)

	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".ogcode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash-mid-write: a truncated credentials file.
	if err := os.WriteFile(filepath.Join(dir, workerCredFile), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, ok := loadCred(); ok {
		t.Fatal("loadCred ok=true with truncated file")
	}
}

func TestLoadCred_GarbageRejected(t *testing.T) {
	isolateHome(t)

	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".ogcode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, workerCredFile), []byte("not-an-int\ntoken\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, ok := loadCred(); ok {
		t.Fatal("loadCred ok=true with garbage expiry")
	}
}

func TestPersistCred_FileIsPrivate(t *testing.T) {
	isolateHome(t)

	if err := persistCred("tok", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("persistCred: %v", err)
	}
	home, _ := os.UserHomeDir()
	info, err := os.Stat(filepath.Join(home, ".ogcode", workerCredFile))
	if err != nil {
		t.Fatalf("stat worker-cred file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("worker-cred file perms = %o, want 600", perm)
	}
}
