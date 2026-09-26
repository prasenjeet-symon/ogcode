package server

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// catalogProvider is a test provider that owns an in-memory catalogue like the
// real ones: SetCatalog seeds it (no fetch), RefreshCatalog "fetches" it. It
// lets the server tests exercise the seed/refresh/persist wiring without a real
// endpoint or reaching into *OpenAIProvider's unexported fields.
type catalogProvider struct {
	id string
	// live is the list RefreshCatalog installs and returns; nil simulates a
	// failed/empty fetch (nothing to persist).
	live []provider.ModelInfo

	mu      sync.Mutex
	catalog []provider.ModelInfo

	refreshes atomic.Int64
}

func (p *catalogProvider) ID() string { return p.id }

func (p *catalogProvider) Models() []provider.ModelInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.ModelInfo(nil), p.catalog...)
}

func (p *catalogProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	close(ch)
	return ch, nil
}

func (p *catalogProvider) SetCatalog(models []provider.ModelInfo) {
	if len(models) == 0 {
		return
	}
	p.mu.Lock()
	p.catalog = append([]provider.ModelInfo(nil), models...)
	p.mu.Unlock()
}

func (p *catalogProvider) RefreshCatalog(ctx context.Context) []provider.ModelInfo {
	p.refreshes.Add(1)
	if p.live == nil {
		return nil
	}
	p.SetCatalog(p.live)
	return append([]provider.ModelInfo(nil), p.live...)
}

// TestSeedModelCatalogPopulatesRegistryFromStore pins the startup path: a
// catalogue persisted by a previous run is installed into the freshly-built
// provider with no fetch, so the picker answers immediately.
func TestSeedModelCatalogPopulatesRegistryFromStore(t *testing.T) {
	srv := newTestServer(t)
	p := &catalogProvider{id: "openrouter"}
	srv.registry.Register(p)

	if err := session.SetModelCatalog(srv.globalDB, "openrouter", []session.CatalogModel{
		{ID: "persisted-1", Name: "Persisted One", ProviderID: "openrouter", ActiveByDefault: true},
	}); err != nil {
		t.Fatalf("persist catalog: %v", err)
	}

	srv.seedModelCatalog()

	if got := p.Models(); len(got) != 1 || got[0].ID != "persisted-1" {
		t.Fatalf("provider catalogue after seed = %+v, want the persisted model", got)
	}
	if p.refreshes.Load() != 0 {
		t.Fatal("seeding must not fetch")
	}
}

// TestSeedModelCatalogPrunesUnregisteredProvider pins that a catalogue whose
// provider is no longer registered is dropped, so a disconnect cannot leave a
// stale list behind.
func TestSeedModelCatalogPrunesUnregisteredProvider(t *testing.T) {
	srv := newTestServer(t)
	if err := session.SetModelCatalog(srv.globalDB, "ogx", []session.CatalogModel{
		{ID: "plan-model", ProviderID: "ogx"},
	}); err != nil {
		t.Fatalf("persist catalog: %v", err)
	}

	// The registry has no ogx provider (the account is disconnected).
	srv.seedModelCatalog()

	rows, err := session.GetModelCatalog(srv.globalDB)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("a disconnected provider's catalogue was not pruned: %+v", rows)
	}
}

// TestRefreshModelCatalogsPersistsAndPublishes pins the refresh path end to end:
// a live fetch is installed, written back to the store (so the next start seeds
// from it), and announced on the bus with models.updated so an open picker
// re-reads without a manual reload.
func TestRefreshModelCatalogsPersistsAndPublishes(t *testing.T) {
	srv := newTestServer(t)
	srv.bus = bus.New(64)
	events := srv.bus.SubscribeAll()

	srv.registry.Register(&catalogProvider{
		id:   "openrouter",
		live: []provider.ModelInfo{{ID: "live-1", ProviderID: "openrouter"}},
	})

	srv.refreshModelCatalogsNow(context.Background())

	rows, err := session.GetModelCatalog(srv.globalDB)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "live-1" {
		t.Fatalf("refreshed catalogue persisted %+v, want live-1", rows)
	}
	select {
	case evt := <-events:
		if evt.Type != "models.updated" {
			t.Fatalf("event type = %q, want models.updated", evt.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no models.updated event published after a refresh")
	}
}

// TestRefreshModelCatalogsGuardIsExclusive pins that two refreshes cannot run at
// once: while one holds the guard, a second waits for it rather than starting a
// second fetch per provider — so exactly one fetch happens, and the waiter still
// returns a list that reflects a refresh that really ran.
func TestRefreshModelCatalogsGuardIsExclusive(t *testing.T) {
	srv := newTestServer(t)
	srv.bus = bus.New(64)

	// A provider whose refresh blocks until released, so the first refresh holds
	// the guard while the second is attempted.
	blocking := &blockingCatalogProvider{id: "openrouter", release: make(chan struct{})}
	srv.registry.Register(blocking)

	firstDone := make(chan struct{})
	go func() { srv.refreshModelCatalogsNow(context.Background()); close(firstDone) }()
	for blocking.entered.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}

	// The second caller blocks on the first (it does not start a fetch). Run it
	// concurrently and confirm no second fetch is made while it waits.
	secondDone := make(chan struct{})
	go func() { srv.refreshModelCatalogsNow(context.Background()); close(secondDone) }()
	time.Sleep(50 * time.Millisecond)
	if got := blocking.refreshes.Load(); got != 1 {
		t.Fatalf("second refresh fetched again (refreshes=%d), want the guard to make it wait", got)
	}
	select {
	case <-secondDone:
		t.Fatal("the second refresh returned while the first still held the guard")
	default:
	}

	close(blocking.release)
	<-firstDone
	select {
	case <-secondDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the waiting refresh did not return after the first finished")
	}
	if got := blocking.refreshes.Load(); got != 1 {
		t.Fatalf("refreshes = %d, want exactly 1 (the second waited)", got)
	}
}

// blockingCatalogProvider blocks inside RefreshCatalog until released.
type blockingCatalogProvider struct {
	id      string
	release chan struct{}
	entered atomic.Int64

	refreshes atomic.Int64
}

func (p *blockingCatalogProvider) ID() string { return p.id }
func (p *blockingCatalogProvider) Models() []provider.ModelInfo {
	return nil
}
func (p *blockingCatalogProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	close(ch)
	return ch, nil
}
func (p *blockingCatalogProvider) RefreshCatalog(ctx context.Context) []provider.ModelInfo {
	p.entered.Add(1)
	p.refreshes.Add(1)
	<-p.release
	return nil
}
