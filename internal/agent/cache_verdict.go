package agent

import (
	"log/slog"
	"sync"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// Resolved cache verdicts are memoized in process on top of the database, so a
// turn against an already-resolved pair costs neither an observation window nor
// a query. The map is package scoped rather than a LoopRunner field because
// RunSubagent copies the runner by value, and an embedded mutex would be copied
// with it.
var (
	cacheVerdictMu sync.Mutex
	cacheVerdicts  = map[string]provider.CacheVerdict{}
)

// endpointKey identifies the service behind a provider slot: the slot alone is
// not enough, because "openai" fronts any OpenAI-shaped service and its base
// URL can be repointed at will.
func endpointKey(p provider.Provider) string {
	if p == nil {
		return ""
	}
	key := p.ID()
	if r, ok := p.(provider.BaseURLReporter); ok {
		key += "|" + r.BaseURL()
	}
	return key
}

// verdictKey pairs the model with the endpoint serving it. Caching is a
// property of both: OpenRouter proxies different upstreams per model, so two
// models on one endpoint can disagree — and one model can be served by two
// endpoints that disagree, which is exactly the local-vs-hosted Ollama case.
func verdictKey(p provider.Provider, modelID string) string {
	ep := endpointKey(p)
	if ep == "" || modelID == "" {
		return ""
	}
	return modelID + "@" + ep
}

// cacheVerdictFor returns the verdict already established for a model on an
// endpoint, or CacheUnknown when the pair has never been resolved. The
// in-process memo is consulted first; a miss falls through to the database, so
// a verdict resolved in an earlier run of the binary is still honoured.
func cacheVerdictFor(database *db.DB, p provider.Provider, modelID string) provider.CacheVerdict {
	key := verdictKey(p, modelID)
	if key == "" {
		return provider.CacheUnknown
	}

	cacheVerdictMu.Lock()
	v, ok := cacheVerdicts[key]
	cacheVerdictMu.Unlock()
	if ok {
		return v
	}

	if database == nil {
		return provider.CacheUnknown
	}
	stored, found, err := session.GetModelCacheSupport(database, modelID, endpointKey(p))
	if err != nil {
		// A read failure must not strand the turn: fall back to observing, which
		// costs a window but always produces a usable answer.
		slog.Warn("read cache verdict", "model", modelID, "err", err)
		return provider.CacheUnknown
	}
	if !found {
		return provider.CacheUnknown
	}

	verdict := provider.CacheVerdict(stored)
	cacheVerdictMu.Lock()
	cacheVerdicts[key] = verdict
	cacheVerdictMu.Unlock()
	return verdict
}

// rememberCacheVerdict persists a resolved verdict so no later session observes
// this pair again. Unknown is never stored: it carries no information, and
// storing it would let an unresolved pair masquerade as a settled one.
func rememberCacheVerdict(database *db.DB, p provider.Provider, modelID string, v provider.CacheVerdict) {
	if v == provider.CacheUnknown {
		return
	}
	if provider.StaticCacheVerdict(p) != provider.CacheUnknown {
		// Answered by a rule, so nothing was observed and there is nothing worth
		// storing. Writing it anyway would leave a stale row behind the moment
		// that rule changes.
		return
	}
	key := verdictKey(p, modelID)
	if key == "" {
		return
	}

	cacheVerdictMu.Lock()
	existing, ok := cacheVerdicts[key]
	if !ok || existing != v {
		cacheVerdicts[key] = v
	}
	cacheVerdictMu.Unlock()
	if ok && existing == v {
		// Already persisted by the turn that first resolved it; every later turn
		// would otherwise re-write the same row.
		return
	}

	if database == nil {
		return
	}
	if err := session.SetModelCacheSupport(database, modelID, endpointKey(p), string(v), session.Now()); err != nil {
		slog.Warn("persist cache verdict", "model", modelID, "verdict", v, "err", err)
		return
	}
	slog.Info("resolved prompt-cache support", "model", modelID, "endpoint", endpointKey(p), "verdict", v)
}

// newCacheObserver builds the observer for one turn, pre-loaded with anything
// already known about this model and endpoint.
//
// The static rule is consulted BEFORE the persisted one and wins outright.
// Static verdicts are code, and code changes with a release; a row written by
// an older build states what that build believed, and letting it answer would
// pin every user to the first verdict their install ever reached. Persistence
// exists to save an observation window, not to outrank a rule.
func newCacheObserver(database *db.DB, p provider.Provider, modelID string) *provider.CacheObserver {
	if v := provider.StaticCacheVerdict(p); v != provider.CacheUnknown {
		return provider.SettledCacheObserver(v)
	}
	if v := cacheVerdictFor(database, p, modelID); v != provider.CacheUnknown {
		return provider.SettledCacheObserver(v)
	}
	return provider.NewCacheObserver(p)
}

// resetCacheVerdicts clears the in-process memo. Tests only — a verdict
// established by one test would otherwise settle the pair for every test after.
func resetCacheVerdicts() {
	cacheVerdictMu.Lock()
	defer cacheVerdictMu.Unlock()
	cacheVerdicts = map[string]provider.CacheVerdict{}
}
