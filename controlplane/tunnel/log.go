package tunnel

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
)

// yamuxLogWriter is the io.Writer yamux's LogOutput receives. yamux writes
// free-form text (keepalive pings, stream resets, internal errors) through the
// standard log package, which terminates every entry with a newline. Lines are
// buffered until that newline arrives, then emitted as one Debug record on the
// connection's logger — keepalive and reset diagnostics land in the structured
// log instead of being discarded.
type yamuxLogWriter struct {
	logger *slog.Logger

	mu  sync.Mutex
	buf bytes.Buffer
}

// newYamuxLogWriter builds the writer. A nil logger falls back to
// slog.Default(), which is what the tunnel uses: neither conn nor the session
// constructors carry a logger of their own.
func newYamuxLogWriter(logger *slog.Logger) *yamuxLogWriter {
	if logger == nil {
		logger = slog.Default()
	}
	return &yamuxLogWriter{logger: logger}
}

// Write buffers p, emitting one Debug record per newline-terminated line. A
// trailing partial line stays buffered for the next Write. Safe for
// concurrent use; it never blocks (the handler does the sink work) and never
// fails — yamux is a diagnostics source, not a data path.
func (w *yamuxLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	for {
		data := w.buf.Bytes()
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSuffix(string(data[:i]), "\r")
		w.buf.Next(i + 1)
		w.logger.Debug(line, "component", "yamux")
	}
	return len(p), nil
}
