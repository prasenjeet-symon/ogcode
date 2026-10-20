//go:build windows

package server

import (
	"os"

	"golang.org/x/sys/windows"
)

// tryLockFile takes a non-blocking exclusive LockFileEx on the first byte. It
// fails when another process already holds the lock.
func tryLockFile(f *os.File) error {
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &windows.Overlapped{},
	)
}

func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}
