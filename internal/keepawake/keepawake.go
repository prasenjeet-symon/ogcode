// Package keepawake holds a macOS power assertion while ogcode is doing work
// that should not be cut short by the display or the system going to sleep —
// specifically, for the duration of an agent turn.
//
// It is reference counted: concurrent turns (the server and a worker, or a
// nested deep_search loop running alongside its parent) share a single OS
// assertion that is released only once the last turn finishes. The assertion
// used is PreventUserIdleDisplaySleep, which keeps the screen on and, because
// the system cannot idle-sleep while the display is up, also prevents idle
// system sleep — matching "keep the Mac and its screen awake while a turn runs".
//
// On non-macOS platforms, and whenever OGCODE_NO_KEEP_AWAKE is set, every call
// is a no-op.
package keepawake

import (
	"log/slog"
	"os"
	"sync"
)

var (
	mu     sync.Mutex
	refs   int
	handle uintptr // opaque platform assertion id; only meaningful while held
	held   bool
)

// disabled reports whether the operator has turned keep-awake off.
func disabled() bool {
	switch os.Getenv("OGCODE_NO_KEEP_AWAKE") {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// Acquire signals that work is in progress that should keep the Mac awake. It
// returns a release function that is safe to call any number of times from any
// goroutine; only the first call counts, so `defer release()` is correct even on
// panic-recovery paths that might run it twice.
//
// While at least one Acquire is outstanding a single power assertion is held,
// released when the last outstanding release runs. Best effort: if the OS
// refuses the assertion the turn proceeds anyway, just without sleep protection.
func Acquire(reason string) (release func()) {
	if disabled() {
		return func() {}
	}

	mu.Lock()
	refs++
	if refs == 1 && !held {
		if h, ok := platformHold(reason); ok {
			handle = h
			held = true
			slog.Debug("keepawake: assertion held", "reason", reason)
		} else {
			// Leave held=false; a later Acquire will try again on the next 0->1.
			slog.Debug("keepawake: could not hold power assertion", "reason", reason)
		}
	}
	mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			mu.Lock()
			refs--
			if refs <= 0 {
				refs = 0
				if held {
					platformRelease(handle)
					held = false
					handle = 0
					slog.Debug("keepawake: assertion released")
				}
			}
			mu.Unlock()
		})
	}
}
