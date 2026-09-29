package provider

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// concurrencyEnv names the environment variable that caps how many
// chat-completions requests may be in flight across the whole process.
const concurrencyEnv = "OGCODE_PROVIDER_MAX_CONCURRENT"

// defaultMaxConcurrentRequests is the process-wide in-flight ceiling when the
// operator sets nothing. It sits just above the indexer's own per-run limit of
// five: a single index run behaves as it did before, while a second run can no
// longer stack on top of the first.
const defaultMaxConcurrentRequests = 8

// interactiveRequestReserve is how many slots the index leaves free even while
// nobody is working. Interactive requests never wait on the index's slots, so
// the reserve is not what lets a turn through; it keeps a turn that starts in
// the middle of a wide index run inside the ceiling, not on top of it.
const interactiveRequestReserve = 2

// indexBusyShare is how many index requests may run while the user is working.
// One keeps a long turn from starving the index outright; more would share the
// endpoint — and on a local model its few generation slots — with the turn the
// user is watching.
const indexBusyShare = 1

// interactiveQuietPeriod is how long the user's work must have been idle before
// the index opens to its full share. The auto-index starts the moment a turn
// ends, which is exactly when the next prompt is most likely to follow; staying
// narrow through that pause keeps the next turn from landing behind a full
// index run.
const interactiveQuietPeriod = 30 * time.Second

// maxConcurrentRequests is the operator's ceiling, read and parsed once.
var maxConcurrentRequests = sync.OnceValue(func() int {
	return parseMaxConcurrent(os.Getenv(concurrencyEnv))
})

// requestBudget bounds how many provider requests may be in flight at once, and
// puts the user's own work ahead of the background project index.
//
// The upstream failure the ceiling exists for is not slowness but refusal. A
// project index runs many sessions at once, each a full agent turn of two or
// more requests, and a wave of them can trip the endpoint's rate limiter. The
// retry path answers a 429 by waiting and resending, so once a burst is
// throttled the retries are added on top of the requests still running — the
// burst becomes both the cause and the amplification of the rate limit. A
// single ceiling across the process turns the index's waves into a queue.
//
// The priority is the other half. The auto-index runs on the model the turn
// that triggered it used, so indexing and the user's turns share one endpoint,
// one rate limit and often one concurrency limit: a local model generates a few
// requests at a time, and a hosted plan queues requests past a per-account
// count. Holding a few slots back for the user was not enough — the turn still
// queued behind index requests upstream, and its sub-agents and utility calls
// outran the reserve. Measured with five index sessions running on a turn's
// model, the turn's requests took a median 12 s to connect and over 50 s at the
// tail, against 2.6 s with the index idle. So the two lanes are asymmetric:
//
//   - An interactive request (any request not made under AsIndexSession) waits
//     only on other interactive requests: it starts while fewer than capacity
//     of them are in flight, however many the index holds.
//   - An index request takes what the user leaves. It starts only while no
//     interactive request is waiting and the total in flight is under capacity,
//     and at most indexBusy of them run while the user is working — a turn
//     running, an interactive request in flight, or one finished within the
//     quiet period — or indexIdle otherwise.
//
// A request already streaming is never preempted; cancelling it would throw
// away tokens already paid for. The budget decides what starts, so an index run
// that opened wide while the user was idle narrows as its requests finish.
type requestBudget struct {
	capacity  int           // most interactive requests in flight; the total an index request may start into
	indexIdle int           // most index requests in flight while the user is idle
	indexBusy int           // most index requests in flight while the user is working
	quiet     time.Duration // how long the user must be idle before the index widens
	now       func() time.Time

	mu          sync.Mutex
	interactive int           // interactive requests in flight
	waiting     int           // interactive requests queued for a slot
	index       int           // index requests in flight
	turns       int           // interactive agent turns running (see BeginTurn)
	lastActive  time.Time     // when an interactive request or turn last started or ended
	changed     chan struct{} // closed and replaced on every change that can admit a waiter
}

