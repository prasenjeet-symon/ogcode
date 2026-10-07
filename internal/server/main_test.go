package server

import (
	"fmt"
	"os"
	"testing"
)

// realHome is the developer's home directory from before TestMain replaced it.
// A test that needs a credential kept there hands it back with t.Setenv, which
// scopes the real home to that one test.
var realHome string

// TestMain runs the package against a throwaway home directory. Serve opens
// ~/.ogcode/config.db and runs the working tree's migrations on it, and writes
// install-id beside it, so a test that starts a server would otherwise do that
// to the developer's real home. A test that wants a home of its own still calls
// t.Setenv("HOME", …), which restores this one when the test ends.
func TestMain(m *testing.M) {
	realHome, _ = os.UserHomeDir()
	home, err := os.MkdirTemp("", "ogcode-server-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create test home:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(home)
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home) // windows
	m.Run()
}
