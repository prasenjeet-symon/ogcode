//go:build windows

package worker

import "errors"

// freeBytes is not implemented on Windows: the Stats command reports ok=false
// and the master falls back to workers that can report.
func freeBytes(path string) (int64, error) {
	return 0, errors.New("free-space stats not supported on this platform")
}
