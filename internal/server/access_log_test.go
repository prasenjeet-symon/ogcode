package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// captureLogs points the default logger at a buffer for one test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestAccessLogLevels(t *testing.T) {
	for _, tc := range []struct {
		name   string
		floor  slog.Level
		status int
		want   string
	}{
		{"api success stays at debug", slog.LevelDebug, http.StatusOK, "level=DEBUG"},
		{"api client error logs at info", slog.LevelDebug, http.StatusNotFound, "level=INFO"},
		{"api auth error is a warning", slog.LevelDebug, http.StatusUnauthorized, "level=WARN"},
		{"api rate limit is a warning", slog.LevelDebug, http.StatusTooManyRequests, "level=WARN"},
		{"api server error is a warning", slog.LevelDebug, http.StatusBadGateway, "level=WARN"},
		{"preview success is on record", slog.LevelInfo, http.StatusOK, "level=INFO"},
		{"preview server error is a warning", slog.LevelInfo, http.StatusInternalServerError, "level=WARN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buf := captureLogs(t)
			h := accessLog(tc.floor)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte("body"))
			}))
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/session?id=1", nil))
			out := buf.String()
			if !strings.Contains(out, tc.want) || !strings.Contains(out, `path=/api/session`) || !strings.Contains(out, `queryKeys=id`) || !strings.Contains(out, "bytes=4") {
				t.Errorf("want %s access line, got:\n%s", tc.want, out)
			}
		})
	}
}

// A handler panic becomes a 500 and one Error record with the stack, rather
// than a stack printed to the terminal.
func TestRecovererLogsPanic(t *testing.T) {
	buf := captureLogs(t)
	h := recoverer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("nil map write")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/boom", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	out := buf.String()
	for _, want := range []string{"level=ERROR", "http handler panic", "panic=\"nil map write\"", "stack="} {
		if !strings.Contains(out, want) {
			t.Errorf("panic record lacks %q:\n%s", want, out)
		}
	}
}

func TestRecovererRepanicsAbort(t *testing.T) {
	captureLogs(t)
	h := recoverer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		if recover() != http.ErrAbortHandler {
			t.Error("ErrAbortHandler was swallowed")
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
}

// The query values are user content — the ⌘K palette's search text, a
// directory path shipped as a parameter — so the request record carries the
// path and the parameter names only. Both records log at Warn and ship to the
// remote sink, where nothing can be recalled.
func TestAccessLogDoesNotLeakQueryValues(t *testing.T) {
	buf := captureLogs(t)
	h := accessLog(slog.LevelInfo)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	h.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest("GET", "/api/session/search?q=private-search-text&directory=/home/someone/private", nil))
	out := buf.String()
	for _, want := range []string{"path=/api/session/search", "queryKeys="} {
		if !strings.Contains(out, want) {
			t.Errorf("access record lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "directory,q") {
		t.Errorf("access record lacks the queryKeys \"directory,q\":\n%s", out)
	}
	for _, leak := range []string{"private-search-text", "/home/someone/private", "uri="} {
		if strings.Contains(out, leak) {
			t.Errorf("access record leaks %q:\n%s", leak, out)
		}
	}
}

// The panic record shows the stack and the request shape, never the query
// values: same rule as above at Error level.
func TestRecovererDoesNotLeakQueryValues(t *testing.T) {
	buf := captureLogs(t)
	h := recoverer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("synthetic boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec,
		httptest.NewRequest("GET", "/api/session/search?q=private-search-text&directory=/home/someone/private", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	out := buf.String()
	for _, want := range []string{"http handler panic", "path=/api/session/search", "stack=", "goroutine"} {
		if !strings.Contains(out, want) {
			t.Errorf("panic record lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "directory,q") {
		t.Errorf("panic record lacks the queryKeys \"directory,q\":\n%s", out)
	}
	for _, leak := range []string{"private-search-text", "/home/someone/private", "uri="} {
		if strings.Contains(out, leak) {
			t.Errorf("panic record leaks %q:\n%s", leak, out)
		}
	}
}
