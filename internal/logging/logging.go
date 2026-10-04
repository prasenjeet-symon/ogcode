// Package logging sets up ogcode's process-wide slog logger: a size-rotated,
// owner-only log file that receives everything at the configured level, and a
// terminal that sees only what the user must act on (errors, by default).
//
// Configuration is by environment, like the rest of ogcode's runtime knobs:
//
//	OGCODE_LOG_LEVEL        debug | info (default) | warn | error — the file's threshold
//	OGCODE_LOG_FORMAT       text (default) | json — the file's format
//	OGCODE_LOG_CONSOLE      off | error (default) | warn | info | debug — the terminal's threshold
//	OGCODE_LOG_DIR          root for log files (default ~/.ogcode/logs; each project gets a folder inside)
//	OGCODE_LOG_MAX_SIZE_MB  rotate the file past this size (default 10)
//	OGCODE_LOG_MAX_FILES    rotated files kept (default 5)
//	OGCODE_LOG_MAX_AGE_DAYS rotated files older than this are deleted (default 14, 0 = keep)
//	OGCODE_LOG_COMPRESS     gzip rotated files (default on)
//
// Records can also be shipped to PostHog Logs, off by default:
//
//	OGCODE_POSTHOG_LOGS       on | off (default) — ship records to PostHog Logs
//	OGCODE_POSTHOG_LOGS_LEVEL severity floor for shipped records (default warn)
//
// Shipping is opt-in and honours DO_NOT_TRACK; records are redacted before they
// leave the process.
package logging

import (
	"context"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

// LevelOff is a threshold no record reaches: the sink is silent.
const LevelOff = slog.Level(math.MaxInt32)

// DefaultRemoteLevel is the floor below which records are never shipped to
// PostHog: a debug log file is for the machine it runs on, and shipping debug
// lines would be both noisy and a surprise. The file's level raises or lowers
// nothing here — OGCODE_POSTHOG_LOGS_LEVEL is the only remote knob.
const DefaultRemoteLevel = slog.LevelWarn

// Defaults for a log file's size and retention. The worst case on disk is
// MaxSizeMB × (MaxFiles + 1), before compression.
const (
	DefaultMaxSizeMB  = 10
	DefaultMaxFiles   = 5
	DefaultMaxAgeDays = 14
)

// Options configure the process logger.
type Options struct {
	// Dir and Name place the log file. Empty Name means no file: the terminal
	// is the only sink (used by commands like `version` that do no work worth
	// keeping and must not create .ogcode/ wherever they are run).
	Dir  string
	Name string

	Level  slog.Level // file threshold
	Format string     // "text" or "json"; the file's format

	Console slog.Level // terminal threshold; LevelOff silences it

	Rotate RotateOptions

	// Stderr is the terminal sink. Nil means os.Stderr.
	Stderr io.Writer
}

// FromEnv reads the OGCODE_LOG_* variables over the defaults. dir and name
// are where the calling command keeps its log (see Root and ProjectDir, which
// apply OGCODE_LOG_DIR).
func FromEnv(dir, name string) Options {
	o := Options{
		Dir:     dir,
		Name:    name,
		Level:   slog.LevelInfo,
		Format:  "text",
		Console: slog.LevelError,
		Rotate: RotateOptions{
			MaxSize:    DefaultMaxSizeMB << 20,
			MaxBackups: DefaultMaxFiles,
			MaxAge:     DefaultMaxAgeDays * 24 * time.Hour,
			Compress:   true,
		},
	}
	if l, ok := envLevel("OGCODE_LOG_LEVEL"); ok {
		o.Level = l
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("OGCODE_LOG_FORMAT")), "json") {
		o.Format = "json"
	}
	if l, ok := envLevel("OGCODE_LOG_CONSOLE"); ok {
		o.Console = l
	}
	if n, ok := envIntChecked("OGCODE_LOG_MAX_SIZE_MB"); ok && n > 0 {
		o.Rotate.MaxSize = int64(n) << 20
	}
	if n, ok := envIntChecked("OGCODE_LOG_MAX_FILES"); ok && n >= 0 {
		o.Rotate.MaxBackups = n
	}
	if n, ok := envIntChecked("OGCODE_LOG_MAX_AGE_DAYS"); ok && n >= 0 {
		o.Rotate.MaxAge = time.Duration(n) * 24 * time.Hour
	}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("OGCODE_LOG_COMPRESS"))); v != "" {
		o.Rotate.Compress = !(v == "0" || v == "false" || v == "off" || v == "no")
	}
	return o
}

// parseLevel reads a level name. "off" (and its synonyms) is LevelOff.
func parseLevel(s string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	case "off", "none", "false", "0":
		return LevelOff, true
	}
	return 0, false
}

// envLevel reads a level from the named environment variable. An empty value
// is simply unset; a value that is present but not a level name is ignored
// with one warning, so a typo does not silently leave the default in place.
func envLevel(name string) (slog.Level, bool) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return 0, false
	}
	l, ok := parseLevel(v)
	if !ok {
		slog.Warn("ignoring unparseable log level", "env", name, "value", v)
		return 0, false
	}
	return l, true
}

// envIntChecked reads an integer setting. An empty value is simply unset; a
// value that is present but unparseable is ignored with one warning, so a typo
// does not silently leave the default in place.
func envIntChecked(name string) (int, bool) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		slog.Warn("ignoring unparseable log setting", "env", name, "value", v)
		return 0, false
	}
	return n, true
}

// Logger is a configured process logger and the file behind it.
type Logger struct {
	*slog.Logger
	file *RotatingFile
	ship *posthogSink

	// ConsoleFallback reports that the log file could not be opened and the
	// terminal is carrying its records. Set once by New.
	ConsoleFallback bool
}

