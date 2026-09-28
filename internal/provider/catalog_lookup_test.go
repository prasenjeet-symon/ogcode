package provider

import (
	"context"
	"testing"
)

func TestNormalizeModelIDFoldsHostSpellings(t *testing.T) {
	for in, want := range map[string]string{
		"claude-sonnet-4-5-20250929":        "claude-sonnet-4-5-20250929",
		"anthropic/claude-sonnet-4.5":       "claude-sonnet-4-5",
		"Anthropic/Claude-Sonnet-4.5":       "claude-sonnet-4-5",
		"openai/gpt-oss-20b":                "gpt-oss-20b",
		"gpt-oss:20b":                       "gpt-oss-20b",
		"gpt-oss:120b-cloud":                "gpt-oss-120b",
		"glm-4.6:cloud":                     "glm-4-6",
		"llama3.2:latest":                   "llama3-2",
		"deepseek/deepseek-chat-v3.1:free":  "deepseek-chat-v3-1",
		"moonshotai/kimi-k2:thinking":       "kimi-k2",
		"deepseek-ai/DeepSeek-V3.1":         "deepseek-v3-1",
		"meta-llama/Llama-3.3-70B-Instruct": "llama-3-3-70b-instruct",
		"  gpt-4.1  ":                       "gpt-4-1",
		"qwen3_coder":                       "qwen3-coder",
		"":                                  "",
	} {
		if got := normalizeModelID(in); got != want {
			t.Errorf("normalizeModelID(%q) = %q, want %q", in, got, want)
		}
	}
}

var fixtureCatalog = []CatalogModel{
	{ID: "claude-sonnet-4-5-20250929", Aliases: []string{"claude-sonnet-4-5"}, ContextWindow: 200_000},
	{ID: "gpt-oss-20b", Aliases: []string{"gpt-oss:20b"}, ContextWindow: 131_072},
	{ID: "gpt-oss-120b", ContextWindow: 131_072},
	{ID: "deepseek-v3.1", Aliases: []string{"deepseek-ai/DeepSeek-V3.1", "deepseek-v3.1:671b"}, ContextWindow: 128_000},
}

func TestCatalogIndexFindsEverySpelling(t *testing.T) {
	idx := newCatalogIndex(fixtureCatalog)
	for in, wantID := range map[string]string{
		"claude-sonnet-4-5-20250929":       "claude-sonnet-4-5-20250929",
		"claude-sonnet-4-5":                "claude-sonnet-4-5-20250929", // undated alias
		"anthropic/claude-sonnet-4.5":      "claude-sonnet-4-5-20250929", // OpenRouter slug
		"claude-sonnet-4-5@20250929":       "",                           // unknown shape: not guessed
		"groq/openai/gpt-oss-20b":          "gpt-oss-20b",
		"gpt-oss:20b":                      "gpt-oss-20b", // Ollama tag
		"gpt-oss:120b-cloud":               "gpt-oss-120b",
		"deepseek-v3.1:671b-cloud":         "deepseek-v3.1",
		"DeepSeek-V3.1":                    "deepseek-v3.1",
		"deepseek/deepseek-v3.1:free":      "deepseek-v3.1",
		"some-vendor/unknown-model":        "",
		"gpt-oss-20b-2025-08-05":           "gpt-oss-20b", // dated snapshot of an undated entry
		"claude-sonnet-4-5-20991231-extra": "",
	} {
		m, ok := idx.lookup(in)
		if wantID == "" {
			if ok {
				t.Errorf("lookup(%q) = %q, want no match", in, m.ID)
			}
			continue
		}
		if !ok || m.ID != wantID {
			t.Errorf("lookup(%q) = %q (found=%v), want %q", in, m.ID, ok, wantID)
		}
	}
}

// Earlier lists win a contested key, so a first-party catalogue entry is never
// shadowed by an open-model alias that happens to normalise the same way.
func TestCatalogIndexEarlierListWins(t *testing.T) {
	first := []CatalogModel{{ID: "gpt-oss-20b", Name: "first-party"}}
	second := []CatalogModel{{ID: "gpt-oss:20b", Name: "open"}}
	m, ok := newCatalogIndex(first, second).lookup("gpt-oss-20b")
	if !ok || m.Name != "first-party" {
		t.Errorf("got %q, want the earlier list's entry", m.Name)
	}
}

// The real catalogue must be unambiguous: two different entries claiming the
// same normalised id would make the answer depend on list order by accident.
func TestCatalogHasNoAmbiguousIDs(t *testing.T) {
	owner := map[string]string{}
	for _, list := range [][]CatalogModel{AnthropicModels, OpenAIModels, OpenModels, LegacyModels} {
		for _, m := range list {
			for _, key := range append([]string{m.ID}, m.Aliases...) {
				k := normalizeModelID(key)
				if prev, ok := owner[k]; ok && prev != m.ID {
					t.Errorf("%q (from %q) is claimed by both %q and %q", k, key, prev, m.ID)
				}
				owner[k] = m.ID
			}
		}
	}
}

