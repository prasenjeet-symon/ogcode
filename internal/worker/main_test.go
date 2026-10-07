package worker

import (
	"fmt"
	"os"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/server"
)

// TestMain runs the package against a throwaway home directory and keeps it off
// the production PostHog project.
//
// A hosted worktree server opens ~/.ogcode/config.db and runs the working
// tree's migrations on it, and the worker keeps its id and credential beside
// it, so a test that forgets isolateHome must still never reach the developer's
// real home. isolateHome still gives a test a fresh home of its own.
//
// With the key blank, the worktree servers the tests spawn build no capture
// client and start no feature-flag refresher.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "ogcode-worker-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create test home:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(home)
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home) // windows

	server.PostHogAPIKey = ""
	m.Run()
}