// newRequestBudget builds a budget with the given capacity and the number of
// slots the index leaves free while the user is idle. It clamps to a shape that
// can always make progress: at least two slots, and an index share of at least
// one that never covers the whole ceiling.
func newRequestBudget(capacity, reserve int) *requestBudget {
	if capacity < 2 {
		capacity = 2
	}
	indexIdle := min(max(capacity-reserve, 1), capacity-1)
	return &requestBudget{
		capacity:  capacity,
		indexIdle: indexIdle,
		indexBusy: min(indexBusyShare, indexIdle),
		quiet:     interactiveQuietPeriod,
		now:       time.Now,
		changed:   make(chan struct{}),
	}
}

// requestBudgetForProcess is the one budget every provider request draws from.
var requestBudgetForProcess = newRequestBudget(maxConcurrentRequests(), interactiveRequestReserve)

// acquireInteractive waits for a slot for one interactive request. Only other
// interactive requests can make it wait: the index's requests do not count
// against it, so indexing never queues the user's work. The returned function
// releases the slot and must be called when the request is finished.
func (b *requestBudget) acquireInteractive(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	if b.interactive >= b.capacity {
		b.waiting++
		for b.interactive >= b.capacity {
			changed := b.changed
			b.mu.Unlock()
			if err := waitForChange(ctx, changed, 0); err != nil {
				b.mu.Lock()
				b.waiting--
				// The index holds back while anyone waits here; the last waiter
				// leaving is a change it has to hear about.
				b.broadcastLocked()
				b.mu.Unlock()
				return nil, err
			}
			b.mu.Lock()
		}
		b.waiting--
	}
	b.interactive++
	b.lastActive = b.now()
	b.mu.Unlock()
	return b.releaser(func() {
		b.interactive--
		b.lastActive = b.now()
	}), nil
}

// acquireIndex waits for a slot for one background index request: room the
// user is not using, within the index's share of the moment. When the only
// thing holding it back is the quiet period after the user's last activity, it
// waits that out on a timer, because nothing is released when the period ends.
func (b *requestBudget) acquireIndex(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	for {
		limit, widensIn := b.indexLimitLocked()
		if b.waiting == 0 && b.index < limit && b.interactive+b.index < b.capacity {
			break
		}
		changed := b.changed
		b.mu.Unlock()
		if err := waitForChange(ctx, changed, widensIn); err != nil {
			return nil, err
		}
		b.mu.Lock()
	}
	b.index++
	b.mu.Unlock()
	return b.releaser(func() { b.index-- }), nil
}

// indexLimitLocked is how many index requests may be in flight right now, and,
// while only the quiet period keeps that at the busy share, how long until it
// widens. b.mu must be held.
func (b *requestBudget) indexLimitLocked() (int, time.Duration) {
	if b.turns > 0 || b.interactive > 0 || b.waiting > 0 {
		return b.indexBusy, 0
	}
	if idle := b.now().Sub(b.lastActive); idle < b.quiet {
		return b.indexBusy, b.quiet - idle
	}
	return b.indexIdle, 0
}

// beginTurn marks one interactive agent turn as running until the returned
// function is called. A turn is working even between its requests, while its
// tools run, so the index stays narrow for the whole turn instead of widening
// in every gap and filling the endpoint ahead of the next step.
func (b *requestBudget) beginTurn() func() {
	b.mu.Lock()
	b.turns++
	b.lastActive = b.now()
	b.mu.Unlock()
	return b.releaser(func() {
		b.turns--
		b.lastActive = b.now()
	})
}

// releaser returns a function that runs undo once, under the lock, and wakes
// every waiter to re-check whether it may start. Callers release on more than
// one path, so a second call must not count the slot free twice.
func (b *requestBudget) releaser(undo func()) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			undo()
			b.broadcastLocked()
			b.mu.Unlock()
		})
	}
}

