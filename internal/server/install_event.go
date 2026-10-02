package server

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	idpkg "github.com/prasenjeet-symon/ogcode/internal/id"
	"github.com/prasenjeet-symon/ogcode/internal/version"
)

// Files the installers write under ~/.ogcode: the website's PostHog id, and a
// marker recording that the install has already been reported.
const (
	installIDFilename       = "install-id"
	installReportedFilename = "install-reported"
)

// localInstallIDPrefix marks an install id this process minted itself, as
// opposed to one the website handed to the install script. A locally minted id
// was never part of a download, so it must not fire the ogcode_installed event.
const localInstallIDPrefix = "local-"

// ReadInstallID returns the PostHog distinct id the install script recorded when
// the user copied the install command from the website, or "" when there is none.
// A missing file, an empty file, or a malformed id all read as "" — the id is
// only ever used to stitch the download and the first run together, so a bad one
// is simply ignored rather than reported.
func ReadInstallID(home string) string {
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

// EnsureInstallID returns the machine's PostHog distinct id, minting and saving
// a local one when the install script recorded none. stitched reports whether
// the id came from an installer (the website wrote it), which is what decides
// whether the one-time ogcode_installed event may fire — a locally minted id
// marks a machine with no download to join, so it must not report an install.
//
// A minted id is persisted best-effort to ~/.ogcode/install-id so the browser's
// anonymous identity stays stable across restarts; if it cannot be written, the
// id is still returned and used for this run.
//
// The web UI adopts whatever id /api/config reports (web/src/lib/posthog.ts), so
// on an install that had no recorded id — which used to leave the browser on a
// random id of its own in localStorage — the first load after upgrading moves
// the browser onto the minted id. That browser's analytics continue under a new
// PostHog person from then on; the switch happens once and the two persons are
// not merged.
func EnsureInstallID(home string) (id string, stitched bool) {
	if recorded := ReadInstallID(home); recorded != "" {
		return recorded, !strings.HasPrefix(recorded, localInstallIDPrefix)
	}
	minted := idpkg.NewInstallID()
	if home != "" {
		path := filepath.Join(home, ".ogcode", installIDFilename)
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, []byte(minted+"\n"), 0o600)
	}
	return minted, false
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
