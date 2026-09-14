//go:build !darwin

package keepawake

// No power-assertion API on non-macOS platforms; keep-awake is a no-op there.
func platformHold(reason string) (uintptr, bool) { return 0, false }

func platformRelease(handle uintptr) {}
