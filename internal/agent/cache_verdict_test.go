package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// testDB opens a migrated database in a temp dir.
func testDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// verdictProvider is a Provider whose only interesting property is its identity
// and, optionally, its endpoint.
type verdictProvider struct {
	id  string
	url string
}

func (v *verdictProvider) ID() string                   { return v.id }
func (v *verdictProvider) Models() []provider.ModelInfo { return nil }
func (v *verdictProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	close(ch)
	return ch, nil
}

// urlVerdictProvider additionally reports its base URL.
type urlVerdictProvider struct{ verdictProvider }

func (v *urlVerdictProvider) BaseURL() string { return v.url }

func TestEndpointKeyDistinguishesSlotAndURL(t *testing.T) {
	sameSlotDifferentURL := endpointKey(&urlVerdictProvider{verdictProvider{id: "openai", url: "https://a.example/v1"}}) !=
		endpointKey(&urlVerdictProvider{verdictProvider{id: "openai", url: "https://b.example/v1"}})
	if !sameSlotDifferentURL {
		t.Error("same slot pointed at different URLs must not share a verdict")
	}

	sameURLDifferentSlot := endpointKey(&urlVerdictProvider{verdictProvider{id: "openai", url: "https://a.example/v1"}}) !=
		endpointKey(&urlVerdictProvider{verdictProvider{id: "openrouter", url: "https://a.example/v1"}})
	if !sameURLDifferentSlot {
		t.Error("different slots at the same URL must not share a verdict")
	}

	if got := endpointKey(nil); got != "" {
		t.Errorf("endpointKey(nil) = %q, want empty", got)
	}
}

func TestRememberedVerdictSeedsALaterTurn(t *testing.T) {
	resetCacheVerdicts()
	t.Cleanup(resetCacheVerdicts)
	database := testDB(t)

	p := &urlVerdictProvider{verdictProvider{id: "openai", url: "https://gateway.example/v1"}}

	// First turn: identity settles nothing, so the observer starts unknown and
	// has to spend its window discovering the endpoint does not cache.
	first := newCacheObserver(database, p, "test-model")
	if got := first.Verdict(); got != provider.CacheUnknown {
		t.Fatalf("first turn starts at %q, want unknown", got)
	}
	for i := 0; i < 3; i++ {
		first.Observe(0, 0, true)
	}
	if got := first.Verdict(); got != provider.CacheAbsent {
		t.Fatalf("first turn resolved to %q, want none", got)
	}
	rememberCacheVerdict(database, p, "test-model", first.Verdict())

	// Second turn against the same endpoint starts settled — no window spent.
	second := newCacheObserver(database, p, "test-model")
	if got := second.Verdict(); got != provider.CacheAbsent {
		t.Errorf("second turn starts at %q, want the remembered none", got)
	}

	// A different endpoint is unaffected by what we learned about this one.
	other := &urlVerdictProvider{verdictProvider{id: "openai", url: "https://other.example/v1"}}
	if got := newCacheObserver(database, other, "test-model").Verdict(); got != provider.CacheUnknown {
		t.Errorf("unrelated endpoint = %q, want unknown", got)
	}
}

func TestUnknownVerdictIsNeverMemoized(t *testing.T) {
	resetCacheVerdicts()
	t.Cleanup(resetCacheVerdicts)
	database := testDB(t)

	p := &urlVerdictProvider{verdictProvider{id: "openai", url: "https://gateway.example/v1"}}
	// A turn that ends before the observer resolves (short turn, error, abort)
	// must leave the endpoint open for the next turn to investigate.
	rememberCacheVerdict(database, p, "test-model", provider.CacheUnknown)
	if got := cacheVerdictFor(database, p, "test-model"); got != provider.CacheUnknown {
		t.Errorf("after storing unknown = %q, want unknown", got)
	}
}

func TestStaticVerdictNeedsNoObservationWindow(t *testing.T) {
	resetCacheVerdicts()
	t.Cleanup(resetCacheVerdicts)
	database := testDB(t)

	// Ollama is the case this whole mechanism exists for: paid, no prefix
	// caching, decided on identity alone with no observation window spent.
	cloud := &urlVerdictProvider{verdictProvider{id: "ollama", url: "https://ollama.com/v1"}}
	if got := newCacheObserver(database, cloud, "qwen3-coder:cloud").Verdict(); got != provider.CacheAbsent {
		t.Errorf("ollama cloud = %q, want none immediately", got)
	}

	// And through a localhost instance, which forwards cloud-marked models to
	// the hosted backend and bills for them. This is the configuration that
	// shipped broken: the URL is local, the inference is not.
	proxied := &urlVerdictProvider{verdictProvider{id: "ollama", url: "http://localhost:11434/v1"}}
	if got := newCacheObserver(database, proxied, "glm-5.2:cloud").Verdict(); got != provider.CacheAbsent {
		t.Errorf("cloud model via localhost ollama = %q, want none — it is billed inference", got)
	}
}

