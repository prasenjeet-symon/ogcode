package provider

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestOpenAIProviderIdleTimeout pins which endpoints get the long idle budget.
// The distinction is not cosmetic: Ollama sends nothing at all while the model
// writes a tool call's arguments, so a tight budget aborts healthy long-file
// turns, while OpenAI streams those arguments as deltas and genuinely is dead
// after two minutes of silence.
func TestOpenAIProviderIdleTimeout(t *testing.T) {
	// These are the budgets the endpoint earns on its own behaviour, which an
	// operator override replaces wholesale — so the assertion only holds when
	// no override is in force.
	if os.Getenv(idleTimeoutEnv) != "" {
		t.Skipf("%s is set in this environment", idleTimeoutEnv)
	}
	tests := []struct {
		name    string
		id      string
		baseURL string
		want    time.Duration
	}{
		{"ollama batches tool calls", "ollama", "http://localhost:11434/v1", streamIdleTimeoutBuffered},
		{"ollama via a relay on another port", "ollama", "http://localhost:8090/v1", streamIdleTimeoutBuffered},
		{"any local endpoint", "custom", "http://127.0.0.1:1234/v1", streamIdleTimeoutBuffered},
		{"openai streams tool args", "openai", "https://api.openai.com/v1", streamIdleTimeout},
		{"openrouter streams tool args", "openrouter", "https://openrouter.ai/api/v1", streamIdleTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &OpenAIProvider{id: tt.id, baseURL: tt.baseURL}
			if got := p.idleTimeout(); got != tt.want {
				t.Errorf("idleTimeout() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestIsLocalEndpoint(t *testing.T) {
	// A LAN-hosted model server counts as local: it batches tool calls the same
	// way a loopback one does, and it is not exposed to the internet-scale
	// failures the tight idle budget exists to catch.
	local := []string{
		"http://localhost:11434/v1", "http://127.0.0.1:8090/v1", "http://[::1]:8080/v1",
		"http://host.docker.internal:11434/v1", "http://mymac.local:11434/v1",
		"http://10.0.0.5:11434/v1", "http://192.168.1.50:11434/v1",
	}
	remote := []string{
		"https://api.openai.com/v1", "https://openrouter.ai/api/v1",
		"https://api.anthropic.com/v1", "https://generativelanguage.googleapis.com/v1beta",
	}
	for _, u := range local {
		if !isLocalEndpoint(u) {
			t.Errorf("isLocalEndpoint(%q) = false, want true", u)
		}
	}
	for _, u := range remote {
		if isLocalEndpoint(u) {
			t.Errorf("isLocalEndpoint(%q) = true, want false", u)
		}
	}
}

// TestIdleWatchdogReportsItsOwnBudget guards the reporting path: an abort must
// name the budget that actually applied, or a 10-minute Ollama timeout would be
// reported as the 2-minute default and send the next reader down the wrong path.
func TestIdleWatchdogReportsItsOwnBudget(t *testing.T) {
	w := newIdleWatchdog(strings.NewReader("x"), func() {}, streamIdleTimeoutBuffered)
	defer w.Stop()
	if w.Timeout() != streamIdleTimeoutBuffered {
		t.Fatalf("Timeout() = %s, want %s", w.Timeout(), streamIdleTimeoutBuffered)
	}
	msg := describeStreamReadError(nil, true, w.Timeout())
	if !strings.Contains(msg, "10m0s") {
		t.Errorf("error message %q does not name the 10m budget that fired", msg)
	}
}

// TestIsLocalEndpointAgreesWithIsCloudURL documents the relationship between
// the two: isLocalEndpoint is isCloudURL's inverse plus the host forms its
// substring matching misses, so the pair must never both claim an endpoint.
func TestIsLocalEndpointAgreesWithIsCloudURL(t *testing.T) {
	for _, u := range []string{
		"http://localhost:11434/v1", "http://127.0.0.1:8090/v1", "http://192.168.1.50:11434/v1",
		"https://api.openai.com/v1", "https://openrouter.ai/api/v1",
	} {
		if isLocalEndpoint(u) == isCloudURL(u) {
			t.Errorf("%q: isLocalEndpoint and isCloudURL both returned %v", u, isCloudURL(u))
		}
	}
}

// TestParseIdleTimeout pins how the operator's override is read. The spellings
// matter as much as the values: the variable exists for people whose local model
// outlasts the built-in budget, and one they have to get exactly right to avoid
// a silent fallback would not have solved their problem.
func TestParseIdleTimeout(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{"unset leaves the built-in budget", "", 0},
		{"whitespace only", "   ", 0},
		{"go duration", "15m", 15 * time.Minute},
		{"go duration in seconds", "900s", 15 * time.Minute},
		{"go duration in hours", "2h", 2 * time.Hour},
		{"bare number means seconds", "900", 15 * time.Minute},
		{"case and padding are forgiven", "  30M  ", 30 * time.Minute},
		{"at the floor", "10s", idleTimeoutFloor},
		{"off", "off", idleTimeoutNever},
		{"none", "none", idleTimeoutNever},
		{"never", "never", idleTimeoutNever},
		{"NEVER uppercased", "NEVER", idleTimeoutNever},
		{"bare zero disables", "0", idleTimeoutNever},
		{"zero as a duration disables too", "0s", idleTimeoutNever},
		// Refusals. Each falls back to the built-in budget rather than to
		// something unusable — a bad value must not be able to break streaming.
		{"unparseable", "soon", 0},
		{"negative", "-5m", 0},
		{"below the floor", "5s", 0},
		{"bare number below the floor", "5", 0},
		// Too many seconds to hold as nanoseconds. It must not wrap into a
		// short budget, which would abort every stream.
		{"bare seconds beyond a Duration", "99999999999", idleTimeoutNever},
		{"bare seconds beyond an int64", "99999999999999999999999", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseIdleTimeout(tt.raw); got != tt.want {
				t.Errorf("parseIdleTimeout(%q) = %s, want %s", tt.raw, got, tt.want)
			}
		})
	}
}

// TestPickIdleTimeout covers the resolution itself: an override replaces every
// built-in budget, and no override leaves each endpoint on the one it earns.
func TestPickIdleTimeout(t *testing.T) {
	tests := []struct {
		name     string
		override time.Duration
		builtin  time.Duration
		want     time.Duration
	}{
		{"no override keeps the tight budget", 0, streamIdleTimeout, streamIdleTimeout},
		{"no override keeps the buffered budget", 0, streamIdleTimeoutBuffered, streamIdleTimeoutBuffered},
		{"override raises the tight budget", 30 * time.Minute, streamIdleTimeout, 30 * time.Minute},
		{"override replaces the buffered budget", 30 * time.Minute, streamIdleTimeoutBuffered, 30 * time.Minute},
		{"override may also lower a budget", 45 * time.Second, streamIdleTimeoutBuffered, 45 * time.Second},
		{"disabled wins everywhere", idleTimeoutNever, streamIdleTimeout, idleTimeoutNever},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pickIdleTimeout(tt.override, tt.builtin); got != tt.want {
				t.Errorf("pickIdleTimeout(%s, %s) = %s, want %s", tt.override, tt.builtin, got, tt.want)
			}
		})
	}
}

