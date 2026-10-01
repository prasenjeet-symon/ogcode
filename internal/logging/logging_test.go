package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testOptions(dir string, console *bytes.Buffer) Options {
	return Options{
		Dir:     dir,
		Name:    "ogcode.log",
		Level:   slog.LevelInfo,
		Format:  "text",
		Console: slog.LevelError,
		Rotate:  RotateOptions{MaxSize: 1 << 20},
		Stderr:  console,
	}
}

// The terminal sees errors only, tersely; the file sees everything at its level.
func TestFileGetsAllTerminalGetsErrors(t *testing.T) {
	dir := t.TempDir()
	var console bytes.Buffer
	l := New(testOptions(dir, &console))
	l.Debug("below the file level")
	l.Info("routine detail", "session", "s1")
	l.Warn("worth a look")
	l.Error("turn failed", "err", "boom", "stack", "goroutine 1 [running]:\nmain.main()")
	l.Close()

	file, err := os.ReadFile(filepath.Join(dir, "ogcode.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"routine detail", "worth a look", "turn failed", "goroutine 1"} {
		if !strings.Contains(string(file), want) {
			t.Errorf("file lacks %q:\n%s", want, file)
		}
	}
	if strings.Contains(string(file), "below the file level") {
		t.Error("file took a record below its level")
	}

	term := console.String()
	if strings.Count(term, "\n") != 1 || !strings.Contains(term, "turn failed") {
		t.Errorf("terminal should show exactly the error, got:\n%s", term)
	}
	if strings.Contains(term, "time=") || strings.Contains(term, "goroutine") {
		t.Errorf("terminal line carries a timestamp or stack:\n%s", term)
	}
}

func TestJSONFormat(t *testing.T) {
	dir := t.TempDir()
	o := testOptions(dir, &bytes.Buffer{})
	o.Format = "json"
	l := New(o)
	l.Info("hello", "n", 3)
	l.Close()
	b, err := os.ReadFile(filepath.Join(dir, "ogcode.log"))
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(b), &rec); err != nil {
		t.Fatalf("file is not one JSON record per line: %v\n%s", err, b)
	}
	if rec["msg"] != "hello" || rec["n"] != float64(3) {
		t.Errorf("unexpected record %v", rec)
	}
}

// A log file that cannot be opened hands its records to the terminal, with the
// reason, instead of dropping them.
func TestFallsBackToTerminal(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var console bytes.Buffer
	l := New(testOptions(filepath.Join(blocker, "logs"), &console))
	l.Info("still visible")
	l.Close()
	if l.Path() != "" {
		t.Errorf("Path() = %q with no file open", l.Path())
	}
	out := console.String()
	if !strings.Contains(out, "file logging unavailable") || !strings.Contains(out, "still visible") {
		t.Errorf("terminal should explain and carry the record:\n%s", out)
	}
}

func TestConsoleOff(t *testing.T) {
	var console bytes.Buffer
	o := testOptions(t.TempDir(), &console)
	o.Console = LevelOff
	l := New(o)
	l.Error("only in the file")
	l.Close()
	if console.Len() != 0 {
		t.Errorf("console is off but printed:\n%s", console.String())
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("OGCODE_LOG_LEVEL", "debug")
	t.Setenv("OGCODE_LOG_FORMAT", "JSON")
	t.Setenv("OGCODE_LOG_CONSOLE", "off")
	t.Setenv("OGCODE_LOG_MAX_SIZE_MB", "25")
	t.Setenv("OGCODE_LOG_MAX_FILES", "3")
	t.Setenv("OGCODE_LOG_MAX_AGE_DAYS", "0")
	t.Setenv("OGCODE_LOG_COMPRESS", "off")
	o := FromEnv("/logs/app-1a2b3c4d", "ogcode.log")
	if o.Level != slog.LevelDebug || o.Format != "json" || o.Console != LevelOff {
		t.Errorf("levels/format not read: %+v", o)
	}
	if o.Dir != "/logs/app-1a2b3c4d" {
		t.Errorf("FromEnv moved the directory it was given: %q", o.Dir)
	}
	want := RotateOptions{MaxSize: 25 << 20, MaxBackups: 3, MaxAge: 0, Compress: false}
	if o.Rotate != want {
		t.Errorf("rotation = %+v, want %+v", o.Rotate, want)
	}
}

func TestFromEnvDefaults(t *testing.T) {
	for _, k := range []string{"OGCODE_LOG_LEVEL", "OGCODE_LOG_FORMAT", "OGCODE_LOG_CONSOLE", "OGCODE_LOG_DIR",
		"OGCODE_LOG_MAX_SIZE_MB", "OGCODE_LOG_MAX_FILES", "OGCODE_LOG_MAX_AGE_DAYS", "OGCODE_LOG_COMPRESS"} {
		t.Setenv(k, "")
	}
	o := FromEnv("/logs/app-1a2b3c4d", "ogcode.log")
	if o.Level != slog.LevelInfo || o.Format != "text" || o.Console != slog.LevelError || o.Dir != "/logs/app-1a2b3c4d" {
		t.Errorf("defaults wrong: %+v", o)
	}
	want := RotateOptions{MaxSize: 10 << 20, MaxBackups: 5, MaxAge: 14 * 24 * time.Hour, Compress: true}
	if o.Rotate != want {
		t.Errorf("rotation = %+v, want %+v", o.Rotate, want)
	}
}
