package provider

import (
	"context"
	"sync"
	"testing"
)

// fakeProvider is a minimal Provider for exercising the Registry.
type fakeProvider struct {
	id     string
	models []ModelInfo
}

func (f *fakeProvider) ID() string          { return f.id }
func (f *fakeProvider) Models() []ModelInfo { return f.models }
func (f *fakeProvider) StreamChat(ctx context.Context, req StreamRequest) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent)
	close(ch)
	return ch, nil
}

func newFake(id string, modelIDs ...string) *fakeProvider {
	var ms []ModelInfo
	for _, m := range modelIDs {
		ms = append(ms, ModelInfo{ID: m, ProviderID: id})
	}
	return &fakeProvider{id: id, models: ms}
}

// newFakeActive builds a provider whose models are all marked ActiveByDefault,
// the way the OGX provider marks every model its plan grants.
func newFakeActive(id string, modelIDs ...string) *fakeProvider {
	f := newFake(id, modelIDs...)
	for i := range f.models {
		f.models[i].ActiveByDefault = true
	}
	return f
}

// TestRegistryResolveProviderIsDeterministic pins the fix for the bug where a
// model id served by two providers resolved by whichever the map handed back
// first — the same prompt landing on a metered plan one run and an out-of-credit
// third-party endpoint the next. `glm-5.3-flash` is served by both OGX (active by
// default) and an OpenAI-compatible Z.ai endpoint (not), so the active one must
// win every time. The loop is what makes map randomness fail it.
func TestRegistryResolveProviderIsDeterministic(t *testing.T) {
	r := NewRegistry()
	// openai outranks ogx in ProviderPriority, so a naive priority walk would
	// pick Z.ai — ActiveByDefault is what has to decide.
	r.Register(newFake("openai", "glm-5.3-flash"))
	r.Register(newFakeActive("ogx", "glm-5.3-flash"))
	for i := 0; i < 200; i++ {
		got := r.ResolveProvider("glm-5.3-flash")
		if got == nil || got.ID() != "ogx" {
			t.Fatalf("iteration %d: ResolveProvider = %v, want the active-by-default ogx", i, got)
		}
	}
}

// TestRegistryResolveProviderFallsBackToPriority covers the ambiguous case with
// no ActiveByDefault winner: two providers list the id, neither claims it by
// default, and the tie must still resolve the same way every run — by the
// ProviderPriority walk, not by map order.
func TestRegistryResolveProviderFallsBackToPriority(t *testing.T) {
	r := NewRegistry()
	r.Register(newFake("ogx", "shared-model"))
	r.Register(newFake("openai", "shared-model"))
	for i := 0; i < 200; i++ {
		got := r.ResolveProvider("shared-model")
		if got == nil || got.ID() != "openai" {
			t.Fatalf("iteration %d: ResolveProvider = %v, want openai (higher priority)", i, got)
		}
	}
}

// TestRegistryResolveProviderForHonoursTheExplicitProvider pins the primary
// fix: a recorded provider is authoritative even when it does not outrank the
// other provider serving the same id. A session that chose OGX must keep
// running on OGX regardless of ProviderPriority.
func TestRegistryResolveProviderForHonoursTheExplicitProvider(t *testing.T) {
	r := NewRegistry()
	r.Register(newFake("openai", "glm-5.3-flash"))
	r.Register(newFake("ogx", "glm-5.3-flash"))
	for i := 0; i < 200; i++ {
		got := r.ResolveProviderFor("glm-5.3-flash", "ogx")
		if got == nil || got.ID() != "ogx" {
			t.Fatalf("iteration %d: ResolveProviderFor(ogx) = %v, want ogx", i, got)
		}
	}
}

// TestRegistryResolveProviderForFallsThroughAnUnregisteredProvider pins that a
// provider id whose provider has since been removed does not make every stored
// session unresolvable — resolution falls back to the model-id walk.
func TestRegistryResolveProviderForFallsThroughAnUnregisteredProvider(t *testing.T) {
	r := NewRegistry()
	r.Register(newFakeActive("ogx", "glm-5.3-flash"))
	got := r.ResolveProviderFor("glm-5.3-flash", "zai-gone")
	if got == nil || got.ID() != "ogx" {
		t.Fatalf("ResolveProviderFor(unregistered) = %v, want the ogx fallback", got)
	}
}

