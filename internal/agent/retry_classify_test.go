package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
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

// TestIsTransientNetErr_StructuralMatch covers the failures that kill a
// connection which was already working. These arrive carrying a real
// *net.OpError wrapping a kernel errno, so they are matched on identity and no
// longer depend on anyone's choice of words.
//
// EHOSTUNREACH is the one that prompted this: on a dual-stack machine with IPv6
// privacy addresses, rotating the temporary source address out from under an
// established stream fails the next read with exactly that, and a multi-minute
// model response is the connection most likely to still be open.
func TestIsTransientNetErr_StructuralMatch(t *testing.T) {
	v6 := &net.TCPAddr{IP: net.ParseIP("2409:40e4:1084:524d:3cb7:625f:d7dd:e6b5"), Port: 56073}
	remote := &net.TCPAddr{IP: net.ParseIP("2606:4700:8d90:eaa1:32ef:c1c:ba36:2d7e"), Port: 443}

	opErr := func(op string, errno syscall.Errno) error {
		return &net.OpError{Op: op, Net: "tcp", Source: v6, Addr: remote, Err: os.NewSyscallError(op, errno)}
	}

	transient := map[string]error{
		"EHOSTUNREACH — source address rotated away": opErr("read", syscall.EHOSTUNREACH),
		"ENETUNREACH — route withdrawn":              opErr("read", syscall.ENETUNREACH),
		"ECONNRESET — peer reset":                    opErr("read", syscall.ECONNRESET),
		"ECONNABORTED — proxy dropped it":            opErr("read", syscall.ECONNABORTED),
		"EPIPE — write to a closed socket":           opErr("write", syscall.EPIPE),
		"ETIMEDOUT — kernel gave up":                 opErr("read", syscall.ETIMEDOUT),
		"ENETDOWN — interface went away":             opErr("read", syscall.ENETDOWN),
		"unexpected EOF mid-frame":                   io.ErrUnexpectedEOF,
		"plain EOF":                                  io.EOF,
		"wrapped by the stream layer":                fmt.Errorf("stream read failed: %w", opErr("read", syscall.EHOSTUNREACH)),
	}
	for name, err := range transient {
		if !isTransientNetErr(err) {
			t.Errorf("%s: isTransientNetErr = false, want true", name)
		}
		if !isTransientError(err) {
			t.Errorf("%s: isTransientError = false, want true", name)
		}
	}

	// Not every failure is worth re-sending. A request the peer refused on its
	// merits, or a context the caller cancelled, must not be replayed.
	for name, err := range map[string]error{
		"caller cancelled":  context.Canceled,
		"plain assertion":   errors.New("tool call arguments were not valid JSON"),
		"permission denied": &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.EACCES)},
	} {
		if isTransientNetErr(err) {
			t.Errorf("%s: isTransientNetErr = true, want false", name)
		}
	}
}

// TestIsTransientError_ProviderProse guards the text fallback against the
// provider layer's own wording. describeStreamReadError deliberately renders
// io.EOF as prose, and the classifier used to grep for the literal "eof" — so
// the message the code itself produces was the one message it could not
// recognise, and an empty stream that should have re-dispatched silently handed
// the user a manual Resume instead.
func TestIsTransientError_ProviderProse(t *testing.T) {
	for _, msg := range []string{
		"stream read failed: provider closed the connection mid-response",
		"stream read failed: read tcp [2409:40e4::1]:56073->[2606:4700::1]:443: read: no route to host",
		"stream read failed: read tcp 1.2.3.4:1->5.6.7.8:443: read: network is unreachable",
		"stream read failed: write tcp 1.2.3.4:1->5.6.7.8:443: write: broken pipe",
		"stream read failed: read tcp 1.2.3.4:1->5.6.7.8:443: read: connection reset by peer",
	} {
		if !isTransientError(errors.New(msg)) {
			t.Errorf("text-only failure should be transient: %s", msg)
		}
	}

	// The idle watchdog firing is deliberately NOT transient. It means the
	// connection went silent for a whole budget, and re-dispatching would buy
	// another full budget of silence — up to maxMidStreamRetries of them, 30
	// minutes on the buffered budget, before the user learns anything at all.
	// A stall is surfaced so the answer can be to raise OGCODE_STREAM_IDLE_TIMEOUT.
	stalled := "stream read failed: no data received for 10m0s, connection appears stalled"
	if isTransientError(errors.New(stalled)) {
		t.Error("an idle-watchdog stall must not be auto-retried")
	}
}

// TestStreamEventErr covers the recovery of the structured error. A provider
// that has a Go error attaches it; one that reported a failure as a message
// leaves only text, and the caller must still get a usable error rather than nil.
func TestStreamEventErr(t *testing.T) {
	wrapped := fmt.Errorf("stream read failed: %w", syscall.EHOSTUNREACH)
	evt := provider.StreamEvent{Type: provider.EventError, Error: wrapped.Error(), Err: wrapped}
	if got := streamEventErr(evt); !errors.Is(got, syscall.EHOSTUNREACH) {
		t.Errorf("structured error lost: %v", got)
	}

	textOnly := provider.StreamEvent{Type: provider.EventError, Error: "overloaded_error: the model is overloaded"}
	got := streamEventErr(textOnly)
	if got == nil || got.Error() != textOnly.Error {
		t.Errorf("text-only event should yield its message, got %v", got)
	}
	if !isTransientError(got) {
		t.Error("an overloaded provider message should still classify as transient")
	}
}
