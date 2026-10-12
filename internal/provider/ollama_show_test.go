package provider

import "testing"

func TestOllamaShowContextWindowFromShow(t *testing.T) {
	resp := &ollamaShowResponse{ModelInfo: map[string]any{
		"general.architecture":       "glm5_next",
		"glm5_next.context_length":   float64(1048576),
		"glm5_next.embedding_length": float64(4096),
	}}
	if got := ollamaContextWindowFromShow(resp); got != 1048576 {
		t.Errorf("got %d, want 1048576", got)
	}

	absent := &ollamaShowResponse{ModelInfo: map[string]any{"general.architecture": "x"}}
	if got := ollamaContextWindowFromShow(absent); got != 0 {
		t.Errorf("got %d, want 0 when no .context_length key exists", got)
	}
}
