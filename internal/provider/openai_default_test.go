package provider

import "testing"

func TestResolveDefaultModel(t *testing.T) {
	list := []ModelInfo{
		{ID: "disabled:1"},
		{ID: "enabled:1", ActiveByDefault: true},
	}

	t.Run("explicit configuration is never overridden", func(t *testing.T) {
		p := &OpenAIProvider{model: "qwen3", modelExplicit: true}
		p.resolveDefaultModel(list)
		if got := p.defaultModel(); got != "qwen3" {
			t.Errorf("got %q, want the explicitly configured qwen3", got)
		}
	})

	t.Run("guess not served falls to first enabled", func(t *testing.T) {
		p := &OpenAIProvider{model: "qwen3"}
		p.resolveDefaultModel(list)
		if got := p.defaultModel(); got != "enabled:1" {
			t.Errorf("got %q, want enabled:1", got)
		}
	})

	t.Run("guess that is served is kept", func(t *testing.T) {
		p := &OpenAIProvider{model: "disabled:1"}
		p.resolveDefaultModel(list)
		if got := p.defaultModel(); got != "disabled:1" {
			t.Errorf("got %q, want the configured model kept", got)
		}
	})

	t.Run("nothing enabled falls to first model", func(t *testing.T) {
		p := &OpenAIProvider{model: "qwen3"}
		p.resolveDefaultModel([]ModelInfo{{ID: "only:1"}})
		if got := p.defaultModel(); got != "only:1" {
			t.Errorf("got %q, want only:1", got)
		}
	})

	t.Run("empty list keeps the placeholder", func(t *testing.T) {
		p := &OpenAIProvider{model: "qwen3"}
		p.resolveDefaultModel(nil)
		if got := p.defaultModel(); got != "qwen3" {
			t.Errorf("got %q, want the placeholder retained", got)
		}
	})
}
