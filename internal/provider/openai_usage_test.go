package provider

import "testing"

// The invariant under test: InputTokens must mean the same thing on every
// provider — input the model actually re-read, exclusive of anything served
// from cache — because callers sum it with the cache fields to get the true
// size of the request they just sent.
func TestUsageFromOAISubtractsCachedTokens(t *testing.T) {
	tests := []struct {
		name                string
		usage               *oaiUsage
		wantInput, wantRead int
	}{
		{
			name:      "nothing cached leaves prompt_tokens alone",
			usage:     &oaiUsage{PromptTokens: 1000, CompletionTokens: 50},
			wantInput: 1000,
		},
		{
			// The common agent-loop shape: most of the prefix is a cache hit.
			// Summed back together this must equal prompt_tokens, not 1900.
			name: "cached portion is carved out of prompt_tokens",
			usage: &oaiUsage{
				PromptTokens:        1000,
				CompletionTokens:    50,
				PromptTokensDetails: &oaiPromptTokensDetails{CachedTokens: 900},
			},
			wantInput: 100,
			wantRead:  900,
		},
		{
			name: "details present but zero cached",
			usage: &oaiUsage{
				PromptTokens:        1000,
				PromptTokensDetails: &oaiPromptTokensDetails{CachedTokens: 0},
			},
			wantInput: 1000,
		},
		{
			name: "a server reporting cached exclusive of prompt cannot go negative",
			usage: &oaiUsage{
				PromptTokens:        100,
				PromptTokensDetails: &oaiPromptTokensDetails{CachedTokens: 900},
			},
			wantInput: 0,
			wantRead:  900,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := usageFromOAI(tt.usage)
			if got.InputTokens != tt.wantInput {
				t.Errorf("InputTokens = %d, want %d", got.InputTokens, tt.wantInput)
			}
			if got.CacheReadTokens != tt.wantRead {
				t.Errorf("CacheReadTokens = %d, want %d", got.CacheReadTokens, tt.wantRead)
			}
		})
	}
}

// What the loop actually computes. Before the fix a 90%-cached step reported
// 1900 for a 1000-token request, halving the effective compaction threshold and
// flooring the output budget.
func TestUsageFromOAIRoundTripsToPromptTokens(t *testing.T) {
	u := &oaiUsage{
		PromptTokens:        1000,
		CompletionTokens:    50,
		PromptTokensDetails: &oaiPromptTokensDetails{CachedTokens: 900},
	}
	got := usageFromOAI(u)
	total := got.InputTokens + got.CacheReadTokens + got.CacheWriteTokens
	if total != u.PromptTokens {
		t.Errorf("input-side total = %d, want %d (prompt_tokens)", total, u.PromptTokens)
	}
}

func TestUsageFromOAINilIsNil(t *testing.T) {
	if got := usageFromOAI(nil); got != nil {
		t.Errorf("usageFromOAI(nil) = %+v, want nil", got)
	}
}

func TestUsageFromOAICarriesReasoningTokens(t *testing.T) {
	got := usageFromOAI(&oaiUsage{
		PromptTokens:            10,
		CompletionTokens:        200,
		CompletionTokensDetails: &oaiCompletionTokenDetails{ReasoningTokens: 150},
	})
	if got.ReasoningTokens != 150 {
		t.Errorf("ReasoningTokens = %d, want 150", got.ReasoningTokens)
	}
}

// DeepSeek's native API reports the cache hit as a TOP-LEVEL
// prompt_cache_hit_tokens rather than nesting it in prompt_tokens_details. The
// hit count is inside prompt_tokens there too, so it must be carved out —
// otherwise every cached step looks like a full-priced read, which is what the
// nested-only parse did before the fallback existed.
func TestUsageFromOAIReadsDeepSeekTopLevelCacheHit(t *testing.T) {
	got := usageFromOAI(&oaiUsage{
		PromptTokens:          1000,
		CompletionTokens:      50,
		PromptCacheHitTokens:  900,
		PromptCacheMissTokens: 100,
	})
	if got.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100 (prompt_tokens - cache hit)", got.InputTokens)
	}
	if got.CacheReadTokens != 900 {
		t.Errorf("CacheReadTokens = %d, want 900", got.CacheReadTokens)
	}
	total := got.InputTokens + got.CacheReadTokens + got.CacheWriteTokens
	if total != 1000 {
		t.Errorf("input-side total = %d, want 1000 (prompt_tokens)", total)
	}
}

// When both are present the nested field is the canonical OpenAI shape and
// wins; a proxy that carries both must not double-count or prefer the other.
func TestUsageFromOAIPrefersNestedDetailsOverTopLevelHit(t *testing.T) {
	got := usageFromOAI(&oaiUsage{
		PromptTokens:         1000,
		PromptCacheHitTokens: 100,
		PromptTokensDetails:  &oaiPromptTokensDetails{CachedTokens: 900},
	})
	if got.CacheReadTokens != 900 {
		t.Errorf("CacheReadTokens = %d, want 900 (nested details win)", got.CacheReadTokens)
	}
	if got.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", got.InputTokens)
	}
}

// A top-level hit on its own must not drive input negative either.
func TestUsageFromOAITopLevelHitCannotGoNegative(t *testing.T) {
	got := usageFromOAI(&oaiUsage{PromptTokens: 100, PromptCacheHitTokens: 900})
	if got.InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0 (clamped)", got.InputTokens)
	}
	if got.CacheReadTokens != 900 {
		t.Errorf("CacheReadTokens = %d, want 900", got.CacheReadTokens)
	}
}
