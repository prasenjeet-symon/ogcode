//go:build !windows

package mcp

import (
	"bufio"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Stopping a stdio server must stop what it started too. Launchers like npx and
// uvx run the real server as a child, and an interrupt sent to the launcher
// alone left that child running after ogcode exited. In a session of its own
// the server leads its own process group, and killProcessTree signals the
// whole group.
func TestKillProcessTreeStopsTheServersChildren(t *testing.T) {
	// The shell stands in for a launcher: it starts a child, reports its pid,
	// and waits on it.
	cmd := exec.Command("sh", "-c", "sleep 30 & echo $!; wait")
	detachFromTerminal(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("read child pid: %v", err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("child pid %q: %v", line, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })

	killProcessTree(cmd)

	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := syscall.Kill(child, 0); err == syscall.ESRCH {
			return // gone
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server's child %d outlived killProcessTree", child)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
