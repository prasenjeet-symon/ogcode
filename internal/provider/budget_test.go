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
		wantCapacity int
		wantIdle     int
		wantBusy     int
	}{
		{"capacity below two is raised", 1, 0, 2, 1, 1},
		{"zero reserve leaves the index all but one slot", 8, 0, 8, 7, 1},
		{"a reserve smaller than capacity is honoured", 8, 2, 8, 6, 1},
		{"a reserve that would starve the index keeps it one", 4, 4, 4, 1, 1},
		{"a huge reserve still leaves the index one", 4, 99, 4, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newRequestBudget(tc.capacity, tc.reserve)
			if b.capacity != tc.wantCapacity {
				t.Fatalf("capacity = %d, want %d", b.capacity, tc.wantCapacity)
			}
			if b.indexIdle != tc.wantIdle {
				t.Fatalf("idle index share = %d, want %d", b.indexIdle, tc.wantIdle)
			}
			if b.indexBusy != tc.wantBusy {
				t.Fatalf("busy index share = %d, want %d", b.indexBusy, tc.wantBusy)
			}
		})
	}
}

// within returns a context that ends after d, cancelled when the test ends.
func within(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

// mustStart acquires a slot that has to be free right now.
func mustStart(t *testing.T, acquire func(context.Context) (func(), error), what string) func() {
	t.Helper()
	release, err := acquire(within(t, time.Second))
	if err != nil {
		t.Fatalf("%s did not start: %v", what, err)
	}
	return release
}

// mustWait checks that a request cannot start yet, by giving it a moment.
func mustWait(t *testing.T, acquire func(context.Context) (func(), error), what string) {
	t.Helper()
	release, err := acquire(within(t, 50*time.Millisecond))
	if err == nil {
		release()
		t.Fatalf("%s started; it should have waited", what)
	}
}

// startsSoon runs acquire in the background and reports on the channel when
// it returns.
func startsSoon(acquire func(context.Context) (func(), error), ctx context.Context) <-chan func() {
	got := make(chan func(), 1)
	go func() {
		release, err := acquire(ctx)
		if err != nil {
			release = nil
		}
		got <- release
	}()
	return got
}

// TestInteractiveNeverQueuesBehindTheIndex pins the priority the budget exists
// for: with the index holding every slot it can, the user's requests still
// start at once, up to their own full ceiling.
func TestInteractiveNeverQueuesBehindTheIndex(t *testing.T) {
	b := newRequestBudget(4, 1) // idle index share: 3
	b.quiet = 0

	var held []func()
	defer func() {
		for _, release := range held {
			release()
		}
	}()
	for i := 0; i < 3; i++ {
		held = append(held, mustStart(t, b.acquireIndex, "index request on an idle budget"))
	}
	for i := 0; i < 4; i++ {
		held = append(held, mustStart(t, b.acquireInteractive, "interactive request beside a full index"))
	}
	// The user's own ceiling still holds.
	mustWait(t, b.acquireInteractive, "a fifth interactive request")
}

// TestIndexNarrowsWhileTheUserWorks pins that the index shares the endpoint
// with a running request one index request at a time.
func TestIndexNarrowsWhileTheUserWorks(t *testing.T) {
	b := newRequestBudget(8, 2)
	b.quiet = time.Hour // the user never goes quiet in this test

	request := mustStart(t, b.acquireInteractive, "interactive request")
	defer request()

	first := mustStart(t, b.acquireIndex, "first index request beside the user")
	defer first()
	mustWait(t, b.acquireIndex, "second index request beside the user")
}

// TestTurnKeepsTheIndexNarrowBetweenRequests pins that a running turn counts as
// work even while no request of it is in flight — its tools are running — and
// that ending it lets the index widen.
func TestTurnKeepsTheIndexNarrowBetweenRequests(t *testing.T) {
	b := newRequestBudget(8, 2)
	b.quiet = 0

	end := b.beginTurn()
	first := mustStart(t, b.acquireIndex, "first index request during a turn")
	defer first()
	mustWait(t, b.acquireIndex, "second index request during a turn")

	second := startsSoon(b.acquireIndex, within(t, 2*time.Second))
	end()
	if release := <-second; release == nil {
		t.Fatal("the index did not widen when the turn ended")
	} else {
		release()
	}
}

// TestIndexWidensWhenTheQuietPeriodEnds pins the timer path: nothing is
// released when the quiet period runs out, and the index must still notice.
func TestIndexWidensWhenTheQuietPeriodEnds(t *testing.T) {
	b := newRequestBudget(8, 2)
	b.quiet = 100 * time.Millisecond

	mustStart(t, b.acquireInteractive, "interactive request")() // the user's last request just ended
	first := mustStart(t, b.acquireIndex, "first index request in the quiet period")
	defer first()

	second := startsSoon(b.acquireIndex, within(t, 2*time.Second))
	if release := <-second; release == nil {
		t.Fatal("the index did not widen after the quiet period")
	} else {
		release()
	}
}

// TestQuietPeriodIsMeasuredFromTheLastActivity checks the share and the wait
// the index is given at each point, on a controlled clock.
func TestQuietPeriodIsMeasuredFromTheLastActivity(t *testing.T) {
	b := newRequestBudget(8, 2)
	b.quiet = 30 * time.Second
	now := time.Unix(1_000_000, 0)
	b.now = func() time.Time { return now }

	check := func(wantLimit int, wantWait time.Duration) {
		t.Helper()
		b.mu.Lock()
		limit, wait := b.indexLimitLocked()
		b.mu.Unlock()
		if limit != wantLimit || wait != wantWait {
			t.Fatalf("index limit = %d, wait %v; want %d, wait %v", limit, wait, wantLimit, wantWait)
		}
	}

	check(6, 0) // nothing has happened yet: idle
	release := mustStart(t, b.acquireInteractive, "interactive request")
	now = now.Add(time.Minute)
	check(1, 0) // a request in flight, however long ago it started
	release()
	check(1, 30*time.Second)
	now = now.Add(20 * time.Second)
	check(1, 10*time.Second)
	now = now.Add(10 * time.Second)
	check(6, 0)
}

// TestIndexNeverStartsIntoAFullPool pins that the index only takes room the
// user leaves: with the user's requests at the ceiling it waits, and it starts
// once one of them ends.
func TestIndexNeverStartsIntoAFullPool(t *testing.T) {
	b := newRequestBudget(2, 0)
	b.quiet = 0

	a := mustStart(t, b.acquireInteractive, "first interactive request")
	c := mustStart(t, b.acquireInteractive, "second interactive request")
	defer c()
	mustWait(t, b.acquireIndex, "index request into a full pool")

	waiting := startsSoon(b.acquireIndex, within(t, 2*time.Second))
	a()
	if release := <-waiting; release == nil {
		t.Fatal("the index did not start when a slot came free")
	} else {
		release()
	}
}

// TestIndexWaitsWhileTheUserQueues pins that a slot freed while the user is
// queued goes to the user: the index does not start while anyone waits.
func TestIndexWaitsWhileTheUserQueues(t *testing.T) {
	b := newRequestBudget(2, 0)
	b.quiet = 0

	a := mustStart(t, b.acquireInteractive, "first interactive request")
	c := mustStart(t, b.acquireInteractive, "second interactive request")
	defer c()

	queued := startsSoon(b.acquireInteractive, within(t, 2*time.Second))
	for {
		b.mu.Lock()
		w := b.waiting
		b.mu.Unlock()
		if w == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	mustWait(t, b.acquireIndex, "index request while the user queues")

	a()
	release := <-queued
	if release == nil {
		t.Fatal("the queued interactive request did not start")
	}
	release()
}

// TestAbandonedInteractiveWaitLeavesNoTrace pins that a request giving up its
// place in the queue stops holding the index back: were it still counted as
// waiting, the index would never start again.
func TestAbandonedInteractiveWaitLeavesNoTrace(t *testing.T) {
	b := newRequestBudget(2, 0)
	b.quiet = 0

	a := mustStart(t, b.acquireInteractive, "first interactive request")
	c := mustStart(t, b.acquireInteractive, "second interactive request")
	defer c()

	ctx, giveUp := context.WithCancel(context.Background())
	queued := startsSoon(b.acquireInteractive, ctx)
	for {
		b.mu.Lock()
		w := b.waiting
		b.mu.Unlock()
		if w == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	giveUp()
	if release := <-queued; release != nil {
		release()
		t.Fatal("a cancelled interactive request got a slot")
	}

	index := startsSoon(b.acquireIndex, within(t, 2*time.Second))
	a()
	if release := <-index; release == nil {
		t.Fatal("the index stayed blocked after the queued request gave up")
	} else {
		release()
	}
}

// TestAcquireIndexBlocksUntilReleased pins that the index's share is a real
// wait, not a silent over-admission.
func TestAcquireIndexBlocksUntilReleased(t *testing.T) {
	b := newRequestBudget(3, 1) // idle index share: 2
	b.quiet = 0

	first := mustStart(t, b.acquireIndex, "first index request")
	second := mustStart(t, b.acquireIndex, "second index request")
	defer second()

	third := startsSoon(b.acquireIndex, within(t, 2*time.Second))
	select {
	case release := <-third:
		if release != nil {
			release()
		}
		t.Fatal("third index request returned early; the share is not bounding")
	case <-time.After(75 * time.Millisecond):
	}

	first()
	if release := <-third; release == nil {
		t.Fatal("third index request did not start after a slot was released")
	} else {
		release()
	}
}

func TestAcquireHonoursContextCancellation(t *testing.T) {
	b := newRequestBudget(2, 0) // idle index share: 1
	b.quiet = 0

	held := mustStart(t, b.acquireIndex, "index request")
	defer held()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.acquireIndex(ctx); err == nil {
		t.Fatal("acquireIndex on a cancelled context returned a slot, want an error")
	}
	if _, err := b.acquireInteractive(ctx); err == nil {
		t.Fatal("acquireInteractive on a cancelled context returned a slot, want an error")
	}
}

func TestReleaseCountsTheSlotOnce(t *testing.T) {
	b := newRequestBudget(2, 0)
	b.quiet = 0

	request := mustStart(t, b.acquireInteractive, "interactive request")
	request()
	request()
	end := b.beginTurn()
	end()
	end()
	index := mustStart(t, b.acquireIndex, "index request")
	index()
	index()

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.interactive != 0 || b.turns != 0 || b.index != 0 {
		t.Fatalf("after double releases: interactive=%d turns=%d index=%d, want all 0", b.interactive, b.turns, b.index)
	}
}

func TestBeginTurnIgnoresTheIndexsOwnTurns(t *testing.T) {
	b := requestBudgetForProcess
	turns := func() int {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.turns
	}

	before := turns()
	end := BeginTurn(AsIndexSession(context.Background()))
	if got := turns(); got != before {
		t.Fatalf("an index turn counted as the user's: turns %d -> %d", before, got)
	}
	end()

	end = BeginTurn(context.Background())
	if got := turns(); got != before+1 {
		t.Fatalf("an interactive turn was not counted: turns %d -> %d", before, got)
	}
	end()
	if got := turns(); got != before {
		t.Fatalf("ending the turn left turns at %d, want %d", got, before)
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
