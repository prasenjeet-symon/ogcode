package tunnel

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
)

// captureHandler is a slog handler that records every message it receives.
// Writes to the slice happen under a mutex, so a concurrent writer's output
// can be counted and compared exactly under -race.
type captureHandler struct {
	mu    sync.Mutex
	lines []string
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lines = append(h.lines, r.Message)
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func newCapturingLogger() (*captureHandler, *slog.Logger) {
	h := &captureHandler{}
	return h, slog.New(h)
}

// TestYamuxLogWriterFraming checks the line framing: everything buffered up to
// each newline is emitted as one record, a trailing partial line stays in the
// buffer until its newline arrives, and several lines in one Write come out as
// several records.
func TestYamuxLogWriterFraming(t *testing.T) {
	rec, log := newCapturingLogger()
	w := newYamuxLogWriter(log)

	writes := []string{
		"hello ",
		"world\nkeepalive\n",
		"reset by peer", // no newline yet: buffered
		"\n",            // completes the buffered line
		"a\n\nb\n",      // an empty line still frames
		"crlf\r\n",      // \r is trimmed the way log.Logger output arrives
	}
	want := []string{
		"hello world",
		"keepalive",
		"reset by peer",
		"a",
		"",
		"b",
		"crlf",
	}

	for _, s := range writes {
		if n, err := w.Write([]byte(s)); n != len(s) || err != nil {
			t.Fatalf("Write(%q) = %d, %v; want %d, nil", s, n, err, len(s))
		}
	}

	rec.mu.Lock()
	got := append([]string(nil), rec.lines...)
	rec.mu.Unlock()
	if len(got) != len(want) {
		t.Fatalf("framed %d records; want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("record %d = %q; want %q", i, got[i], want[i])
		}
	}
}

// TestYamuxLogWriterConcurrent hammers one writer from many goroutines, each
// writing whole newline-terminated lines per Write call, and checks every line
// arrives exactly once, byte-for-byte — that no interleaved write ever splits,
// merges or drops a record. Run with -race.
func TestYamuxLogWriterConcurrent(t *testing.T) {
	rec, log := newCapturingLogger()
	w := newYamuxLogWriter(log)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				// One Write per line: interleaved partial writes from
				// competing goroutines would legitimately merge in the
				// shared buffer (the framing test covers partial lines
				// from a single writer).
				line := fmt.Sprintf("goroutine-%d-line-%d\n", g, i)
				if _, err := w.Write([]byte(line)); err != nil {
					t.Errorf("write failed: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	rec.mu.Lock()
	got := append([]string(nil), rec.lines...)
	rec.mu.Unlock()

	const wantCount = 8 * 50
	if len(got) != wantCount {
		t.Fatalf("got %d records; want %d", len(got), wantCount)
	}
	// Every written line must arrive exactly once, byte-for-byte — the
	// writer strips the framing newline when it emits the record, so the
	// comparison is against the line contents that were written.
	want := map[string]bool{}
	for g := 0; g < 8; g++ {
		for i := 0; i < 50; i++ {
			want[fmt.Sprintf("goroutine-%d-line-%d", g, i)] = true
		}
	}
	seen := map[string]bool{}
	for _, line := range got {
		if seen[line] {
			t.Errorf("duplicate record %q", line)
		}
		seen[line] = true
		if !want[line] {
			t.Errorf("record %q is not one intact written line", line)
		}
	}
}

func TestYamuxLogWriterNilLoggerUsesDefault(t *testing.T) {
	if newYamuxLogWriter(nil) == nil {
		t.Fatal("nil logger must still produce a usable writer")
	}
}
