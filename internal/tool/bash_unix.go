//go:build !windows

package tool

import (
	"os/exec"
	"syscall"
)

// setProcessGroup starts the command in a session of its own — which also makes
// it the leader of its own process group — and overrides the
// context-cancellation behaviour to SIGKILL the entire group.
//
// exec.CommandContext by default only kills the direct child (the shell), so any
// background/daemon process the command started survives — and, because it
// inherited the stdout pipe, keeps cmd.Run() blocked until WaitDelay force-closes
// the pipe. Killing the whole process group stops those children too, so the
// command doesn't leave orphaned servers running (holding ports) behind it.
//
// A session rather than just a group, because a group alone still shares the
// terminal ogcode was started from. An interactive shell the agent runs (zsh
// -ic, bash -i) takes that terminal over for its own group as it starts, and
// exits without handing it back — leaving the user's Ctrl+C aimed at a group
// that no longer exists, so ogcode could not be stopped from its own terminal.
// Without a controlling terminal nothing the agent runs can take it, read the
// user's keystrokes, or prompt on it and hang; a command that needs a terminal
// fails at once instead.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// A negative PID targets the whole process group led by the child shell.
		// ESRCH means the group is already gone — treat that as success.
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
			return err
		}
		return nil
	}
}
