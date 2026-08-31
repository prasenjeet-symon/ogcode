package provider

import "strings"

// CacheVerdict reports whether re-sending a request's prefix to an endpoint is
// cheap. It drives whether the agent is offered a way to compact its own
// context mid-turn: compaction pays for itself only when every step re-pays
// full price for the whole accumulated prefix.
type CacheVerdict string

const (
	// CacheUnknown means the endpoint has not answered yet. Callers should keep
	// observing rather than assume either way.
	CacheUnknown CacheVerdict = "unknown"
	// CacheSupported means a repeated prefix is served from a cache — billed at
	// a discount, or not billed at all. Compacting here is a net loss: it
	// invalidates the cache, so the next request re-establishes the whole prefix
	// at full price.
	CacheSupported CacheVerdict = "caching"
	// CacheAbsent means every step re-pays full price for the entire prefix.
	// This is where in-turn compaction is worth its round trip.
	CacheAbsent CacheVerdict = "none"
)

// BaseURLReporter is implemented by providers that talk to a configurable
// endpoint. Provider ID alone cannot answer the caching question, because
// newOpenAICompatible serves "openai", "openrouter" and "ollama" from one
// implementation with a swappable base URL — and that URL can be repointed at
// any OpenAI-shaped service. The endpoint is the thing that caches, not the
// slot it is configured in.
type BaseURLReporter interface {
	BaseURL() string
}

// StaticCacheVerdict answers from provider identity where identity is actually
// sufficient, and returns CacheUnknown everywhere else so the caller falls back
// to observing what the endpoint reports.
//
// Ollama is CacheAbsent unconditionally, local endpoint or not. An earlier
// version keyed this on the base URL and got it backwards, because a local
// Ollama is a router as much as a runtime: a model named with the cloud marker
// (":cloud" or "-cloud", see cloudModelID) is forwarded to the hosted backend
// and billed per token, while the request still goes to http://localhost:11434.
// The URL says local; the invoice says otherwise. Reading the model name here
// instead would work, but there is no case where withholding compaction from
// Ollama is the better trade: a genuinely local model reuses its KV cache but
// runs in a context window small enough that reclaiming space is worth more
// than the re-prefill it costs.
func StaticCacheVerdict(p Provider) CacheVerdict {
	if p == nil {
		return CacheUnknown
	}
	switch p.ID() {
	case "anthropic":
		// anthropic.go attaches cache_control breakpoints on every request, so
		// caching is a property of our own client, not of configuration.
		return CacheSupported
	case "ollama":
		return CacheAbsent
	}

	// Supplement to the switch above, not a replacement for it: Ollama's
	// OpenAI-compatible endpoint is routinely configured in the generic "openai"
	// slot, where p.ID() is "openai" and the case above never fires. Ollama
	// reports no cached-token field at all, so the observer is blind there and
	// can only reach CacheAbsent by exhausting its whole window — costing the
	// first turn of a fresh install the compaction it should have had.
	//
	// Strictly additive, and it must stay that way: a URL that looks local can
	// never imply CacheSupported (see the router note above), it can only
	// recognise an endpoint already known not to cache.
	if r, ok := p.(BaseURLReporter); ok && isOllamaBaseURL(r.BaseURL()) {
		return CacheAbsent
	}
	return CacheUnknown
}

// ollamaDefaultPort is the port Ollama listens on. Matching it is what lets an
// Ollama server configured in an OpenAI-compatible slot still be recognised.
const ollamaDefaultPort = ":11434"

// isOllamaBaseURL reports whether a base URL addresses an Ollama server.
func isOllamaBaseURL(baseURL string) bool {
	return strings.Contains(baseURL, ollamaDefaultPort)
}

// cacheObservationSteps is how many steps with a repeated prefix must report
// zero cache reads before an endpoint is declared non-caching. One step is not
// enough: the first request of a turn establishes the prefix rather than
// reusing it, and legitimately reports no cache read on a caching provider.
const cacheObservationSteps = 3

// CacheObserver resolves the verdict for endpoints StaticCacheVerdict cannot
// answer, by watching what comes back on the wire. Observed evidence outranks
// any table we could ship: it survives a user repointing a base URL at a
// different service, and needs no per-model catalog upkeep.
//
// The zero value is ready to use and reports CacheUnknown.
type CacheObserver struct {
	verdict CacheVerdict
	steps   int
}

// NewCacheObserver seeds an observer from the static verdict. When identity
// already settles the question the observer is final immediately and never
// changes its mind, so no observation budget is spent.
func NewCacheObserver(p Provider) *CacheObserver {
	return &CacheObserver{verdict: StaticCacheVerdict(p)}
}

// Observe folds in one step's reported cache usage.
//
// repeatedPrefix reports whether this request actually shared a prefix with the
// previous one — false on the first step of a turn, and after a compaction has
// rewritten the history. Steps without a repeated prefix carry no evidence
// either way and are ignored, so a turn that compacts early cannot mislead the
// observer into declaring a caching endpoint non-caching.
func (o *CacheObserver) Observe(cacheReadTokens, cacheWriteTokens int, repeatedPrefix bool) {
	if o.verdict != CacheUnknown {
		return
	}
	if cacheReadTokens > 0 || cacheWriteTokens > 0 {
		// Any cache accounting at all proves the endpoint caches. One positive
		// report is conclusive in a way that a zero never is.
		o.verdict = CacheSupported
		return
	}
	if !repeatedPrefix {
		return
	}
	o.steps++
	if o.steps >= cacheObservationSteps {
		o.verdict = CacheAbsent
	}
}

// Verdict returns the current answer. It stays CacheUnknown until there is
// enough evidence, and callers must treat unknown as "do not offer compaction
// yet" rather than as either verdict.
func (o *CacheObserver) Verdict() CacheVerdict {
	if o == nil || o.verdict == "" {
		return CacheUnknown
	}
	return o.verdict
}

// SettledCacheObserver returns an observer that already holds v and will not
// change its mind. Used to carry a verdict established in an earlier turn into
// a new one without re-running the observation window.
func SettledCacheObserver(v CacheVerdict) *CacheObserver {
	return &CacheObserver{verdict: v}
}
