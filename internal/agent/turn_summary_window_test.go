package agent

import (
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// TestRecentTurnSummariesWindow pins the env gate for how many past turn
// summaries the next turn injects: unset/empty/unparseable fall back to the
// session default (2), a usable value is taken verbatim, and out-of-range
// values clamp into [turnSummaryWindowMin, turnSummaryWindowMax].
func TestRecentTurnSummariesWindow(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int
	}{
		{"unset uses the session default", "", session.RecentTurnSummariesWindow},
		{"a usable value is taken verbatim", "5", 5},
		{"zero disables injection", "0", 0},
		{"unparseable falls back to the session default", "lots", session.RecentTurnSummariesWindow},
		{"below the floor is clamped up", "-3", turnSummaryWindowMin},
		{"above the max is clamped down", "999999", turnSummaryWindowMax},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(turnSummaryWindowEnv, tt.raw)
			if got := recentTurnSummariesWindow(); got != tt.want {
				t.Fatalf("recentTurnSummariesWindow() with %q = %d, want %d", tt.raw, got, tt.want)
			}
		})
	}
}

// TestRecentTurnSummariesWindowDefaultIsTwo pins the requested default: the
// session default the accessor falls back to is 2, and the accessor returns
// it when the env var is unset.
func TestRecentTurnSummariesWindowDefaultIsTwo(t *testing.T) {
	if session.RecentTurnSummariesWindow != 2 {
		t.Fatalf("session.RecentTurnSummariesWindow = %d, want 2", session.RecentTurnSummariesWindow)
	}
	t.Setenv(turnSummaryWindowEnv, "")
	if got := recentTurnSummariesWindow(); got != 2 {
		t.Fatalf("recentTurnSummariesWindow() with unset env = %d, want 2", got)
	}
}
