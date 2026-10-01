package server

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// accessLog records each request through slog, so it lands in the log file
// (with the file's redaction) rather than on the terminal, where chi's
// middleware.Logger prints. floor is the least level a request logs at: the UI
// polls several endpoints every few seconds, so ogcode's own successful
// requests pass Debug and stay out of a default Info log, while the preview
// proxy passes Info to keep every request under the preview domain on record.
// Server errors log at Warn either way.
func accessLog(floor slog.Level) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				status := ww.Status()
				if status == 0 {
					status = http.StatusOK // handler wrote nothing
				}
				level := floor
				if status >= 500 {
					level = max(level, slog.LevelWarn)
				}
				ctx := r.Context()
				if !slog.Default().Enabled(ctx, level) {
					return
				}
				slog.LogAttrs(ctx, level, "http request",
					slog.String("method", r.Method),
					slog.String("host", r.Host),
					slog.String("uri", r.URL.RequestURI()),
					slog.Int("status", status),
					slog.Int("bytes", ww.BytesWritten()),
					slog.Duration("duration", time.Since(start)),
					slog.String("remote", r.RemoteAddr),
					slog.String("requestId", middleware.GetReqID(ctx)),
				)
			}()
			next.ServeHTTP(ww, r)
		})
	}
}

// recoverer turns a handler panic into a 500 and one Error record carrying the
// stack, in place of chi's middleware.Recoverer, which prints the stack to
// stderr. The terminal gets the one-line error; the stack goes to the file.
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rvr := recover()
			if rvr == nil {
				return
			}
			if rvr == http.ErrAbortHandler {
				// The sentinel net/http uses to abort a response; it must
				// reach the server, which suppresses its stack.
				panic(rvr)
			}
			slog.ErrorContext(r.Context(), "http handler panic",
				"method", r.Method,
				"uri", r.URL.RequestURI(),
				"requestId", middleware.GetReqID(r.Context()),
				"panic", rvr,
				"stack", string(debug.Stack()),
			)
			if r.Header.Get("Connection") != "Upgrade" {
				w.WriteHeader(http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
