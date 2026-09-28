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

// Gemini's OpenAI-compatible endpoint leaves thinking out of completion_tokens
// and reports it only in total_tokens, with no reasoning breakdown. These are
// the figures a live gemini-2.5-flash call returned: 3430 + 24 = 3454, but the
// provider billed 3693. The 239-token gap is thinking, spent as output.
func TestUsageFromOAIRecoversThinkingHiddenInTotal(t *testing.T) {
	got := usageFromOAI(&oaiUsage{PromptTokens: 3430, CompletionTokens: 24, TotalTokens: 3693})
	if got.OutputTokens != 263 {
		t.Errorf("OutputTokens = %d, want 263 (24 visible + 239 thinking)", got.OutputTokens)
	}
	if got.ReasoningTokens != 239 {
		t.Errorf("ReasoningTokens = %d, want 239", got.ReasoningTokens)
	}
	if got.InputTokens != 3430 {
		t.Errorf("InputTokens = %d, want 3430 (prompt untouched)", got.InputTokens)
	}
	if sum := got.InputTokens + got.CacheReadTokens + got.CacheWriteTokens + got.OutputTokens; sum != 3693 {
		t.Errorf("all tokens = %d, want 3693 (the provider's total_tokens)", sum)
	}
}

// A provider that itemises reasoning but leaves it out of completion_tokens
// still has the gap added to output, and its own reasoning figure is kept rather
// than doubled.
func TestUsageFromOAIReasoningOutsideCompletionIsNotDoubled(t *testing.T) {
	got := usageFromOAI(&oaiUsage{
		PromptTokens:            100,
		CompletionTokens:        20,
		TotalTokens:             420,
		CompletionTokensDetails: &oaiCompletionTokenDetails{ReasoningTokens: 300},
	})
	if got.OutputTokens != 320 {
		t.Errorf("OutputTokens = %d, want 320", got.OutputTokens)
	}
	if got.ReasoningTokens != 300 {
		t.Errorf("ReasoningTokens = %d, want 300 (not 600)", got.ReasoningTokens)
	}
}

// When total_tokens shows the cached count sits OUTSIDE prompt_tokens (the gap
// equals it exactly), prompt_tokens is already the uncached input: it must be
// kept, not clamped to zero, and the gap must not be mistaken for output.
func TestUsageFromOAIExclusiveCachedCountKeepsInput(t *testing.T) {
	got := usageFromOAI(&oaiUsage{
		PromptTokens:        100,
		CompletionTokens:    20,
		TotalTokens:         1020,
		PromptTokensDetails: &oaiPromptTokensDetails{CachedTokens: 900},
	})
	if got.InputTokens != 100 || got.CacheReadTokens != 900 {
		t.Errorf("input/cacheRead = %d/%d, want 100/900", got.InputTokens, got.CacheReadTokens)
	}
	if got.OutputTokens != 20 || got.ReasoningTokens != 0 {
		t.Errorf("output/reasoning = %d/%d, want 20/0", got.OutputTokens, got.ReasoningTokens)
	}
}

// The ordinary shapes are untouched: an OpenAI total that equals prompt +
// completion adds nothing, and a missing total_tokens is never read as a gap.
func TestUsageFromOAIConsistentTotalAddsNothing(t *testing.T) {
	for name, u := range map[string]*oaiUsage{
		"total matches": {PromptTokens: 1000, CompletionTokens: 50, TotalTokens: 1050,
			PromptTokensDetails: &oaiPromptTokensDetails{CachedTokens: 900}},
		"total absent": {PromptTokens: 1000, CompletionTokens: 50,
			PromptTokensDetails: &oaiPromptTokensDetails{CachedTokens: 900}},
	} {
		got := usageFromOAI(u)
		if got.InputTokens != 100 || got.CacheReadTokens != 900 || got.OutputTokens != 50 || got.ReasoningTokens != 0 {
			t.Errorf("%s: got %+v, want input 100, cacheRead 900, output 50, reasoning 0", name, got)
		}
	}
}
