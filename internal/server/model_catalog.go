package server

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/modelcatalog"
)

// The model catalogue of every provider is persisted in the global config DB so
// a fresh process can populate the picker without a network call, and refreshed
// in the background so a live picker updates on its own. The conversions and the
// seed/persist logic live in internal/modelcatalog (shared with the CLI and the
// worker); this file owns the server-only part: the refresh guard and the
// models.updated event that follows a refresh.
//
// The split exists because Models() must never block: it is a pure read of the
// in-memory catalogue, and every fetch runs here, off the read path.

// modelCatalogRefreshTimeout bounds one background catalogue refresh across all
// providers. Generous: a refresh is detached from any request, so waiting longer
// costs nothing but a later update, while re-fetching an unreachable endpoint
// too eagerly would burn connections.
const modelCatalogRefreshTimeout = 45 * time.Second

// catalogRefreshState serializes catalogue refreshes. A refresh holds `running`
// for its whole span (one fetch per provider); a caller that arrives while one
// is in flight waits on `done` instead of starting a second, so the manual
// /models/refresh path can return a list that reflects a refresh it did not have
// to start.
type catalogRefreshState struct {
	mu      sync.Mutex
	running bool
	done    chan struct{}
}

// beginCatalogRefresh acquires the refresh slot. It returns started=false and a
// channel that closes when the in-flight refresh finishes, when one is already
// running; otherwise it returns started=true and a release func the caller must
// call (via defer) when its refresh completes.
func (c *catalogRefreshState) begin() (started bool, wait <-chan struct{}, release func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		return false, c.done, func() {}
	}
	c.running = true
	c.done = make(chan struct{})
	done := c.done
	return true, done, func() {
		c.mu.Lock()
		c.running = false
		close(done)
		c.mu.Unlock()
	}
}

// seedModelCatalog populates every registered provider's in-memory catalogue from
// the persisted copy, so the picker answers immediately on startup.
//
// The registry it is given holds the full configured provider set, so it also
// prunes catalogues whose provider has unregistered (a cleared API key, an OGX
// disconnect) — a stale list would otherwise reappear the moment the provider
// was re-added.
func (s *Server) seedModelCatalog() {
	if s.registry == nil || s.globalDB == nil {
		return
	}
	if err := modelcatalog.Seed(s.registry, s.globalDB); err != nil {
		slog.Warn("failed to seed model catalog", "err", err)
	}
	if err := modelcatalog.Prune(s.registry, s.globalDB); err != nil {
		slog.Warn("failed to prune stale model catalog", "err", err)
	}
}

// refreshModelCatalogsNow fetches every provider's live catalogue, persists it,
// and publishes models.updated so an open picker re-reads the list. It is the
// one network path for model lists; callers run it OFF a read path (in the
// background, or from the explicit refresh request).
//
// When another refresh is already in flight this waits for it rather than
// starting a second — so the manual path still returns a list that reflects a
// refresh (the one it waited on) instead of a half-updated one.
func (s *Server) refreshModelCatalogsNow(ctx context.Context) {
	if s.registry == nil || s.globalDB == nil {
		return
	}
	started, wait, release := s.catalogRefresh.begin()
	if !started {
		select {
		case <-wait:
		case <-ctx.Done():
		}
		return
	}
	defer release()

	ctx, cancel := context.WithTimeout(ctx, modelCatalogRefreshTimeout)
	defer cancel()

	refreshed := s.registry.RefreshCatalogs(ctx)
	if len(refreshed) == 0 {
		return
	}
	if err := modelcatalog.Persist(s.globalDB, refreshed); err != nil {
		slog.Warn("failed to persist model catalog", "err", err)
	}
	s.bus.Publish("models.updated", map[string]int{"providers": len(refreshed)})
}

// refreshModelCatalogsInBackground runs refreshModelCatalogsNow in a detached
// goroutine, recovering from a panic so a bad fetch can never take down the
// server. Used at startup and after a credential change, where the caller must
// not wait on the network.
func (s *Server) refreshModelCatalogsInBackground() {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("model catalog refresh panicked", "panic", r)
			}
		}()
		s.refreshModelCatalogsNow(context.Background())
	}()
}