// TestResolveIdleTimeoutUnset guards the default path, which is what almost
// every install runs: with the variable unset, the budgets are exactly the ones
// the endpoint-specific logic chose.
func TestResolveIdleTimeoutUnset(t *testing.T) {
	if os.Getenv(idleTimeoutEnv) != "" {
		t.Skipf("%s is set in this environment", idleTimeoutEnv)
	}
	if got := resolveIdleTimeout(streamIdleTimeout); got != streamIdleTimeout {
		t.Errorf("resolveIdleTimeout(tight) = %s, want %s", got, streamIdleTimeout)
	}
	if got := resolveIdleTimeout(streamIdleTimeoutBuffered); got != streamIdleTimeoutBuffered {
		t.Errorf("resolveIdleTimeout(buffered) = %s, want %s", got, streamIdleTimeoutBuffered)
	}
}

// TestIdleWatchdogNeverBudget checks the claim idleTimeoutNever rests on: that a
// near-overflow deadline arms a timer normally instead of panicking or firing at
// once. If time.AfterFunc ever stopped clamping the overflow, "off" would become
// "abort immediately" — the worst possible reading of that setting.
func TestIdleWatchdogNeverBudget(t *testing.T) {
	fired := make(chan struct{}, 1)
	w := newIdleWatchdog(strings.NewReader("x"), func() { fired <- struct{}{} }, idleTimeoutNever)
	defer w.Stop()
	if w.Timeout() != idleTimeoutNever {
		t.Fatalf("Timeout() = %s, want the disabled budget", w.Timeout())
	}
	// Read a byte, which resets the timer — the other path that carries the
	// budget into a deadline computation.
	if _, err := w.Read(make([]byte, 1)); err != nil {
		t.Fatalf("Read: %v", err)
	}
	select {
	case <-fired:
		t.Fatal("watchdog fired with the disabled budget")
	case <-time.After(50 * time.Millisecond):
	}
}
