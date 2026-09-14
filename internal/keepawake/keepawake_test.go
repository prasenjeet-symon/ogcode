package keepawake

import (
	"os"
	"sync"
	"testing"
)

// reset clears package state so each test is independent of prior Acquire calls.
// It also releases any lingering OS assertion on darwin so a leaked ref in one
// test cannot keep the machine awake past the run.
func reset() {
	mu.Lock()
	if held {
		platformRelease(handle)
	}
	refs, held, handle = 0, false, 0
	mu.Unlock()
}

func TestAcquireIsReferenceCounted(t *testing.T) {
	os.Unsetenv("OGCODE_NO_KEEP_AWAKE")
	reset()
	t.Cleanup(reset)

	r1 := Acquire("turn 1")
	if refs != 1 {
		t.Fatalf("refs = %d, want 1 after first Acquire", refs)
	}
	r2 := Acquire("turn 2")
	if refs != 2 {
		t.Fatalf("refs = %d, want 2 after second Acquire (concurrent turns share one assertion)", refs)
	}

	r1()
	if refs != 1 {
		t.Fatalf("refs = %d, want 1 after releasing one of two", refs)
	}
	// The assertion must still be held while a turn is outstanding. On non-darwin
	// held is always false (no-op), so only assert this where holds can succeed.
	if platformCanHold() && !held {
		t.Error("assertion released while a turn is still active")
	}

	// A duplicate release of the same handle must not double-decrement.
	r1()
	if refs != 1 {
		t.Fatalf("refs = %d, want 1 after a duplicate release", refs)
	}

	r2()
	if refs != 0 {
		t.Fatalf("refs = %d, want 0 after releasing all", refs)
	}
	if held {
		t.Error("assertion still held after the last release")
	}
}

func TestAcquireDisabledIsNoOp(t *testing.T) {
	os.Setenv("OGCODE_NO_KEEP_AWAKE", "1")
	t.Cleanup(func() { os.Unsetenv("OGCODE_NO_KEEP_AWAKE") })
	reset()
	t.Cleanup(reset)

	release := Acquire("turn")
	if refs != 0 || held {
		t.Fatalf("refs=%d held=%v, want 0/false when disabled via env", refs, held)
	}
	release() // must be safe and change nothing
	if refs != 0 {
		t.Fatalf("refs = %d, want 0", refs)
	}
}

func TestConcurrentAcquireReleaseDoesNotLeak(t *testing.T) {
	os.Unsetenv("OGCODE_NO_KEEP_AWAKE")
	reset()
	t.Cleanup(reset)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release := Acquire("concurrent")
			release()
		}()
	}
	wg.Wait()

	if refs != 0 {
		t.Fatalf("refs = %d, want 0 after balanced concurrent acquire/release", refs)
	}
	if held {
		t.Error("assertion still held after all turns released")
	}
}

// platformCanHold reports whether platformHold can actually take an assertion
// here, so the shared test can assert on `held` only where that is meaningful.
func platformCanHold() bool {
	h, ok := platformHold("probe")
	if ok {
		platformRelease(h)
	}
	return ok
}
