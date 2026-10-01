package server

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/version"
)

// Files the installers write under ~/.ogcode: the website's PostHog id, and a
// marker recording that the install has already been reported.
const (
	installIDFilename       = "install-id"
	installReportedFilename = "install-reported"
)

// readInstallID returns the PostHog distinct id the install script recorded when
// the user copied the install command from the website, or "" when there is none.
// A missing file, an empty file, or a malformed id all read as "" — the id is
// only ever used to stitch the download and the first run together, so a bad one
// is simply ignored rather than reported.
func readInstallID(home string) string {
	if home == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".ogcode", installIDFilename))
	if err != nil {
		return ""
	}
	id := strings.TrimSpace(string(data))
	if !validInstallID(id) {
		return ""
	}
	return id
}

// validInstallID accepts the ids the website produces (a PostHog UUID) and keeps
// the property space clean: at most 64 bytes of URL-safe identifier characters.
func validInstallID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// reportInstallOnce emits the one-time ogcode_installed event for a stitched
// install (one where the website handed the id to the installer), recording the
// install channel. It is a no-op when there is no id, no home directory, or no
// capture sink, and it never emits twice: a marker file under ~/.ogcode is
// written on success and checked before every call.
func reportInstallOnce(home, installID string, capture func(event, distinctID string, props map[string]any)) {
	if home == "" || installID == "" || capture == nil {
		return
	}
	marker := filepath.Join(home, ".ogcode", installReportedFilename)
	if _, err := os.Stat(marker); err == nil {
		return
	}
	capture("ogcode_installed", installID, map[string]any{
		"channel": version.DetectInstallChannel(),
	})
	// Best-effort: failing to write the marker costs at most a duplicate event.
	_ = os.MkdirAll(filepath.Dir(marker), 0o755)
	_ = os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
}