// TestRegistryModelLookupAgreesAcrossCapabilityReaders pins that the three
// capability readers return the SAME provider's values, so image support, the
// context window and the output ceiling cannot disagree about which provider a
// model came from — the deterministic walk is shared, not re-implemented.
func TestRegistryModelLookupAgreesAcrossCapabilityReaders(t *testing.T) {
	// An id the built-in catalogue does not know, so the providers' listings
	// are the only source (a catalogued id is answered by the catalogue).
	const model = "acme-coder-1"
	r := NewRegistry()
	r.Register(newFake("openai", model))
	r.Register(activeWithCapabilities("ogx", model))
	for i := 0; i < 100; i++ {
		if got := r.ContextWindow(model); got != 128000 {
			t.Fatalf("iteration %d: ContextWindow = %d, want the active provider's 128000", i, got)
		}
		if r.ModelSupportsImages(model) {
			t.Fatalf("iteration %d: ModelSupportsImages must read the active provider's value", i)
		}
		if got := r.MaxOutputTokens(model); got != 8192 {
			t.Fatalf("iteration %d: MaxOutputTokens = %d, want the active provider's 8192", i, got)
		}
	}
}

// activeWithCapabilities is a provider whose one model is ActiveByDefault and
// carries capability metadata, so the capability readers have an unambiguous
// value to return when two providers serve the id.
func activeWithCapabilities(id, modelID string) *fakeProvider {
	return &fakeProvider{id: id, models: []ModelInfo{{
		ID: modelID, ProviderID: id, ActiveByDefault: true,
		ContextWindow: 128000, MaxOutputTokens: 8192,
	}}}
}

func TestRegistryReplaceProviders(t *testing.T) {
	r := NewRegistry()
	r.Register(newFake("openai", "gpt-x"))
	if r.Get("openai") == nil {
		t.Fatal("expected openai registered")
	}
	if r.Get("anthropic") != nil {
		t.Fatal("anthropic should not be registered yet")
	}

	// Swap in a new set that adds anthropic and drops openai — the hot-reload path.
	r.ReplaceProviders(map[string]Provider{
		"anthropic": newFake("anthropic", "claude-x"),
	})
	if r.Get("anthropic") == nil {
		t.Fatal("expected anthropic after replace")
	}
	if r.Get("openai") != nil {
		t.Fatal("openai should be gone after replace")
	}
	if got := r.ResolveProvider("claude-x"); got == nil || got.ID() != "anthropic" {
		t.Fatalf("ResolveProvider(claude-x) = %v, want anthropic", got)
	}
}

func TestRegistryReplacePreservesCustomModels(t *testing.T) {
	r := NewRegistry()
	r.Register(newFake("anthropic", "claude-x"))
	r.RegisterCustomModel("my-custom", "anthropic")

	// Replacing providers must not drop custom-model routing.
	r.ReplaceProviders(map[string]Provider{
		"anthropic": newFake("anthropic", "claude-x"),
	})
	if got := r.ResolveProvider("my-custom"); got == nil || got.ID() != "anthropic" {
		t.Fatalf("custom model routing lost after replace: got %v", got)
	}
}

func TestRegistryDefaultPriority(t *testing.T) {
	r := NewRegistry()
	if r.Default() != nil {
		t.Fatal("empty registry should have a nil default")
	}
	r.Register(newFake("ollama", "llama"))
	r.Register(newFake("openai", "gpt-x"))
	// anthropic outranks both but isn't registered; openai outranks ollama.
	if got := r.Default(); got == nil || got.ID() != "openai" {
		t.Fatalf("Default() = %v, want openai", got)
	}
	r.Register(newFake("anthropic", "claude-x"))
	if got := r.Default(); got == nil || got.ID() != "anthropic" {
		t.Fatalf("Default() = %v, want anthropic", got)
	}
}

