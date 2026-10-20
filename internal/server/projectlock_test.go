package server

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestProjectLock_OneServerPerDir pins the guard behind the FK-787 report: a
// second server on the same project directory must be refused, and the refusal
// must name the running process and its URL so the user can go there instead.
func TestProjectLock_OneServerPerDir(t *testing.T) {
	dir := t.TempDir()

	l1, err := acquireProjectLock(dir)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	l1.setPort(7777)

	if _, err := os.Stat(filepath.Join(dir, ".ogcode", "server.lock")); err != nil {
		t.Errorf("lock file not written: %v", err)
	}

	_, err = acquireProjectLock(dir)
	if err == nil {
		t.Fatal("second acquire succeeded, so two servers can share one project")
	}
	msg := err.Error()
	if !strings.Contains(msg, strconv.Itoa(os.Getpid())) {
		t.Errorf("error %q does not name the owning pid", msg)
	}
	if !strings.Contains(msg, "http://localhost:7777") {
		t.Errorf("error %q does not point at the running server", msg)
	}

	if err := l1.Close(); err != nil {
		t.Fatalf("release first lock: %v", err)
	}

	l2, err := acquireProjectLock(dir)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	if err := l2.Close(); err != nil {
		t.Fatalf("release second lock: %v", err)
	}
}
