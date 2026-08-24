package provider

import (
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
