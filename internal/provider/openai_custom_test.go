package provider

import (
	"strings"
	"testing"
)

// TestCustomProviderHappyPath pins the named-provider constructor
// the config file's extra providers go through: the id and baseURL the config
// named, an inferred collection from the endpoint's host, and a provider built
// on the OpenAI plumbing that is otherwise usable.
func TestCustomProviderHappyPath(t *testing.T) {
	p, err := NewOpenAICompatibleProvider("my-deepseek", "sk-1", "https://api.deepseek.com/v1", "deepseek-chat", "")
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider: %v", err)
	}
	if p.ID() != "my-deepseek" {
		t.Errorf("ID() = %q, want %q", p.ID(), "my-deepseek")
	}
	op := p.(*OpenAIProvider)
	if op.BaseURL() != "https://api.deepseek.com/v1" {
		t.Errorf("BaseURL() = %q, want %q", op.BaseURL(), "https://api.deepseek.com/v1")
	}
	if got := op.collection; got != "DeepSeek" {
		t.Errorf("collection = %q, want %q (inferred from the host)", got, "DeepSeek")
	}
	if got := op.defaultModel(); got != "deepseek-chat" {
		t.Errorf("defaultModel() = %q, want %q", got, "deepseek-chat")
	}
	if op.modelExplicit != true {
		t.Errorf("modelExplicit = false, want true for an explicit model")
	}
	// Models() is a pure read — it may answer from the compiled-in fallback,
	// but it must not panic.
	_ = p.Models()
}

// TestCustomProviderLabelOverrides pins that an explicit label
// wins over whatever collectionFromBaseURL infers from the host.
func TestCustomProviderLabelOverrides(t *testing.T) {
	p, err := NewOpenAICompatibleProvider("my-vllm", "", "https://api.deepseek.com/v1", "deepseek-chat", "Self-hosted")
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider: %v", err)
	}
	if got := p.(*OpenAIProvider).collection; got != "Self-hosted" {
		t.Errorf("collection = %q, want %q", got, "Self-hosted")
	}
}

// TestCustomProviderModelEmptyKeepsPlaceholder pins that an empty
// model keeps NewOpenAIProvider's placeholder and stays non-explicit, so the
// endpoint's own model list can still resolve a default later.
func TestCustomProviderModelEmptyKeepsPlaceholder(t *testing.T) {
	p, err := NewOpenAICompatibleProvider("local-vllm", "", "http://localhost:8000/v1", "", "")
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider: %v", err)
	}
	op := p.(*OpenAIProvider)
	if op.model != "gpt-6-sol" {
		t.Errorf("model = %q, want the placeholder %q", op.model, "gpt-6-sol")
	}
	if op.modelExplicit {
		t.Errorf("modelExplicit = true, want false for an empty model")
	}
}

// TestCustomProviderRefusals pins every refusal: an empty id, the
// five reserved built-in slots (iterated from ProviderPriority, so a new
// built-in is reserved automatically), an id that is not a plain slug, and an
// empty baseURL — an OpenAI-compatible provider only makes sense pointed at an
// endpoint. Every refusal returns a nil provider.
func TestCustomProviderRefusals(t *testing.T) {
	cases := []struct {
		name    string
		id      string
		baseURL string
		wantErr string
	}{
		{"empty id", "", "https://api.deepseek.com/v1", "needs an id"},
		{"reserved openai", "openai", "https://api.openai.com/v1", "reserved"},
		{"reserved ogx", "ogx", "https://ogx.example.com/v1", "reserved"},
		{"reserved anthropic", "anthropic", "https://api.anthropic.com/v1", "reserved"},
		{"reserved openrouter", "openrouter", "https://openrouter.ai/api/v1", "reserved"},
		{"reserved ollama", "ollama", "http://localhost:11434/v1", "reserved"},
		{"empty baseURL", "my-deepseek", "", "baseURL"},
		{"id with space", "my deepseek", "https://api.deepseek.com/v1", "plain slug"},
		{"id with slash", "a/b", "https://api.deepseek.com/v1", "plain slug"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := NewOpenAICompatibleProvider(c.id, "sk-1", c.baseURL, "m-model", "")
			if p != nil {
				t.Errorf("provider = %v, want nil on refusal", p)
			}
			if err == nil {
				t.Fatalf("err = nil, want a refusal")
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("err = %q, want it to mention %q", err.Error(), c.wantErr)
			}
		})
	}
}