// TestRegistryOGXPriority pins ogx's seat in ProviderPriority: it is in the
// list, so it outranks any provider that is not, while a local Ollama daemon —
// which costs nothing per token — keeps its seat ahead of a metered plan.
func TestRegistryOGXPriority(t *testing.T) {
	r := NewRegistry()
	r.Register(newFake("deepseek", "deepseek-chat"))
	r.Register(newFake("ogx", "glm-5.3-flash"))
	if got := r.Default(); got == nil || got.ID() != "ogx" {
		t.Fatalf("Default() = %v, want ogx to outrank an unlisted provider", got)
	}
	r.Register(newFake("ollama", "llama"))
	if got := r.Default(); got == nil || got.ID() != "ollama" {
		t.Fatalf("Default() = %v, want ollama ahead of ogx", got)
	}
}

// TestRegistryConcurrentReplaceAndRead drives ReplaceProviders against the
// lock-protected read paths concurrently. Run with -race, this fails if the
// providers map is accessed without synchronization.
// mustOllamaProvider builds a concrete ollama provider for DefaultUsable tests.
// baseURL points the daemon probe wherever the subtest needs (live stub or dead
// port). Type-checks the result so a change in NewProviderWithConfig's ollama
// branch fails loudly here instead of silently changing DefaultUsable semantics.
func mustOllamaProvider(t *testing.T, baseURL string) *OpenAIProvider {
	t.Helper()
	p, err := NewProviderWithConfig("ollama", "", baseURL)
	if err != nil {
		t.Fatalf("ollama provider: %v", err)
	}
	op, ok := p.(*OpenAIProvider)
	if !ok {
		t.Fatalf("ollama provider is %T, want *OpenAIProvider", p)
	}
	return op
}

// TestRegistryDefaultUsable pins the default-provider contract: an
// installed-but-stopped Ollama must not shadow usable providers behind it in
// the priority walk, while a live daemon (or OLLAMA_API_KEY) keeps its seat.
func TestRegistryDefaultUsable(t *testing.T) {
	// Clear the key so "usable" is decided purely by the daemon probe; each
	// subtest re-establishes whatever it needs.
	t.Setenv("OLLAMA_API_KEY", "")

	t.Run("down ollama yields to later priority provider", func(t *testing.T) {
		r := NewRegistry()
		r.Register(mustOllamaProvider(t, deadURL))
		r.Register(newFake("ogx", "glm-5.3-flash"))
		got := r.DefaultUsable()
		if got == nil || got.ID() != "ogx" {
			t.Fatalf("DefaultUsable() = %v, want ogx (stopped ollama must not shadow a priority provider)", got)
		}
	})

	t.Run("down ollama yields to non-priority registered provider", func(t *testing.T) {
		r := NewRegistry()
		r.Register(mustOllamaProvider(t, deadURL))
		r.Register(newFake("custom-llama", "x/y"))
		got := r.DefaultUsable()
		if got == nil || got.ID() != "custom-llama" {
			t.Fatalf("DefaultUsable() = %v, want custom-llama", got)
		}
	})

	t.Run("live ollama keeps default", func(t *testing.T) {
		r := NewRegistry()
		r.Register(mustOllamaProvider(t, liveOllama(t)+"/v1"))
		r.Register(newFake("ogx", "glm-5.3-flash"))
		got := r.DefaultUsable()
		if got == nil || got.ID() != "ollama" {
			t.Fatalf("DefaultUsable() = %v, want ollama (daemon answers, it stays default)", got)
		}
	})

	t.Run("ollama with API key stays default even when daemon is down", func(t *testing.T) {
		t.Setenv("OLLAMA_API_KEY", "test-key")
		r := NewRegistry()
		r.Register(mustOllamaProvider(t, deadURL))
		r.Register(newFake("ogx", "glm-5.3-flash"))
		got := r.DefaultUsable()
		if got == nil || got.ID() != "ollama" {
			t.Fatalf("DefaultUsable() = %v, want ollama (keyed remote endpoint counts as usable)", got)
		}
	})

	t.Run("lone down ollama is still returned", func(t *testing.T) {
		r := NewRegistry()
		r.Register(mustOllamaProvider(t, deadURL))
		got := r.DefaultUsable()
		if got == nil || got.ID() != "ollama" {
			t.Fatalf("DefaultUsable() = %v, want ollama (only option; provider surfaces the error itself)", got)
		}
	})

	t.Run("empty registry returns nil", func(t *testing.T) {
		r := NewRegistry()
		if got := r.DefaultUsable(); got != nil {
			t.Fatalf("DefaultUsable() = %v, want nil", got)
		}
	})
}

