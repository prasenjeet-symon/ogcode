package provider

import (
	"slices"
	"testing"
)

// Every catalogued ladder must be made of ogcode's own levels, lowest first,
// with the vendor default on it — the picker renders the list as given and
// marks the default, and the wire translators assume both.
func TestCatalogEffortLaddersAreWellFormed(t *testing.T) {
	lists := map[string][]CatalogModel{
		"AnthropicModels": AnthropicModels,
		"OpenAIModels":    OpenAIModels,
		"GoogleModels":    GoogleModels,
		"OpenModels":      OpenModels,
		"LegacyModels":    LegacyModels,
	}
	rank := func(level string) int { return slices.Index(effortOrder, level) }
	for name, list := range lists {
		for _, m := range list {
			if len(m.Efforts) == 0 {
				if m.DefaultEffort != "" {
					t.Errorf("%s %s: default effort %q with no levels", name, m.ID, m.DefaultEffort)
				}
				continue
			}
			for i, level := range m.Efforts {
				if !IsEffort(level) {
					t.Errorf("%s %s: unknown level %q", name, m.ID, level)
				}
				if i > 0 && rank(level) <= rank(m.Efforts[i-1]) {
					t.Errorf("%s %s: levels out of order at %q (%v)", name, m.ID, level, m.Efforts)
				}
			}
			if m.DefaultEffort != "" && !slices.Contains(m.Efforts, m.DefaultEffort) {
				t.Errorf("%s %s: default %q is not one of its levels %v", name, m.ID, m.DefaultEffort, m.Efforts)
			}
			// On/off is its own shape: a model that only switches thinking has
			// exactly those two levels, and "on" never sits on a graded ladder.
			if slices.Contains(m.Efforts, EffortOn) && !slices.Equal(m.Efforts, onOffEfforts) {
				t.Errorf("%s %s: %q belongs only to the on/off ladder, got %v", name, m.ID, EffortOn, m.Efforts)
			}
		}
	}
}

func TestClaudeEffortSpecs(t *testing.T) {
	p := &AnthropicProvider{}
	cases := []struct {
		model   string
		levels  []string
		deflt   string
		allowed string
		refused string
	}{
		// Opus 5.5 defaults to medium, one level below the rest of the line.
		{"claude-opus-5-5", claudeEfforts, EffortMedium, EffortMax, EffortNone},
		{"claude-fable-5-1", claudeEfforts, EffortHigh, EffortXHigh, EffortMinimal},
		// xhigh arrived with Opus 4.7; the 4.6 models stop below it.
		{"claude-sonnet-4-6", claude46Efforts, EffortHigh, EffortMax, EffortXHigh},
		{"claude-opus-4-5", lowToHighEfforts, EffortHigh, EffortLow, EffortMax},
		// Haiku 4.5 rejects the parameter outright.
		{"claude-haiku-4-5", nil, "", "", EffortLow},
		// A future id gets nothing rather than a guess.
		{"claude-unreleased-9", nil, "", "", EffortHigh},
		// Another vendor's model on an Anthropic-compatible host is not Claude.
		{"glm-5.3", nil, "", "", EffortHigh},
	}
	for _, c := range cases {
		spec := p.EffortSpec(c.model)
		if !slices.Equal(spec.Levels, c.levels) {
			t.Errorf("%s: levels %v, want %v", c.model, spec.Levels, c.levels)
		}
		if spec.Default != c.deflt {
			t.Errorf("%s: default %q, want %q", c.model, spec.Default, c.deflt)
		}
		if c.allowed != "" && !spec.Allows(c.allowed) {
			t.Errorf("%s: should allow %q", c.model, c.allowed)
		}
		if spec.Allows(c.refused) {
			t.Errorf("%s: must not allow %q", c.model, c.refused)
		}
	}
}

// The wire body is what matters: effort rides in output_config, only on the
// agent loop's thinking requests, and only at a level the model takes — any
// other level is a 400 that fails the turn.
func TestAnthropicEffortParameter(t *testing.T) {
	effortOf := func(body map[string]any) (string, bool) {
		oc, ok := body["output_config"].(map[string]any)
		if !ok {
			return "", false
		}
		e, _ := oc["effort"].(string)
		return e, true
	}

	t.Run("a chosen level is sent beside adaptive thinking", func(t *testing.T) {
		req := baseThinkingRequest("claude-opus-5-5")
		req.Thinking = true
		req.Effort = EffortMax
		body := captureAnthropicBody(t, req)
		if got, ok := effortOf(body); !ok || got != EffortMax {
			t.Fatalf("expected output_config.effort %q, got %v", EffortMax, body["output_config"])
		}
		if th, _ := body["thinking"].(map[string]any); th["type"] != "adaptive" {
			t.Errorf("effort must not displace adaptive thinking, got %v", body["thinking"])
		}
	})

	t.Run("no effort means no output_config", func(t *testing.T) {
		req := baseThinkingRequest("claude-opus-5-5")
		req.Thinking = true
		if _, ok := captureAnthropicBody(t, req)["output_config"]; ok {
			t.Error("an unset effort must leave the model at its default")
		}
	})

	t.Run("utility calls never carry effort", func(t *testing.T) {
		req := baseThinkingRequest("claude-opus-5-5")
		req.Effort = EffortMax // Thinking left off, as titles and compaction do
		if _, ok := captureAnthropicBody(t, req)["output_config"]; ok {
			t.Error("effort must ride only on thinking requests")
		}
	})

	t.Run("a level the model does not take is dropped", func(t *testing.T) {
		req := baseThinkingRequest("claude-opus-4-6")
		req.Thinking = true
		req.Effort = EffortXHigh
		if oc, ok := captureAnthropicBody(t, req)["output_config"]; ok {
			t.Errorf("Opus 4.6 has no xhigh; expected no output_config, got %v", oc)
		}
	})

	t.Run("a model without effort gets none", func(t *testing.T) {
		req := baseThinkingRequest("claude-haiku-4-5-20251001")
		req.Thinking = true
		req.Effort = EffortLow
		if oc, ok := captureAnthropicBody(t, req)["output_config"]; ok {
			t.Errorf("Haiku 4.5 rejects effort; expected none, got %v", oc)
		}
	})

	t.Run("Opus 4.5 takes effort without a thinking mode", func(t *testing.T) {
		req := baseThinkingRequest("claude-opus-4-5")
		req.Thinking = true
		req.Effort = EffortLow
		body := captureAnthropicBody(t, req)
		if got, ok := effortOf(body); !ok || got != EffortLow {
			t.Errorf("expected output_config.effort %q, got %v", EffortLow, body["output_config"])
		}
		if _, ok := body["thinking"]; ok {
			t.Errorf("Opus 4.5 takes only a sized budget; expected no thinking, got %v", body["thinking"])
		}
	})
}

func TestResolveEffort(t *testing.T) {
	p := &AnthropicProvider{}
	if got := ResolveEffort(p, "claude-opus-5-5", EffortMax); got != EffortMax {
		t.Errorf("a level the model takes passes through, got %q", got)
	}
	if got := ResolveEffort(p, "claude-sonnet-4-6", EffortXHigh); got != "" {
		t.Errorf("a level the model does not take falls back to its default, got %q", got)
	}
	if got := ResolveEffort(p, "claude-opus-5-5", ""); got != "" {
		t.Errorf("no choice stays no choice, got %q", got)
	}
	if got := ResolveEffort(nil, "claude-opus-5-5", EffortMax); got != "" {
		t.Errorf("no provider resolves nothing, got %q", got)
	}
}
