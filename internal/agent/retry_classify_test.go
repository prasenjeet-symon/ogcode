package agent

import (
	"fmt"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
)

// TestClassifiers_PreferTypedError verifies the loop's classifiers use the
// provider's structured status/body when present, and — importantly — that a
// 400 whose body happens to contain a transient-looking word ("timeout") is NOT
// retried, which the old string-only matcher got wrong.
func TestClassifiers_PreferTypedError(t *testing.T) {
	rateLimited := &provider.APIError{Provider: "anthropic", StatusCode: 429, Body: "rate limit"}
	if !isTransientError(rateLimited) {
		t.Error("429 APIError should be transient")
	}

	overflow := &provider.APIError{Provider: "openai", StatusCode: 400, Body: `{"code":"context_length_exceeded"}`}
	if !isContextLengthError(overflow) {
		t.Error("400 context_length_exceeded should be a context-length error")
	}
	if isTransientError(overflow) {
		t.Error("a 400 context-length error must NOT be treated as transient")
	}

	// A 400 whose body mentions "timeout" is a client error, not transient.
	badReq := &provider.APIError{Provider: "openai", StatusCode: 400, Body: "upstream timeout while validating request"}
	if isTransientError(badReq) {
		t.Error("a 400 must not be retried even if its body says 'timeout'")
	}

	// Wrapped errors are still classified (errors.As unwraps).
	wrapped := fmt.Errorf("stream chat: %w", rateLimited)
	if !isTransientError(wrapped) {
		t.Error("wrapped 429 APIError should still be transient")
	}
}

// TestClassifiers_StringFallback verifies non-HTTP (stream/network) errors, which
// carry no status code, still classify via the string fallback.
func TestClassifiers_StringFallback(t *testing.T) {
	if !isTransientError(fmt.Errorf("read tcp 1.2.3.4:443: connection reset by peer")) {
		t.Error("connection reset should be transient via fallback")
	}
	if !isTransientError(fmt.Errorf("anthropic is overloaded")) {
		t.Error("overloaded should be transient via fallback")
	}
	if !isContextLengthError(fmt.Errorf("ollama API error 400: ")) {
		t.Error("Ollama bare-400 should be a context-length error via fallback")
	}
	if isTransientError(fmt.Errorf("some unrelated failure")) {
		t.Error("an unrelated error should not be transient")
	}
}

func TestRetryAfterFromError(t *testing.T) {
	withRA := &provider.APIError{StatusCode: 429, RetryAfter: 8 * time.Second}
	if got := retryAfterFromError(fmt.Errorf("stream chat: %w", withRA)); got != 8*time.Second {
		t.Errorf("retryAfterFromError = %v, want 8s", got)
	}
	if got := retryAfterFromError(fmt.Errorf("plain error")); got != 0 {
		t.Errorf("retryAfterFromError(plain) = %v, want 0", got)
	}
}
