//go:build darwin

package tool

import "syscall"

// sessionOf returns the session a process belongs to.
func sessionOf(pid int) (int, error) { return syscall.Getsid(pid) }
