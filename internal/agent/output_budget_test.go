package agent

import "testing"

func TestOutputTokenBudget(t *testing.T) {
	tests := []struct {
		name          string
		modelMax      int
		contextWindow int
		requestTokens int
		want          int
	}{
		{"unknown ceiling sends no limit", 0, 200000, 1000, 0},
		{"small request gets the full ceiling", 64000, 200000, 5000, 64000},
		{"unknown window still gets the full ceiling", 32000, 0, 5000, 32000},
		{"nearly-full window clamps to the room left", 64000, 200000, 160000, 200000 - 160000 - outputBudgetMargin},
		{"full window floors at the provider minimum", 64000, 200000, 199000, minOutputTokens},
		{"over-full window never goes negative", 64000, 200000, 260000, minOutputTokens},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := outputTokenBudget(tt.modelMax, tt.contextWindow, tt.requestTokens); got != tt.want {
				t.Errorf("outputTokenBudget(%d, %d, %d) = %d, want %d",
					tt.modelMax, tt.contextWindow, tt.requestTokens, got, tt.want)
			}
		})
	}
}

// TestOutputTokenBudgetFitsContextWindow is the invariant that matters: a
// provider rejects any request whose input plus max_tokens overruns the window,
// so the budget must always leave room for the input it was sized against.
func TestOutputTokenBudgetFitsContextWindow(t *testing.T) {
	const window = 200000
	for _, requestTokens := range []int{0, 50000, 120000, 175000, 190000} {
		budget := outputTokenBudget(64000, window, requestTokens)
		if requestTokens+budget > window {
			t.Errorf("requestTokens=%d + budget=%d exceeds the %d-token window", requestTokens, budget, window)
		}
	}
}