func TestStaticRuleOutranksAStalePersistedVerdict(t *testing.T) {
	resetCacheVerdicts()
	t.Cleanup(resetCacheVerdicts)
	database := testDB(t)

	// A row written by an older build that believed localhost Ollama cached.
	// The rule has since changed; the row must not pin this install to the old
	// answer, or the fix would never reach anyone who had already run a session.
	p := &urlVerdictProvider{verdictProvider{id: "ollama", url: "http://localhost:11434/v1"}}
	if err := session.SetModelCacheSupport(database, "glm-5.2:cloud", endpointKey(p), string(provider.CacheSupported), session.Now()); err != nil {
		t.Fatalf("seed stale row: %v", err)
	}

	if got := newCacheObserver(database, p, "glm-5.2:cloud").Verdict(); got != provider.CacheAbsent {
		t.Errorf("verdict = %q, want the current rule (none) to win over the stale row", got)
	}
}

func TestVerdictSurvivesAProcessRestart(t *testing.T) {
	resetCacheVerdicts()
	t.Cleanup(resetCacheVerdicts)
	database := testDB(t)

	p := &urlVerdictProvider{verdictProvider{id: "openai", url: "https://gateway.example/v1"}}
	const model = "some-model"

	// One run of the binary spends the observation window and resolves the pair.
	obs := newCacheObserver(database, p, model)
	for i := 0; i < 3; i++ {
		obs.Observe(0, 0, true)
	}
	rememberCacheVerdict(database, p, model, obs.Verdict())

	// A later run starts with an empty in-process memo but the same database.
	// Dropping the memo is what makes this a restart rather than a second turn.
	resetCacheVerdicts()

	if got := newCacheObserver(database, p, model).Verdict(); got != provider.CacheAbsent {
		t.Errorf("after restart = %q, want the persisted none", got)
	}
}

func TestVerdictIsKeyedByModelAndEndpointTogether(t *testing.T) {
	resetCacheVerdicts()
	t.Cleanup(resetCacheVerdicts)
	database := testDB(t)

	// One model id served by two gateways that disagree. Uses the openai slot
	// because only observed verdicts are persisted — a slot answered by a static
	// rule never reaches the store.
	const model = "some-model"
	a := &urlVerdictProvider{verdictProvider{id: "openai", url: "https://a.example/v1"}}
	b := &urlVerdictProvider{verdictProvider{id: "openai", url: "https://b.example/v1"}}

	rememberCacheVerdict(database, a, model, provider.CacheSupported)
	rememberCacheVerdict(database, b, model, provider.CacheAbsent)
	resetCacheVerdicts()

	if got := cacheVerdictFor(database, a, model); got != provider.CacheSupported {
		t.Errorf("gateway a %s = %q, want caching", model, got)
	}
	if got := cacheVerdictFor(database, b, model); got != provider.CacheAbsent {
		t.Errorf("gateway b %s = %q, want none", model, got)
	}

	// And two models on one endpoint resolve independently — OpenRouter proxies
	// a different upstream per model, so one verdict must not answer for another.
	gw := &urlVerdictProvider{verdictProvider{id: "openrouter", url: "https://openrouter.ai/api/v1"}}
	rememberCacheVerdict(database, gw, "anthropic/claude-sonnet-4", provider.CacheSupported)
	resetCacheVerdicts()
	if got := cacheVerdictFor(database, gw, "meta-llama/llama-3.1"); got != provider.CacheUnknown {
		t.Errorf("unresolved sibling model = %q, want unknown", got)
	}
}

func TestNilDatabaseFallsBackToObservation(t *testing.T) {
	resetCacheVerdicts()
	t.Cleanup(resetCacheVerdicts)

	// The CLI paths construct a LoopRunner without a store. Persistence is a
	// nicety there; refusing to run is not acceptable.
	p := &urlVerdictProvider{verdictProvider{id: "openai", url: "https://gateway.example/v1"}}
	obs := newCacheObserver(nil, p, "some-model")
	if got := obs.Verdict(); got != provider.CacheUnknown {
		t.Fatalf("nil db = %q, want unknown so observation still runs", got)
	}
	for i := 0; i < 3; i++ {
		obs.Observe(0, 0, true)
	}
	if got := obs.Verdict(); got != provider.CacheAbsent {
		t.Errorf("nil db observation = %q, want none", got)
	}
	rememberCacheVerdict(nil, p, "some-model", provider.CacheAbsent) // must not panic
}