// Every catalogued model states the facts the app relies on. A window of 0
// would read as "unknown" and silently disable the catalogue for that model.
func TestCatalogEntriesAreComplete(t *testing.T) {
	for _, list := range [][]CatalogModel{AnthropicModels, OpenAIModels, OpenModels, LegacyModels} {
		for _, m := range list {
			if m.Name == "" || m.ContextWindow <= 0 {
				t.Errorf("%s: want a name and a context window, got %q / %d", m.ID, m.Name, m.ContextWindow)
			}
			if m.MaxOutputTokens > m.ContextWindow {
				t.Errorf("%s: max output %d exceeds the window %d", m.ID, m.MaxOutputTokens, m.ContextWindow)
			}
		}
	}
}

// windowProvider lists one model with whatever window its endpoint reported.
type windowProvider struct {
	id     string
	models []ModelInfo
}

func (p windowProvider) ID() string          { return p.id }
func (p windowProvider) Models() []ModelInfo { return p.models }
func (p windowProvider) StreamChat(context.Context, StreamRequest) (<-chan StreamEvent, error) {
	return nil, nil
}

// The catalogue answers for a known model whichever host serves it and under
// whatever id — including an id no provider lists (a model a user added by
// hand). A host's own figure fills in for models the catalogue does not know
// and lowers the catalogue's when the host serves less, but never raises it.
func TestRegistryContextWindowTakesTheSmallerKnownFigure(t *testing.T) {
	cm := AnthropicModels[0]
	reg := NewRegistry()
	reg.Register(windowProvider{id: "openrouter", models: []ModelInfo{
		{ID: "anthropic/" + cm.ID, ContextWindow: cm.ContextWindow * 2},
		{ID: "anthropic/" + cm.ID + ":nitro", ContextWindow: 12_345},
		{ID: "vendor/uncatalogued-model", ContextWindow: 64_000},
	}})
	if got := reg.ContextWindow("anthropic/" + cm.ID); got != cm.ContextWindow {
		t.Errorf("host claiming more than the vendor: window = %d, want the catalogue's %d", got, cm.ContextWindow)
	}
	if got := reg.ContextWindow("anthropic/" + cm.ID + ":nitro"); got != 12_345 {
		t.Errorf("host serving less than the vendor: window = %d, want the host's 12345", got)
	}
	if got := reg.ContextWindow(cm.ID); got != cm.ContextWindow {
		t.Errorf("catalogued id no provider lists: window = %d, want %d", got, cm.ContextWindow)
	}
	if got := reg.ContextWindow("vendor/uncatalogued-model"); got != 64_000 {
		t.Errorf("uncatalogued model: window = %d, want the provider's 64000", got)
	}
	if got := reg.ContextWindow("vendor/nobody-knows"); got != 0 {
		t.Errorf("unknown model: window = %d, want 0", got)
	}
}

