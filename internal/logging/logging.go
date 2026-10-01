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
)

// LevelOff is a threshold no record reaches: the sink is silent.
const LevelOff = slog.Level(math.MaxInt32)

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
	if l, ok := parseLevel(os.Getenv("OGCODE_LOG_LEVEL")); ok && l != LevelOff {
		o.Level = l
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("OGCODE_LOG_FORMAT")), "json") {
		o.Format = "json"
	}
	if l, ok := parseLevel(os.Getenv("OGCODE_LOG_CONSOLE")); ok {
		o.Console = l
	}
	if n, ok := envInt("OGCODE_LOG_MAX_SIZE_MB"); ok && n > 0 {
		o.Rotate.MaxSize = int64(n) << 20
	}
	if n, ok := envInt("OGCODE_LOG_MAX_FILES"); ok && n >= 0 {
		o.Rotate.MaxBackups = n
	}
	if n, ok := envInt("OGCODE_LOG_MAX_AGE_DAYS"); ok && n >= 0 {
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

func envInt(name string) (int, bool) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	return n, err == nil
}

// Logger is a configured process logger and the file behind it.
type Logger struct {
	*slog.Logger
	file *RotatingFile
}

// Path is the active log file, or "" when there is none.
func (l *Logger) Path() string {
	if l.file == nil {
		return ""
	}
	return l.file.Path()
}

// Close flushes nothing (writes are unbuffered) but closes the file and waits
// for pending rotation housekeeping.
func (l *Logger) Close() error {
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
	if o.Name != "" {
		f, err := OpenRotating(filepath.Join(o.Dir, o.Name), o.Rotate)
		if err == nil {
			l.file = f
			handlers = append(handlers, fileHandler(f, o))
		} else {
			openErr = err
			o.Console = min(o.Console, o.Level)
		}
	}
	if o.Console != LevelOff {
		handlers = append(handlers, consoleHandler(stderr, o.Console))
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

// consoleHandler writes terse lines for a person watching the terminal: no
// timestamp (they are watching it happen) and no stack traces (those stay in
// the file, where a panic's full record belongs).
func consoleHandler(w io.Writer, level slog.Level) slog.Handler {
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

// describe renders the options for the startup line.
func (o Options) describe() []any {
	return []any{
		"fileLevel", o.Level.String(),
		"format", o.Format,
		"console", levelName(o.Console),
		"maxSizeMB", o.Rotate.MaxSize >> 20,
		"maxFiles", o.Rotate.MaxBackups,
		"maxAge", o.Rotate.MaxAge.String(),
		"compress", o.Rotate.Compress,
	}
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
	args := append([]any{"dir", dir, "version", version, "pid", os.Getpid()}, o.describe()...)
	l.InfoContext(ctx, command+" started", args...)
}
