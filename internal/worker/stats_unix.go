//go:build !windows

package worker

import "syscall"

// freeBytes reports the free bytes available on the filesystem holding path
// (used by the Stats command so the master can pick a worker for a new repo
// clone by free space).
func freeBytes(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
