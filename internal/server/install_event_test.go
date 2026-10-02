package server

import (
	"os"
	"path/filepath"
	"strings"
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
	if got := ReadInstallID(dir); got != "" {
		t.Errorf("ReadInstallID with no file = %q, want \"\"", got)
	}

	if err := os.MkdirAll(filepath.Join(dir, ".ogcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".ogcode", installIDFilename)

	if err := os.WriteFile(path, []byte("abc-123_XYZ\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ReadInstallID(dir); got != "abc-123_XYZ" {
		t.Errorf("ReadInstallID = %q, want %q", got, "abc-123_XYZ")
	}

	// A malformed id is ignored rather than reported.
	if err := os.WriteFile(path, []byte("bad id with spaces"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ReadInstallID(dir); got != "" {
		t.Errorf("ReadInstallID of a malformed id = %q, want \"\"", got)
	}

	if got := ReadInstallID(""); got != "" {
		t.Errorf("ReadInstallID(\"\") = %q, want \"\"", got)
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

// TestEnsureInstallIDPrefersARecordedID pins that an installer-provided id is
// returned untouched and reported as stitched — it is the one a download can be
// joined to, so it may fire the install event.
func TestEnsureInstallIDPrefersARecordedID(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ogcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".ogcode", installIDFilename)
	if err := os.WriteFile(path, []byte("web-uuid-abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	id, stitched := EnsureInstallID(home)
	if id != "web-uuid-abc" {
		t.Errorf("EnsureInstallID = %q, want the recorded %q", id, "web-uuid-abc")
	}
	if !stitched {
		t.Error("stitched = false for an installer id, want true (it may fire the install event)")
	}
}

// TestEnsureInstallIDKeepsALocalID pins that a previously minted id is reused
// and never reported as stitched — a local id has no download to join, so it
// must not fire the install event.
func TestEnsureInstallIDKeepsALocalID(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ogcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ogcode", installIDFilename), []byte("local-01ABC\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	id, stitched := EnsureInstallID(home)
	if id != "local-01ABC" {
		t.Errorf("EnsureInstallID = %q, want the recorded local id", id)
	}
	if stitched {
		t.Error("stitched = true for a local id, want false (it must not fire the install event)")
	}
}

// TestEnsureInstallIDMintsAndSaves pins the fix: with no recorded id one is
// minted, saved so the browser's identity stays stable across restarts, valid by
// ReadInstallID's rules, and NOT stitched.
func TestEnsureInstallIDMintsAndSaves(t *testing.T) {
	home := t.TempDir()
	id, stitched := EnsureInstallID(home)
	if id == "" {
		t.Fatal("EnsureInstallID minted an empty id")
	}
	if stitched {
		t.Error("stitched = true for a freshly minted id, want false")
	}
	if !strings.HasPrefix(id, localInstallIDPrefix) {
		t.Errorf("minted id = %q, want the %q prefix", id, localInstallIDPrefix)
	}
	if !validInstallID(id) {
		t.Errorf("minted id %q fails validInstallID", id)
	}
	if got := ReadInstallID(home); got != id {
		t.Errorf("ReadInstallID after EnsureInstallID = %q, want %q (the minted id was not saved)", got, id)
	}
	// A second call is stable, not a fresh id each boot.
	again, _ := EnsureInstallID(home)
	if again != id {
		t.Errorf("second EnsureInstallID = %q, want %q", again, id)
	}
}

// TestEnsureInstallIDWithoutHome pins that an empty home still yields a usable
// id for this run, even though it cannot be persisted.
func TestEnsureInstallIDWithoutHome(t *testing.T) {
	id, stitched := EnsureInstallID("")
	if id == "" {
		t.Fatal("EnsureInstallID with empty home minted an empty id")
	}
	if stitched {
		t.Error("stitched = true with no home, want false")
	}
}
