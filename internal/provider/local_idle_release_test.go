package provider

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// loaded reports whether the inference pipeline is currently built.
func (e *LocalEmbedder) loaded() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pipeline != nil
}

func waitUntilUnloaded(t *testing.T, e *LocalEmbedder, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if !e.loaded() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the idle model was never released")
}

// The loaded model is ~300 MB of live Go heap held for the life of the process.
// Releasing it after a quiet stretch is the whole point; rebuilding on the next
// call is the price.
func TestEmbedderReleasesIdleModelAndReloads(t *testing.T) {
	e := NewLocalEmbedder("")
	e.idleTimeout = 100 * time.Millisecond
	ctx := context.Background()

	first, err := e.Embed(ctx, []string{"a short fact about authentication"})
	if err != nil {
		t.Fatalf("first embed: %v", err)
	}
	if !e.loaded() {
		t.Fatal("pipeline is not loaded after an embed")
	}

	waitUntilUnloaded(t, e, 5*time.Second)

	e.mu.Lock()
	sessionCleared := e.session == nil
	e.mu.Unlock()
	if !sessionCleared {
		t.Error("the session outlived the released pipeline")
	}

	// The release must not be terminal: the next call rebuilds and still works.
	second, err := e.Embed(ctx, []string{"a short fact about authentication"})
	if err != nil {
		t.Fatalf("embed after release: %v", err)
	}
	if !e.loaded() {
		t.Fatal("pipeline was not rebuilt by the embed after release")
	}
	if len(first) != 1 || len(second) != 1 || len(first[0]) != len(second[0]) {
		t.Fatalf("vector shape changed across a reload: %d then %d dims", len(first[0]), len(second[0]))
	}
	for i := range first[0] {
		if first[0][i] != second[0][i] {
			t.Errorf("the reloaded model embedded the same text differently at dim %d: %v vs %v",
				i, first[0][i], second[0][i])
			break
		}
	}
}

// A model in active use must not be pulled out from under the next call.
func TestEmbedderKeepsModelWhileInUse(t *testing.T) {
	e := NewLocalEmbedder("")
	e.idleTimeout = 300 * time.Millisecond
	ctx := context.Background()

	for i := 0; i < 4; i++ {
		if _, err := e.Embed(ctx, []string{"still working"}); err != nil {
			t.Fatalf("embed %d: %v", i, err)
		}
		if !e.loaded() {
			t.Fatalf("model was released while still in use (after embed %d)", i)
		}
		time.Sleep(100 * time.Millisecond) // well inside the idle window
	}
}

func TestEmbedderCloseIsIdempotent(t *testing.T) {
	e := NewLocalEmbedder("")
	if _, err := e.Embed(context.Background(), []string{"fact"}); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if e.loaded() {
		t.Error("pipeline survived Close")
	}
	if err := e.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}


// liveHeapMB is the heap actually retained, after collecting what is not.
func liveHeapMB() float64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return float64(ms.HeapAlloc) / 1048576
}

// The reason the release exists: the weights are the largest thing ogcode holds,
// and letting go of the pipeline has to actually give that heap back. A release
// that only dropped a handle would be pure cost — a reload price for no memory.
func TestEmbedderReleaseReclaimsTheModelHeap(t *testing.T) {
	e := NewLocalEmbedder("")
	e.idleTimeout = 100 * time.Millisecond

	if _, err := e.Embed(context.Background(), []string{"a short fact about authentication"}); err != nil {
		t.Fatalf("embed: %v", err)
	}
	loaded := liveHeapMB()

	waitUntilUnloaded(t, e, 5*time.Second)
	released := liveHeapMB()

	t.Logf("live heap: %.1f MB loaded → %.1f MB released (freed %.1f MB)",
		loaded, released, loaded-released)

	// The model is around 300 MB; anything short of a hundred means the release
	// is not reaching the weights.
	if freed := loaded - released; freed < 100 {
		t.Errorf("releasing the idle model freed only %.1f MB (loaded %.1f, released %.1f) — the weights are still retained",
			freed, loaded, released)
	}
}
