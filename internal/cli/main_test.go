package cli

import (
	"fmt"
	"os"
	"testing"
)

// TestMain runs the package against a throwaway home directory. run, index and
// serve open ~/.ogcode/config.db and run the working tree's migrations on it,
// and write their logs and the install id under the home directory too, so a
// test that drives one must never reach the developer's real home. A test that
// wants a home of its own still calls t.Setenv("HOME", …).
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "ogcode-cli-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create test home:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(home)
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home) // windows
	m.Run()
}
