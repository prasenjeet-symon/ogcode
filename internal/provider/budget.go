package provider

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
)

// concurrencyEnv names the environment variable that caps how many
// chat-completions requests may be in flight across the whole process.
const concurrencyEnv = "OGCODE_PROVIDER_MAX_CONCURRENT"

// defaultMaxConcurrentRequests is the process-wide in-flight ceiling when the
// operator sets nothing. It sits just above the indexer's own per-run limit of
// five: a single index run behaves as it did before, while a second run — or a
// run racing an interactive turn — can no longer stack on top of the first.
const defaultMaxConcurrentRequests = 8

// interactiveRequestReserve is how many of those slots are kept out of the
// background index's reach: the number of concurrent turns a user could
// plausibly be running. The index draws only from the remainder, so however
// many of its sessions are ready it can never hold every slot and leave a real
// turn queued behind indexing the user did not start. One slot always stays
// outside the index's reach even when this would leave it none, so a small
// total still lets both run.
const interactiveRequestReserve = 2

// maxConcurrentRequests is the operator's ceiling, read and parsed once.
var maxConcurrentRequests = sync.OnceValue(func() int {
	return parseMaxConcurrent(os.Getenv(concurrencyEnv))
})

// requestBudget bounds how many provider requests may be in flight at once, and
// how much of that the background project index may hold.
//
// The upstream failure this exists for is not slowness but refusal. A project
// index runs many sessions at once, each a full agent turn of two or more
// requests, and a wave of them can trip the endpoint's rate limiter. The retry
// path answers a 429 by waiting and resending, so once a burst is throttled the
// retries are added on top of the requests still running — the burst becomes
// both the cause and the amplification of the rate limit. A single ceiling
// across the process turns the index's waves into a queue.
//
// The reserve is the other half of the point. Indexing and the user's own turns
// share one endpoint and, through it, one rate limit; without a reserve a
// finished turn's background refresh could fill every slot and make the next
// turn — the one the user is watching — wait behind it.
type requestBudget struct {
	// total is the shared ceiling: every request, interactive or index, holds
	// one place in it for its whole lifetime.
	total chan struct{}
	// indexGate bounds how many index requests are admitted at all, so they
	// cannot occupy the interactive reserve even while it sits idle.
	indexGate chan struct{}
}

// newRequestBudget builds a budget with the given total capacity and the number
// of slots held back from the index. It clamps to a shape that can always make
// progress: at least two slots in total, at least one for the index, and at
// least one kept for interactive turns.
func newRequestBudget(capacity, reserve int) *requestBudget {
	if capacity < 2 {
		capacity = 2
	}
	indexMax := capacity - reserve
	if indexMax < 1 {
		indexMax = 1
	}
	if indexMax > capacity-1 {
		indexMax = capacity - 1
	}
	return &requestBudget{
		total:     make(chan struct{}, capacity),
		indexGate: make(chan struct{}, indexMax),
	}
}

// requestBudgetForProcess is the one budget every provider request draws from.
var requestBudgetForProcess = newRequestBudget(maxConcurrentRequests(), interactiveRequestReserve)

// acquireInteractive waits for a slot for one interactive request. It is the
// path every request takes unless its context is marked as a background index
// session. The returned function releases the slot and must be called when the
// request is finished.
func (b *requestBudget) acquireInteractive(ctx context.Context) (func(), error) {
	select {
	case b.total <- struct{}{}:
		return func() { <-b.total }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// acquireIndex waits for a slot for one background index request: first a place
// within the index's share of the pool, then a place in the pool itself.
// Taking the indexGate first is what keeps the reserve free — an index request
// waiting for room in the total pool already holds gate space, and the
// interactive path never waits on the gate, so the two cannot deadlock.
func (b *requestBudget) acquireIndex(ctx context.Context) (func(), error) {
	select {
	case b.indexGate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case b.total <- struct{}{}:
	case <-ctx.Done():
		<-b.indexGate
		return nil, ctx.Err()
	}
	return func() {
		<-b.total
		<-b.indexGate
	}, nil
}

// acquire routes one request to the share its context earns.
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
// Every provider request made under it draws the index's share of the in-flight
// budget instead of the shared pool. The indexer applies it to the context it
// runs each batch under; no other caller should.
func AsIndexSession(ctx context.Context) context.Context {
	return context.WithValue(ctx, indexSessionKey{}, true)
}

// isIndexSession reports whether ctx was marked by AsIndexSession.
func isIndexSession(ctx context.Context) bool {
	v, _ := ctx.Value(indexSessionKey{}).(bool)
	return v
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
// every request over a typo. One is refused deliberately: a single slot shared
// between the index and the interactive path would have no room for the
// reserve, which is the guarantee the budget exists to make.
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