// broadcastLocked wakes everything waiting on the budget. b.mu must be held.
func (b *requestBudget) broadcastLocked() {
	close(b.changed)
	b.changed = make(chan struct{})
}

// waitForChange blocks until changed is closed, after widensIn when it is
// positive, or until ctx ends, whose error it then returns.
func waitForChange(ctx context.Context, changed <-chan struct{}, widensIn time.Duration) error {
	var widened <-chan time.Time
	if widensIn > 0 {
		timer := time.NewTimer(widensIn)
		defer timer.Stop()
		widened = timer.C
	}
	select {
	case <-changed:
	case <-widened:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// acquire routes one request to the lane its context earns.
func (b *requestBudget) acquire(ctx context.Context) (func(), error) {
	if isIndexSession(ctx) {
		return b.acquireIndex(ctx)
	}
	return b.acquireInteractive(ctx)
}

// indexSessionKey marks a context as belonging to a background project-index
// run. The agent loop runs interactive turns and index turns through the same
// code, so the mark travels on the context rather than being threaded as a
// parameter through every layer between the indexer and the provider.
type indexSessionKey struct{}

// AsIndexSession marks ctx as belonging to a background project-index run.
// Every provider request made under it takes the index's lane of the in-flight
// budget, which yields to the user's own work. The indexer applies it to the
// context it runs each batch under; no other caller should.
func AsIndexSession(ctx context.Context) context.Context {
	return context.WithValue(ctx, indexSessionKey{}, true)
}

// isIndexSession reports whether ctx was marked by AsIndexSession.
func isIndexSession(ctx context.Context) bool {
	v, _ := ctx.Value(indexSessionKey{}).(bool)
	return v
}

// BeginTurn tells the in-flight budget that an agent turn is running, so the
// background index keeps to its narrow share until the returned function is
// called and the quiet period after it has passed. A turn run under
// AsIndexSession is the index's own work and is not counted. A nested turn — a
// sub-agent inside a turn — simply counts twice.
func BeginTurn(ctx context.Context) func() {
	if isIndexSession(ctx) {
		return func() {}
	}
	return requestBudgetForProcess.beginTurn()
}

// acquireRequest reserves a place in the process-wide in-flight budget for one
// request. The returned function releases it and must be called once the
// request is done — for a streamed request, when the stream has ended, not when
// the first bytes arrive, because the generation is still running while the
// body is being drained.
func acquireRequest(ctx context.Context) (func(), error) {
	return requestBudgetForProcess.acquire(ctx)
}

// budgetBody ties a reserved place in the in-flight budget to the body of the
// response that place was taken for. Provider streaming owns no Close beyond
// the response body's, so releasing there — on every return path out of the
// stream reader, including a mid-stream interruption — is what keeps the
// budget's count honest without a second bookkeeping path.
type budgetBody struct {
	io.ReadCloser
	release func()
}

// Close returns the slot reserved for this request and closes the body beneath
// it. It is idempotent, so a path that closes the body twice still releases the
// slot exactly once.
func (b *budgetBody) Close() error {
	if b.release != nil {
		b.release()
		b.release = nil
	}
	return b.ReadCloser.Close()
}

// parseMaxConcurrent interprets concurrencyEnv. A positive integer of at least
// two is the ceiling; empty or unusable keeps the default rather than failing
// every request over a typo. One is refused deliberately: the index could never
// start beside even a single interactive request, and a long session would
// leave it no room at all.
func parseMaxConcurrent(raw string) int {
	v := strings.TrimSpace(raw)
	if v == "" {
		return defaultMaxConcurrentRequests
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 2 {
		slog.Warn("ignoring unusable provider concurrency, keeping the default",
			"env", concurrencyEnv, "value", raw, "default", defaultMaxConcurrentRequests)
		return defaultMaxConcurrentRequests
	}
	return n
}