// The spellings real hosts use reach the right entry, and the ones that mean
// different models on different hosts reach none rather than a wrong one.
func TestCatalogResolvesRealHostSpellings(t *testing.T) {
	for in, wantID := range map[string]string{
		// OGX plan ids, and the Ollama cloud names they relay to.
		"deepseek-v4.1-flash":        "deepseek-flash",
		"glm-5.3-flash":              "glm-5.3-flash",
		"glm-5.3-flash:cloud":        "glm-5.3-flash",
		"kimi-k3:cloud":              "kimi-k3",
		"deepseek-v4-pro:0813-cloud": "deepseek-v4-pro",
		"minimax-m3:cloud":           "MiniMax-M3",
		"gpt-oss:120b-cloud":         "gpt-oss-120b",
		// OpenRouter slugs, variants and dated canonical slugs.
		"z-ai/glm-5.3":                          "glm-5.3",
		"moonshotai/kimi-k2.7-code":             "kimi-k2.7-code",
		"qwen/qwen3.8-27b:free":                 "qwen3.8-27b",
		"qwen/qwen3-coder":                      "qwen3-coder-480b-a35b-instruct",
		"minimax/minimax-m2.7":                  "MiniMax-M2.7",
		"mistralai/mistral-medium-3.5-20260430": "mistral-medium-3-5",
		"nvidia/nemotron-3.5-lightning":         "nvidia/nemotron-3.5-lightning-30b-a3b",
		"anthropic/claude-sonnet-5:batch":       "claude-sonnet-5",
		"openai/gpt-6-sol-pro":                  "gpt-6-sol",
		"openai/o4-mini-high":                   "o4-mini",
		// Groq, Hugging Face, Together, Fireworks, NVIDIA.
		"llama-3.3-70b-versatile":                 "meta-llama/Llama-3.3-70B-Instruct",
		"openai/gpt-oss-20b":                      "gpt-oss-20b",
		"deepseek-ai/DeepSeek-V3.2":               "deepseek-ai/DeepSeek-V3.2",
		"Qwen/Qwen3-Coder-30B-A3B-Instruct":       "qwen3-coder-30b-a3b-instruct",
		"meta-llama/Llama-3.3-70B-Instruct-Turbo": "meta-llama/Llama-3.3-70B-Instruct",
		"accounts/fireworks/models/glm-5p3":       "glm-5.3",
		"nvidia/nemotron-nano-3-30b-a3b":          "nvidia/nemotron-3-nano-30b-a3b",
		// Ollama default tags name one size.
		"llama3.1:latest": "meta-llama/Llama-3.1-8B-Instruct",
		"gpt-oss:latest":  "gpt-oss-20b",
		"gemma4:latest":   "google/gemma-4-E4B-it",
		"devstral:latest": "mistralai/Devstral-Small-2507",
		"qwen3.8:latest":  "qwen3.8-27b",
		// Ambiguous across hosts: no match beats a wrong window.
		"deepseek-r1":        "",
		"deepseek-r1:latest": "",
		"deepseek-chat":      "",
		"mistral-medium-3":   "",
		"magistral:24b":      "",
	} {
		m, ok := LookupCatalogModel(in)
		if wantID == "" {
			if ok {
				t.Errorf("%q matched %q; it names different models on different hosts", in, m.ID)
			}
			continue
		}
		if !ok || m.ID != wantID {
			t.Errorf("%q = %q (found=%v), want %q", in, m.ID, ok, wantID)
		}
	}
}

// A model running on this machine's Ollama gets the window the instance
// reports, not the model's datasheet: the instance runs it at num_ctx, often
// far below the model's maximum. Its cloud tags, and every model on a remote
// Ollama, run at the full window, so the catalogue applies there.
func TestRegistryLeavesLocallyRunModelsToTheirRuntime(t *testing.T) {
	cm, ok := LookupCatalogModel("gpt-oss-120b")
	if !ok {
		t.Fatal("gpt-oss-120b is not catalogued")
	}
	local := &OpenAIProvider{id: "ollama", baseURL: "http://localhost:11434/v1"}
	local.storeCatalog([]ModelInfo{{ID: "gpt-oss:120b"}, {ID: "gpt-oss:120b-cloud"}})
	reg := NewRegistry()
	reg.Register(local)

	if got := reg.ContextWindow("gpt-oss:120b"); got != 0 {
		t.Errorf("local model: window = %d, want 0 (the runtime's, unknown)", got)
	}
	if _, ok := reg.CatalogModel("gpt-oss:120b"); ok {
		t.Error("local model: catalogue applied")
	}
	if got := reg.ContextWindow("gpt-oss:120b-cloud"); got != cm.ContextWindow {
		t.Errorf("cloud tag on a local instance: window = %d, want %d", got, cm.ContextWindow)
	}

	remote := &OpenAIProvider{id: "ollama", baseURL: "https://ollama.com/v1"}
	remote.storeCatalog([]ModelInfo{{ID: "gpt-oss:120b"}})
	reg = NewRegistry()
	reg.Register(remote)
	if got := reg.ContextWindow("gpt-oss:120b"); got != cm.ContextWindow {
		t.Errorf("remote Ollama: window = %d, want %d", got, cm.ContextWindow)
	}
}

// Every model Ollama's cloud served on 2026-09-28 (ollama.com/api/tags) is
// catalogued, as is every cloud fallback entry: an uncatalogued one would fall
// back to the fixed compaction cap however large its real window.
func TestCatalogCoversOllamaCloud(t *testing.T) {
	served := []string{
		"nemotron-3-nano:30b", "gemma4:31b", "glm-5.3-flash", "minimax-m2.7", "nemotron-3-ultra",
		"kimi-k2.7-code", "glm-5.3", "deepseek-v4-pro:0813", "kimi-k3", "deepseek-v4.1-flash",
		"gpt-oss:120b", "glm-5.2", "kimi-k2.6", "gpt-oss:20b", "mistral-large-3:675b", "minimax-m3",
		"nemotron-3-super",
	}
	for _, m := range ollamaCloudFallback {
		served = append(served, m.ID)
	}
	for _, id := range served {
		if _, ok := LookupCatalogModel(id); !ok {
			t.Errorf("%s is served by Ollama's cloud but not catalogued", id)
		}
	}
}
