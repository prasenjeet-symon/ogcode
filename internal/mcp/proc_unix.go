//go:build !windows

package mcp

import (
	"os"
	"os/exec"
	"syscall"
)

// detachFromTerminal starts a stdio server in a session of its own, so it has
// no controlling terminal: nothing it runs can take over the terminal ogcode was
// started from (and leave the user's Ctrl+C aimed elsewhere), read the user's
// keystrokes, or prompt on it. A session leader also leads its own process
// group, which is what lets signalTree reach the processes a launcher like npx
// or uvx starts beneath it.
func detachFromTerminal(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// signalTree signals the server's whole process group. When the group is gone,
// or the server was not started in one, it falls back to the process alone.
func signalTree(p *os.Process, sig syscall.Signal) {
	if err := syscall.Kill(-p.Pid, sig); err != nil {
		_ = p.Signal(sig)
	}
}

// interruptTree asks the server and its children to stop.
func interruptTree(p *os.Process) { signalTree(p, syscall.SIGINT) }

// killTree stops the server and its children outright.
func killTree(p *os.Process) { signalTree(p, syscall.SIGKILL) }
