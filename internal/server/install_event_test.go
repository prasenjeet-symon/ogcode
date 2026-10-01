package server

import (
	"os"
	"path/filepath"
	"testing"
)

// captureCall records one capture invocation.
type captureCall struct {
	event      string
	distinctID string
	props      map[string]any
}

func TestReadInstallID(t *testing.T) {
	dir := t.TempDir()
	if got := readInstallID(dir); got != "" {
		t.Errorf("readInstallID with no file = %q, want \"\"", got)
	}

	if err := os.MkdirAll(filepath.Join(dir, ".ogcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".ogcode", installIDFilename)

	if err := os.WriteFile(path, []byte("abc-123_XYZ\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readInstallID(dir); got != "abc-123_XYZ" {
		t.Errorf("readInstallID = %q, want %q", got, "abc-123_XYZ")
	}

	// A malformed id is ignored rather than reported.
	if err := os.WriteFile(path, []byte("bad id with spaces"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readInstallID(dir); got != "" {
		t.Errorf("readInstallID of a malformed id = %q, want \"\"", got)
	}

	if got := readInstallID(""); got != "" {
		t.Errorf("readInstallID(\"\") = %q, want \"\"", got)
	}
}

func TestValidInstallID(t *testing.T) {
	valid := []string{"a", "abc-123_XYZ", "0123456789"}
	for _, id := range valid {
		if !validInstallID(id) {
			t.Errorf("validInstallID(%q) = false, want true", id)
		}
	}
	invalid := []string{"", "has space", "has/slash", "has.dot", string(make([]byte, 65))}
	for _, id := range invalid {
		if validInstallID(id) {
			t.Errorf("validInstallID(%q) = true, want false", id)
		}
	}
}

func TestReportInstallOnceEmitsOnceWithTheStampedId(t *testing.T) {
	home := t.TempDir()
	var calls []captureCall
	capture := func(event, distinctID string, props map[string]any) {
		calls = append(calls, captureCall{event, distinctID, props})
	}

	reportInstallOnce(home, "stamped-id", capture)
	if len(calls) != 1 {
		t.Fatalf("capture called %d times, want 1", len(calls))
	}
	if calls[0].event != "ogcode_installed" {
		t.Errorf("event = %q, want %q", calls[0].event, "ogcode_installed")
	}
	// The distinct id is the website's — that is what stitches the funnel.
	if calls[0].distinctID != "stamped-id" {
		t.Errorf("distinctID = %q, want %q", calls[0].distinctID, "stamped-id")
	}
	if _, ok := calls[0].props["channel"]; !ok {
		t.Errorf("props missing channel: %v", calls[0].props)
	}

	// The marker suppresses every later call.
	reportInstallOnce(home, "stamped-id", capture)
	if len(calls) != 1 {
		t.Errorf("capture called %d times after the marker, want 1", len(calls))
	}
}

func TestReportInstallOnceNoOps(t *testing.T) {
	home := t.TempDir()
	calls := 0
	capture := func(string, string, map[string]any) { calls++ }

	reportInstallOnce("", "id", capture)
	reportInstallOnce(home, "", capture)
	reportInstallOnce(home, "id", nil)
	if calls != 0 {
		t.Errorf("capture called %d times for no-op inputs, want 0", calls)
	}
}