func TestRegistryConcurrentReplaceAndRead(t *testing.T) {
	r := NewRegistry()
	r.Register(newFake("anthropic", "claude-x"))
	r.RegisterCustomModel("custom-x", "anthropic")

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writer: continuously swap the provider set.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			r.ReplaceProviders(map[string]Provider{
				"anthropic": newFake("anthropic", "claude-x"),
				"openai":    newFake("openai", "gpt-x"),
			})
		}
	}()

	// Readers: hammer every lock-protected accessor.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = r.Get("anthropic")
				_ = r.List()
				_ = r.ListModels()
				_ = r.ResolveProvider("gpt-x")
				_ = r.ResolveProvider("custom-x")
				_ = r.ResolveProviderFor("claude-x", "anthropic")
				_ = r.Default()
				_ = r.ModelSupportsImages("claude-x")
			}
		}()
	}

	// Bound the run by work, not wall-clock.
	for i := 0; i < 2000; i++ {
		_ = r.Default()
	}
	close(stop)
	wg.Wait()
}

// ServingProvider names only a provider that actually serves the model — a
// custom routing or a listing — and nil otherwise, where ResolveProvider would
// fall back to the highest-priority provider.
func TestServingProviderNeverGuesses(t *testing.T) {
	r := NewRegistry()
	r.Register(newFake("anthropic", "claude-x"))
	r.Register(newFake("ollama", "glm-5.3-flash:cloud"))
	if p := r.ServingProvider("glm-5.3-flash:cloud"); p == nil || p.ID() != "ollama" {
		t.Errorf("listed model: got %v, want ollama", p)
	}
	r.RegisterCustomModel("my-router-model", "ollama")
	if p := r.ServingProvider("my-router-model"); p == nil || p.ID() != "ollama" {
		t.Errorf("custom model: got %v, want ollama", p)
	}
	if p := r.ServingProvider("nobody-serves-this"); p != nil {
		t.Errorf("unserved model: got %s, want nil", p.ID())
	}
	if p := r.ResolveProvider("nobody-serves-this"); p == nil {
		t.Error("ResolveProvider should still fall back for routing; the difference is the point")
	}
}

// TestRegistryUserConfiguredCustomModel pins what the capability readers answer
// for a model the user configured by hand: the user's figures are used as given,
// the context window included — a host listing never caps or raises a window the
// user set. Nothing is invented for a field the user left empty, and the
// listing still answers for one they did not configure.
func TestRegistryUserConfiguredCustomModel(t *testing.T) {
	const model = "acme-coder-user"
	tests := []struct {
		name       string
		info       CustomModelInfo
		listed     int // the serving host's listed window; 0 = it does not list the model
		listedMax  int
		wantWindow int
		wantOutput int
		wantImages bool
	}{
		{
			name:       "user figures answer when nothing else knows the model",
			info:       CustomModelInfo{ContextWindow: 200000, MaxOutputTokens: 32000, SupportsImages: true},
			wantWindow: 200000, wantOutput: 32000, wantImages: true,
		},
		{
			name:   "the user's window wins over a smaller host listing",
			info:   CustomModelInfo{ContextWindow: 400000},
			listed: 128000, listedMax: 4096,
			wantWindow: 400000, wantOutput: 4096,
		},
		{
			name:       "a larger host listing does not raise the user's window",
			info:       CustomModelInfo{ContextWindow: 128000},
			listed:     200000,
			wantWindow: 128000,
		},
		{
			name:   "user max output wins over the host listing",
			info:   CustomModelInfo{MaxOutputTokens: 32000},
			listed: 128000, listedMax: 4096,
			wantWindow: 128000, wantOutput: 32000,
		},
		{
			name:   "nothing configured falls through to the host listing",
			listed: 64000, listedMax: 8192,
			wantWindow: 64000, wantOutput: 8192,
		},
		{
			name: "nothing configured and nothing known is zero",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			var models []ModelInfo
			if tt.listed > 0 {
				models = []ModelInfo{{ID: model, ProviderID: "openrouter", ContextWindow: tt.listed, MaxOutputTokens: tt.listedMax}}
			}
			r.Register(&fakeProvider{id: "openrouter", models: models})
			info := tt.info
			info.ProviderID = "openrouter"
			r.RegisterCustomModelInfo(model, info)

			if got := r.ContextWindow(model); got != tt.wantWindow {
				t.Errorf("ContextWindow = %d, want %d", got, tt.wantWindow)
			}
			if got := r.MaxOutputTokens(model); got != tt.wantOutput {
				t.Errorf("MaxOutputTokens = %d, want %d", got, tt.wantOutput)
			}
			if got := r.ModelSupportsImages(model); got != tt.wantImages {
				t.Errorf("ModelSupportsImages = %v, want %v", got, tt.wantImages)
			}
		})
	}
}

