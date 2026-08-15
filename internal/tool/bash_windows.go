//go:build windows

package tool

import "os/exec"

// setProcessGroup is a no-op on Windows. The cross-platform cmd.WaitDelay set by
// the caller still bounds how long cmd.Run() can block after cancellation — the
// critical hang fix — and process-group kill semantics differ on Windows and are
// not needed to break the deadlock.
func setProcessGroup(cmd *exec.Cmd) {}
