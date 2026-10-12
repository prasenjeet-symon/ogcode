//go:build windows

package mcp

import (
	"os"
	"os/exec"
)

// detachFromTerminal is a no-op on Windows, where a console child cannot take
// over the console's foreground the way a Unix process group can.
func detachFromTerminal(cmd *exec.Cmd) {}

// interruptTree asks the server to stop. os/exec exposes no process-group
// signal on Windows, and Interrupt is unsupported there, so this reaches only
// the server itself, as before.
func interruptTree(p *os.Process) { _ = p.Signal(os.Interrupt) }

// killTree stops the server outright.
func killTree(p *os.Process) { _ = p.Kill() }
