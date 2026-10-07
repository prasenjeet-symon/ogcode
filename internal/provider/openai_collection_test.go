package provider

import "testing"

// TestCollectionFromBaseURL pins the label behind each endpoint preset the web
// app offers (COMPATIBLE_PRESETS in web/src/lib/providers.ts), plus the hosts it
// names by hand: OpenRouter, and GitHub Models, retired but still labelled so a
// slot saved against it keeps its name.
func TestCollectionFromBaseURL(t *testing.T) {
	cases := []struct {
		baseURL string
		want    string
	}{
		{"https://generativelanguage.googleapis.com/v1beta/openai", "Gemini"},
		{"https://api.deepseek.com/v1", "DeepSeek"},
		{"https://api.groq.com/openai/v1", "Groq"},
		{"https://api.cerebras.ai/v1", "Cerebras"},
		{"https://api.together.xyz/v1", "Together"},
		{"https://api.mistral.ai/v1", "Mistral"},
		{"https://api.sambanova.ai/v1", "SambaNova"},
		{"https://integrate.api.nvidia.com/v1", "NVIDIA"},
		{"https://openrouter.ai/api/v1", "OpenRouter"},
		{"https://models.inference.ai.azure.com", "GitHub Models"},
		{"https://api.openai.com/v1", ""},
		{"http://localhost:8000/v1", ""},
	}
	for _, c := range cases {
		if got := collectionFromBaseURL(c.baseURL); got != c.want {
			t.Errorf("collectionFromBaseURL(%q) = %q, want %q", c.baseURL, got, c.want)
		}
	}
}