// Path is the active log file, or "" when there is none.
func (l *Logger) Path() string {
	if l.file == nil {
		return ""
	}
	return l.file.Path()
}

// Close stops the PostHog sink (flushing any buffered records), closes the file
// and waits for pending rotation housekeeping.
func (l *Logger) Close() error {
	if l.ship != nil {
		l.ship.stop()
	}
	if l.file == nil {
		return nil
	}
	return l.file.Close()
}

// New builds a logger from o. It never fails: if the file cannot be opened
// (read-only disk, no permission) the terminal takes over at the file's level,
// and says why, so nothing is lost silently.
func New(o Options) *Logger {
	stderr := o.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	l := &Logger{}
	var handlers []slog.Handler
	var openErr error
	// An Off file level means no file at all: neither opened nor created.
	if o.Name != "" && o.Level != LevelOff {
		f, err := OpenRotating(filepath.Join(o.Dir, o.Name), o.Rotate)
		if err == nil {
			l.file = f
			handlers = append(handlers, fileHandler(f, o))
		} else {
			openErr = err
			// The file cannot take its records, so the terminal takes over at
			// the file's level and the fallback is recorded on the logger.
			o.Console = min(o.Console, o.Level)
			l.ConsoleFallback = true
		}
	}
	if o.Console != LevelOff {
		handlers = append(handlers, consoleHandler(stderr, o.Console))
	}
	if o.shipsLogs() {
		l.ship = newPostHogSink(posthogShipLevel(), posthogLogsEndpoint, posthogLogsToken, posthogLogsClient, posthogFlushInterval)
		handlers = append(handlers, &posthogHandler{sink: l.ship})
	}
	switch len(handlers) {
	case 0:
		l.Logger = slog.New(slog.DiscardHandler)
	case 1:
		l.Logger = slog.New(handlers[0])
	default:
		l.Logger = slog.New(slog.NewMultiHandler(handlers...))
	}
	if openErr != nil {
		l.Warn("file logging unavailable; logging to the terminal instead", "err", openErr)
	}
	return l
}

// Setup builds a logger from o and installs it as the process default, which
// also routes the standard library's log package (net/http's server errors,
// for one) through it.
func Setup(o Options) *Logger {
	l := New(o)
	slog.SetDefault(l.Logger)
	return l
}

func fileHandler(w io.Writer, o Options) slog.Handler {
	opts := &slog.HandlerOptions{
		Level:       o.Level,
		AddSource:   o.Level <= slog.LevelDebug,
		ReplaceAttr: redactAttr,
	}
	if o.Format == "json" {
		return slog.NewJSONHandler(w, opts)
	}
	return slog.NewTextHandler(w, opts)
}

// consoleHandler writes the terminal's view of the log. On a terminal it is
// terse for a person watching: no timestamp (they are watching it happen) and
// no stack traces (those stay in the file, where a panic's full record
// belongs). Piped — under a container, in a service unit — it is JSON with the
// timestamp kept, because a collector parses it rather than reads it.
func consoleHandler(w io.Writer, level slog.Level) slog.Handler {
	if !consoleIsTerminal(w) {
		return slog.NewJSONHandler(w, &slog.HandlerOptions{
			Level:       level,
			ReplaceAttr: redactAttr,
		})
	}
	return slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && (a.Key == slog.TimeKey || a.Key == "stack") {
				return slog.Attr{}
			}
			return redactAttr(groups, a)
		},
	})
}

// consoleIsTerminal reports whether the console sink is an interactive
// terminal, which decides its format. A var so a test need not hold a tty.
var consoleIsTerminal = isTerminalWriter

// isTerminalWriter reports whether w is an interactive terminal.
func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// shipsLogs reports whether this logger forwards records to PostHog Logs. It
// is opt-in: a logger with a file behind it — a real command, not the throwaway
// bootstrap that has no Name — ships only when OGCODE_POSTHOG_LOGS turns it on
// and the floor admits some level.
func (o Options) shipsLogs() bool {
	return o.Name != "" && posthogLogsEnabled() && posthogShipLevel() != LevelOff
}

// posthogShipLevel is the severity floor for shipped records: the configured
// remote level, but never below DefaultRemoteLevel. The file's level does not
// enter into it, so OGCODE_LOG_LEVEL=debug cannot turn on debug shipping.
func posthogShipLevel() slog.Level {
	level := posthogLogsLevel()
	if level < DefaultRemoteLevel {
		level = DefaultRemoteLevel
	}
	return level
}

// describe renders the options for the startup line.
func (o Options) describe() []any {
	args := []any{
		"fileLevel", levelName(o.Level),
		"format", o.Format,
		"console", levelName(o.Console),
		"maxSizeMB", o.Rotate.MaxSize >> 20,
		"maxFiles", o.Rotate.MaxBackups,
		"maxAge", o.Rotate.MaxAge.String(),
		"compress", o.Rotate.Compress,
	}
	if o.shipsLogs() {
		args = append(args, "posthogLogs", true, "posthogLogsLevel", levelName(posthogShipLevel()))
	} else {
		args = append(args, "posthogLogs", false)
	}
	return args
}

func levelName(l slog.Level) string {
	if l == LevelOff {
		return "off"
	}
	return l.String()
}

// Started logs the line that opens a process's section of the log: which
// process, in which directory, which version, under which settings. It makes a
// file shared by restarts (and, rarely, by concurrent processes) readable, and
// names the project a per-project log folder belongs to.
func (l *Logger) Started(ctx context.Context, command, dir, version string, o Options) {
	args := append([]any{"host", hostName(), "dir", dir, "version", version, "pid", os.Getpid()}, o.describe()...)
	l.InfoContext(ctx, command+" started", args...)
}
