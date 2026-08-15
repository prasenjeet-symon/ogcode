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
		{"ogcode-groq", "llama-3.3-70b", "generic"},
		{"", "", "generic"},
	}
	for _, c := range cases {
		if got := modelFamily(c.provider, c.model); got != c.want {
			t.Errorf("modelFamily(%q,%q) = %q, want %q", c.provider, c.model, got, c.want)
		}
	}
}

func TestModelFamilyStylePrompt(t *testing.T) {
	for _, fam := range []string{"openai", "gemini", "local"} {
		if modelFamilyStylePrompt(fam) == "" {
			t.Errorf("expected a non-empty style block for family %q", fam)
		}
	}
	for _, fam := range []string{"anthropic", "generic", ""} {
		if got := modelFamilyStylePrompt(fam); got != "" {
			t.Errorf("expected no style block for family %q, got %q", fam, got)
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
