//go:build windows

package search

import (
	"os/exec"
	"syscall"
)

// createNoWindow is the Windows CREATE_NO_WINDOW process flag. Without it each
// headless launch flashes a console window on the user's desktop.
const createNoWindow = 0x08000000

func configureChromiumCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
}

// NewBrowserBackend names the browser-driven backend for this platform — here,
// Chrome, Brave, or Edge driven through Chromium's headless CLI. BuildBackend
// calls this, so the platform question is asked in one place rather than behind
// a build tag at every call site.
func NewBrowserBackend() Backend { return NewChromiumBackend() }
