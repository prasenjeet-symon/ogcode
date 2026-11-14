package db

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpenLogsMigrations pins the migration logging path: goose's
// per-migration lines flow through the gooseLog adapter into the default slog
// handler, and Open reports the applied count. Losing these lines is what
// silently hid migration history before.
func TestOpenLogsMigrations(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open temp db: %v", err)
	}
	defer d.Close()

	out := buf.String()
	// A fresh database applies the full migration set, so goose's per-migration
	// line (emitted through the adapter as "goose: OK ...") must appear.
	if !strings.Contains(out, "goose: OK") {
		t.Errorf("per-migration goose output missing from log:\n%s", out)
	}
	if !strings.Contains(out, "migrations applied") {
		t.Errorf("applied-migration count line missing from log:\n%s", out)
	}
}