// TestSetModelOverrideLayersOnAListedModel pins the edit path for a model the
// catalogue or a provider already lists: what the user configured answers as
// their own figures, but the model itself stays listed — IsCustomModel is
// false, so its routing, its collection and its catalogue name are untouched.
// A record that configures nothing is no override at all, which is how an
// editor that clears every field falls back to what the listing says.
func TestSetModelOverrideLayersOnAListedModel(t *testing.T) {
	const model = "acme-listed"
	r := NewRegistry()
	r.Register(&fakeProvider{id: "openrouter", models: []ModelInfo{{
		ID: model, ProviderID: "openrouter", ContextWindow: 64000, MaxOutputTokens: 4096,
	}}})

	if _, ok := r.ModelOverride(model, "openrouter"); ok {
		t.Fatal("a listed model starts with no override")
	}
	r.SetModelOverride(model, CustomModelInfo{
		ProviderID: "openrouter", ContextWindow: 200000,
		MaxOutputTokens: 32000, SupportsImages: true,
	})

	if r.IsCustomModel(model) {
		t.Fatal("an override must not mark a listed model custom")
	}
	if got := r.ContextWindow(model); got != 200000 {
		t.Errorf("ContextWindow = %d, want the user's 200000", got)
	}
	if got := r.MaxOutputTokens(model); got != 32000 {
		t.Errorf("MaxOutputTokens = %d, want the user's 32000", got)
	}
	if !r.ModelSupportsImages(model) {
		t.Error("ModelSupportsImages must honour the user's yes")
	}
	if info, ok := r.ModelOverride(model, "openrouter"); !ok || info.ContextWindow != 200000 {
		t.Errorf("ModelOverride = %+v, %v; want the user's record", info, ok)
	}

	// Clearing every field clears the entry: the listed figures answer again.
	r.SetModelOverride(model, CustomModelInfo{ProviderID: "openrouter"})
	if _, ok := r.ModelOverride(model, "openrouter"); ok {
		t.Fatal("an override configuring nothing must be dropped")
	}
	if got := r.ContextWindow(model); got != 64000 {
		t.Errorf("ContextWindow after clearing = %d, want the listed 64000", got)
	}
	if got := r.MaxOutputTokens(model); got != 4096 {
		t.Errorf("MaxOutputTokens after clearing = %d, want the listed 4096", got)
	}
	if r.ModelSupportsImages(model) {
		t.Error("an images override must not survive its own removal")
	}
}

// TestRegisterCustomModelStillMarksAModelCustom pins the predating signature:
// callers that only know the provider still mark the model custom, and reading
// it back yields that provider with every capability left unset, so catalogue
// and listing facts still answer for them.
func TestRegisterCustomModelStillMarksAModelCustom(t *testing.T) {
	r := NewRegistry()
	r.Register(newFake("anthropic", "claude-x"))
	r.RegisterCustomModel("my-custom", "anthropic")

	if !r.IsCustomModel("my-custom") {
		t.Fatal("RegisterCustomModel must still mark the model custom")
	}
	info, ok := r.CustomModel("my-custom")
	if !ok || info.ProviderID != "anthropic" {
		t.Fatalf("CustomModel = %+v, %v; want the anthropic record", info, ok)
	}
	if info.ContextWindow != 0 || info.MaxOutputTokens != 0 || info.InputPricePerM != 0 || info.OutputPricePerM != 0 || info.SupportsImages {
		t.Errorf("CustomModel = %+v, want every capability unset", info)
	}
	if r.IsCustomModel("claude-x") {
		t.Error("a catalogued model is not custom")
	}
	if _, ok := r.CustomModel("claude-x"); ok {
		t.Error("CustomModel must not answer for a model nobody registered")
	}
}
