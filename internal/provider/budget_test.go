package provider

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseMaxConcurrent(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
	}{
		{"empty keeps the default", "", defaultMaxConcurrentRequests},
		{"whitespace keeps the default", "  ", defaultMaxConcurrentRequests},
		{"non-numeric keeps the default", "abc", defaultMaxConcurrentRequests},
		{"zero keeps the default", "0", defaultMaxConcurrentRequests},
		{"negative keeps the default", "-3", defaultMaxConcurrentRequests},
		{"one keeps the default", "1", defaultMaxConcurrentRequests},
		{"an operator value is honoured", "4", 4},
		{"surrounding space is trimmed", " 6 ", 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseMaxConcurrent(tc.raw); got != tc.want {
				t.Fatalf("parseMaxConcurrent(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}

func TestNewRequestBudgetClamps(t *testing.T) {
	cases := []struct {
		name         string
		capacity     int
		reserve      int
		wantTotal    int
		wantIndexMax int
	}{
		{"capacity below two is raised", 1, 0, 2, 1},
		{"zero reserve leaves the whole pool to the index", 8, 0, 8, 7},
		{"a reserve smaller than capacity is honoured", 8, 2, 8, 6},
		{"a reserve that would starve the index keeps it one", 4, 4, 4, 1},
		{"a huge reserve still leaves the index one", 4, 99, 4, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newRequestBudget(tc.capacity, tc.reserve)
			if got := cap(b.total); got != tc.wantTotal {
				t.Fatalf("total capacity = %d, want %d", got, tc.wantTotal)
			}
			if got := cap(b.indexGate); got != tc.wantIndexMax {
				t.Fatalf("index capacity = %d, want %d", got, tc.wantIndexMax)
			}
		})
	}
}

// TestIndexCannotConsumeTheInteractiveReserve pins the guarantee the budget
// exists to make: however many index requests arrive, at least one slot in the
// total pool stays free for an interactive turn.
func TestIndexCannotConsumeTheInteractiveReserve(t *testing.T) {
	const capacity, reserve = 4, 2
	b := newRequestBudget(capacity, reserve)

	var releases []func()
	for i := 0; i < capacity; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		release, err := b.acquireIndex(ctx)
		cancel()
		if err != nil {
			break
		}
		releases = append(releases, release)
	}
	defer func() {
		for _, r := range releases {
			r()
		}
	}()

	// The index took only its share (capacity - reserve); the next attempt has
	// to wait, which the short timeout surfaces as an error rather than a slot.
	if len(releases) != capacity-reserve {
		t.Fatalf("index acquired %d slots, want %d", len(releases), capacity-reserve)
	}

	// An interactive request still proceeds immediately, on the reserve.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := b.acquireInteractive(ctx)
	if err != nil {
		t.Fatalf("interactive acquire failed while the index held its share: %v", err)
	}
	release()
}

// TestAcquireIndexBlocksUntilReleased pins that the gate's ceiling is a real
// wait, not a silent over-admission.
func TestAcquireIndexBlocksUntilReleased(t *testing.T) {
	b := newRequestBudget(3, 1) // indexMax = 2

	first, err := b.acquireIndex(context.Background())
	if err != nil {
		t.Fatalf("first index acquire: %v", err)
	}
	second, err := b.acquireIndex(context.Background())
	if err != nil {
		t.Fatalf("second index acquire: %v", err)
	}

	blocked := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		release, err := b.acquireIndex(ctx)
		if err == nil {
			release()
		}
		blocked <- err
	}()

	select {
	case err := <-blocked:
		t.Fatalf("third index acquire returned early (err=%v); the gate is not bounding", err)
	case <-time.After(75 * time.Millisecond):
		// Still waiting, as it should be.
	}

	first()
	select {
	case err := <-blocked:
		if err != nil {
			t.Fatalf("third index acquire after release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("third index acquire did not proceed after a slot was released")
	}
	second()
}

func TestAcquireIndexHonoursContextCancellation(t *testing.T) {
	b := newRequestBudget(2, 0) // indexMax = 1

	held, err := b.acquireIndex(context.Background())
	if err != nil {
		t.Fatalf("first index acquire: %v", err)
	}
	defer held()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.acquireIndex(ctx); err == nil {
		t.Fatal("acquireIndex on a cancelled context returned a slot, want an error")
	}
}

func TestAsIndexSessionRoundTrips(t *testing.T) {
	if isIndexSession(context.Background()) {
		t.Fatal("an unmarked context reads as an index session")
	}
	if !isIndexSession(AsIndexSession(context.Background())) {
		t.Fatal("AsIndexSession did not mark the context")
	}
	// The mark is inherited by a context derived from it, which is how it
	// reaches the provider through the agent loop.
	marked := AsIndexSession(context.Background())
	child, cancel := context.WithCancel(marked)
	defer cancel()
	if !isIndexSession(child) {
		t.Fatal("a context derived from an index session lost the mark")
	}
}

func TestBudgetBodyReleasesOnceOnDoubleClose(t *testing.T) {
	var count int
	var mu sync.Mutex
	body := &budgetBody{
		ReadCloser: io.NopCloser(strings.NewReader("stream")),
		release: func() {
			mu.Lock()
			count++
			mu.Unlock()
		},
	}
	if err := body.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := body.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if count != 1 {
		t.Fatalf("release ran %d times, want 1", count)
	}
}
