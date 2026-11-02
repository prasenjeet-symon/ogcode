package agent

import (
	"net/http"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// TestClassifyInterruption_FreeAllowance pins the user-facing story for a spent
// free daily allowance. It is a 429 like a plan budget, but the fix is waiting
// for the UTC day, not a few minutes — so the text has to say that, name what
// was used, and never read as a failed bill or an exhausted plan.
func TestClassifyInterruption_FreeAllowance(t *testing.T) {
	const body = `{"error":"free daily allowance exhausted","plan":"none","free":true,"daily_limit":5,"daily_used":5.0002,"daily_reset":1793664000}`
	got := classifyInterruption(&provider.APIError{
		Provider:   "ogx",
		StatusCode: http.StatusTooManyRequests,
		Body:       body,
	}, 4)
	if got == nil {
		t.Fatal("no interruption recorded")
	}
	if got.Reason != session.InterruptRateLimit {
		t.Errorf("reason = %q, want %q — waiting is still the fix", got.Reason, session.InterruptRateLimit)
	}
	if !got.Resumable {
		t.Error("resumable = false, want true — the allowance refills")
	}
	if got.RetryAfter != 1793664000 {
		t.Errorf("retryAfter = %d, want the daily_reset second 1793664000", got.RetryAfter)
	}
	if got.Step != 4 {
		t.Errorf("step = %d, want 4", got.Step)
	}
	for _, want := range []string{
		"Free daily allowance used up (5.00 of 5.00)",
		"00:00 UTC on Tue 3 Nov 2026",
	} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("detail = %q, want it to contain %q", got.Detail, want)
		}
	}
	for _, unwanted := range []string{"billing", "balance", "plan budget", "quota"} {
		if strings.Contains(strings.ToLower(got.Detail), unwanted) {
			t.Errorf("detail = %q, must not read as %q", got.Detail, unwanted)
		}
	}
}

// A body without the figures still says the allowance is spent and that it
// refills with the UTC day, rather than printing zeros or dropping the case
// into the generic rate-limit wording.
func TestClassifyInterruption_FreeAllowanceWithoutFigures(t *testing.T) {
	got := classifyInterruption(&provider.APIError{
		StatusCode: http.StatusTooManyRequests,
		Body:       "free daily allowance exhausted",
	}, 1)
	if got.Reason != session.InterruptRateLimit {
		t.Errorf("reason = %q, want %q", got.Reason, session.InterruptRateLimit)
	}
	if got.RetryAfter != 0 {
		t.Errorf("retryAfter = %d, want 0 when the gateway gave no reset time", got.RetryAfter)
	}
	if want := "Free daily allowance used up. It refills with the UTC day."; got.Detail != want {
		t.Errorf("detail = %q, want %q", got.Detail, want)
	}

	// A plan-budget 429 keeps its own wording: that one is a window to wait
	// out, not an allowance that refills on a calendar day.
	budget := classifyInterruption(&provider.APIError{
		StatusCode: http.StatusTooManyRequests,
		Body:       `{"error":"plan budget exhausted","plan":"pro"}`,
	}, 1)
	if budget.Detail != rateLimitDetail(0) {
		t.Errorf("plan budget detail = %q, want the rate-limit wording %q", budget.Detail, rateLimitDetail(0))
	}
}
