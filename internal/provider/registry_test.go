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
		r.Register(newFake("ogcode-openrouter", "free/model"))
		got := r.DefaultUsable()
		if got == nil || got.ID() != "ogcode-openrouter" {
			t.Fatalf("DefaultUsable() = %v, want ogcode-openrouter (stopped ollama must not shadow the free pool)", got)
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
		r.Register(newFake("ogcode-openrouter", "free/model"))
		got := r.DefaultUsable()
		if got == nil || got.ID() != "ollama" {
			t.Fatalf("DefaultUsable() = %v, want ollama (daemon answers, it stays default)", got)
		}
	})

	t.Run("ollama with API key stays default even when daemon is down", func(t *testing.T) {
		t.Setenv("OLLAMA_API_KEY", "test-key")
		r := NewRegistry()
		r.Register(mustOllamaProvider(t, deadURL))
		r.Register(newFake("ogcode-openrouter", "free/model"))
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
