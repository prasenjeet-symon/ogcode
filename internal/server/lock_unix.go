//go:build !windows

package server

import (
	"os"
	"syscall"
)

// tryLockFile takes a non-blocking exclusive flock. It fails when another
// process (or another descriptor in this one) already holds the lock.
func tryLockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
