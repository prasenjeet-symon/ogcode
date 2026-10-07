package worker

import (
	"fmt"
	"os"
	"testing"
)

// TestMain runs the package against a throwaway home directory. A hosted
// worktree server opens ~/.ogcode/config.db and runs the working tree's
// migrations on it, and the worker keeps its id and credential beside it, so a
// test that forgets isolateHome must still never reach the developer's real
// home. isolateHome still gives a test a fresh home of its own.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "ogcode-worker-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create test home:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(home)
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home) // windows
	m.Run()
}
