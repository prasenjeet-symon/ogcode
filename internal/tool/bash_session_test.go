//go:build darwin || linux

package tool

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
)

// A command the agent runs must not share ogcode's terminal. In a process
// group of its own it still did: an interactive shell it ran (zsh -ic) took the
// terminal over as it started and never handed it back, so the user's Ctrl+C
// went to a group that no longer existed and ogcode could not be stopped from
// its own terminal. In a session of its own it has no controlling terminal to
// take — and it still leads its own process group, which the cancel path kills.
func TestBashCommandRunsInItsOwnSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "sh", "-c", "sleep 30")
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})
	pid := cmd.Process.Pid

	sid, err := sessionOf(pid)
	if err != nil {
		t.Fatalf("session of %d: %v", pid, err)
	}
	if sid != pid {
		t.Errorf("command runs in session %d, want a session of its own (%d)", sid, pid)
	}
	if mine, err := sessionOf(syscall.Getpid()); err == nil && sid == mine {
		t.Errorf("command shares the test process's session %d — and with it the terminal", sid)
	}
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		t.Fatalf("getpgid: %v", err)
	}
	if pgid != pid {
		t.Errorf("command is in process group %d, want its own (%d), so cancel can kill the whole group", pgid, pid)
	}
}
