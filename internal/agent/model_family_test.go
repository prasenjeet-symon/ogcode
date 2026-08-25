package agent

import (
	"strings"
	"testing"
)

func TestModelFamily(t *testing.T) {
	cases := []struct {
		provider string
		model    string
		want     string
	}{
		{"anthropic", "claude-sonnet-4-6", "anthropic"},
		{"openrouter", "anthropic/claude-3.5-sonnet", "anthropic"}, // model name wins over aggregator
		{"openai", "gpt-4o", "openai"},
		{"openrouter", "openai/gpt-4o-mini", "openai"},
		{"openai", "o3-mini", "openai"},
		{"openrouter", "google/gemini-2.0-flash", "gemini"},
		{"ollama", "llama3.1", "local"},
		{"ollama", "qwen2.5-coder:7b", "local"}, // local runtime wins regardless of model
		{"ogcode-cerebras", "llama-3.3-70b", "generic"},
		{"", "", "generic"},
	}
	for _, c := range cases {
		if got := modelFamily(c.provider, c.model); got != c.want {
			t.Errorf("modelFamily(%q,%q) = %q, want %q", c.provider, c.model, got, c.want)
		}
	}
}

func TestModelFamilyStylePrompt(t *testing.T) {
	// "generic" is included deliberately: it is where every unrecognised model
	// lands, and the base prompt is written for Claude, not for them.
	for _, fam := range []string{"openai", "gemini", "local", "generic"} {
		if modelFamilyStylePrompt(fam) == "" {
			t.Errorf("expected a non-empty style block for family %q", fam)
		}
	}
	// Claude is what the base prompt is tuned for; the empty family means no
	// model is in hand at all.
	for _, fam := range []string{"anthropic", ""} {
		if got := modelFamilyStylePrompt(fam); got != "" {
			t.Errorf("expected no style block for family %q, got %q", fam, got)
		}
	}
	// The generic block must not contradict the parallel tool-call section.
	if strings.Contains(modelFamilyStylePrompt("generic"), "ONE tool at a time") {
		t.Error("generic block serialises tool calls; that rule belongs to small local models only")
	}
}

// The catalogue an aggregator serves is mostly models modelFamily has no name
// for. Landing in "generic" has to mean they get steering, not that they are
// treated as Claude.
func TestModelFamily_UnrecognisedModelsGetSteering(t *testing.T) {
	for _, model := range []string{
		"x-ai/grok-4",
		"deepseek/deepseek-r1",
		"qwen/qwen3-coder",
		"moonshotai/kimi-k2",
		"mistralai/mistral-large",
		"meta-llama/llama-4-maverick",
		"z-ai/glm-4.6",
		"cohere/command-a",
	} {
		fam := modelFamily("openrouter", model)
		if fam != "generic" {
			t.Errorf("modelFamily(%q) = %q, want generic", model, fam)
		}
		if modelFamilyStylePrompt(fam) == "" {
			t.Errorf("%s: unrecognised model got no working-style guidance", model)
		}
	}
}

func TestBuildSystemPromptForFamily_InjectsForCodebaseAgents(t *testing.T) {
	const marker = "Working style for this model"

	// A non-Claude family injects the style block for a codebase agent.
	openaiPrompt := buildSystemPromptForFamily(BuildAgent, "/tmp/proj", false, "", "", 0, 0, "openai")
	if !strings.Contains(openaiPrompt, marker) || !strings.Contains(openaiPrompt, "Be decisive") {
		t.Error("expected the openai working-style block in the Build agent prompt")
	}

	// Claude (the family the base prompt is written for) injects nothing.
	anthropicPrompt := buildSystemPromptForFamily(BuildAgent, "/tmp/proj", false, "", "", 0, 0, "anthropic")
	if strings.Contains(anthropicPrompt, marker) {
		t.Error("did not expect a working-style block for the anthropic family")
	}

	// The plain wrapper (no family) matches the anthropic/generic prompt.
	if got := buildSystemPrompt(BuildAgent, "/tmp/proj", false, "", "", 0, 0); strings.Contains(got, marker) {
		t.Error("buildSystemPrompt wrapper should add no style block")
	}

	// Utility (non-project-scoped) agents never get the block, even for openai.
	indexPrompt := buildSystemPromptForFamily(IndexAgent, "/tmp/proj", false, "", "", 0, 0, "openai")
	if strings.Contains(indexPrompt, marker) {
		t.Error("non-codebase agents must not get the working-style block")
	}
}
