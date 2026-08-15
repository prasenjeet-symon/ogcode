package agent

import "testing"

func TestCompactionThresholdTokens(t *testing.T) {
	tests := []struct {
		name          string
		contextWindow int
		want          int
	}{
		{"unknown window falls back to fixed cap", 0, fallbackMaxRequestTokens},
		{"negative window falls back to fixed cap", -5, fallbackMaxRequestTokens},
		{"200k window reserves 20k", 200000, 200000 - compactionReserveTokens},
		{"128k window reserves 20k", 128000, 128000 - compactionReserveTokens},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compactionThresholdTokens(tt.contextWindow); got != tt.want {
				t.Fatalf("compactionThresholdTokens(%d) = %d, want %d", tt.contextWindow, got, tt.want)
			}
		})
	}
}

func TestCompactionThresholdTokens_SmallWindowClampsReserve(t *testing.T) {
	// For a tiny window the reserve must be clamped to at most half, so the
	// budget never collapses to zero/negative.
	const small = 8000
	got := compactionThresholdTokens(small)
	want := small - small/2 // reserve clamped to half
	if got != want {
		t.Fatalf("small window: got %d, want %d", got, want)
	}
	if got <= 0 {
		t.Fatalf("budget must stay positive, got %d", got)
	}
}

func TestEffectiveRequestTokens(t *testing.T) {
	tests := []struct {
		name           string
		estimatedTokens int
		reportedTokens  int
		want            int
	}{
		{"no reported usage yet uses estimate", 100_000, 0, 100_000},
		{"reported larger than estimate wins (exact count)", 25_000, 40_000, 40_000},
		{"estimate larger than reported wins (fresh tool output)", 75_000, 40_000, 75_000},
		{"equal falls to estimate", 80_000, 80_000, 80_000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := effectiveRequestTokens(tt.estimatedTokens, tt.reportedTokens); got != tt.want {
				t.Fatalf("effectiveRequestTokens(%d, %d) = %d, want %d",
					tt.estimatedTokens, tt.reportedTokens, got, tt.want)
			}
		})
	}
}

// A larger context window must always permit a larger request than a smaller one.
func TestCompactionThresholdTokens_MonotonicInWindow(t *testing.T) {
	prev := compactionThresholdTokens(16000)
	for _, w := range []int{32000, 64000, 128000, 200000, 1000000} {
		cur := compactionThresholdTokens(w)
		if cur <= prev {
			t.Fatalf("threshold not increasing: window=%d gave %d, previous %d", w, cur, prev)
		}
		prev = cur
	}
}
