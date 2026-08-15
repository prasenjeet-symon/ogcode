//go:build !windows

package tool

import (
	"os/exec"
	"syscall"
)

// setProcessGroup places the command in its own process group and overrides the
// context-cancellation behaviour to SIGKILL the entire group.
//
// exec.CommandContext by default only kills the direct child (the shell), so any
// background/daemon process the command started survives — and, because it
// inherited the stdout pipe, keeps cmd.Run() blocked until WaitDelay force-closes
// the pipe. Killing the whole process group stops those children too, so the
// command doesn't leave orphaned servers running (holding ports) behind it.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
