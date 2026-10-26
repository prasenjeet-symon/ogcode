package nativehost

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// manifestName is the manifest file's name inside each browser's
// NativeMessagingHosts directory — the host's name plus .json, per Chrome's
// native messaging docs.
const manifestName = NativeHostName + ".json"

// nativeHostDirName is the subfolder of every supported browser's profile
// root that holds native-messaging manifests.
const nativeHostDirName = "NativeMessagingHosts"

// errWindowsUnsupported is Register/Uninstall's answer on Windows: Chrome
// there stores native-messaging hosts under the Windows registry, which is a
// different (registry-based) installation this installer does not manage.
var errWindowsUnsupported = errors.New("native host registration is not supported on Windows (registration is registry-based)")

// nativeManifest is the on-disk manifest shape Chrome reads.
type nativeManifest struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Path           string   `json:"path"`
	Type           string   `json:"type"`
	AllowedOrigins []string `json:"allowed_origins"`
}

// manifestDirsFor lists the directories the manifest belongs in. A package
// var so tests redirect them into a t.TempDir() tree instead of the real
// browser profile folders.
var manifestDirsFor = defaultManifestDirs

// defaultManifestDirs lists every Chromium-based browser this binary supports,
// macOS paths first, then the Linux ones. Registering only ever writes into a
// dir whose parent profile folder already exists (Register's check), so the
// uninstalled browsers in the list cost nothing to attempt.
func defaultManifestDirs() []string {
	roots := []string{
		// macOS — Chrome and every fork keeps its profile under
		// ~/Library/Application Support.
		"Library/Application Support/Google/Chrome",
		"Library/Application Support/Chromium",
		"Library/Application Support/BraveSoftware/Brave-Browser",
		"Library/Application Support/Microsoft Edge",
		"Library/Application Support/Vivaldi",
		"Library/Application Support/Arc/User Data",
		// Linux — Chrome's own convention under ~/.config.
		".config/google-chrome",
		".config/chromium",
		".config/BraveSoftware/Brave-Browser",
		".config/microsoft-edge",
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil // no home, no browser dirs; the caller installs nothing
	}
	dirs := make([]string, 0, len(roots))
	for _, root := range roots {
		dirs = append(dirs, filepath.Join(home, filepath.FromSlash(root), nativeHostDirName))
	}
	return dirs
}

// Register writes the native-messaging manifest naming executablePath into
// every supported browser's NativeMessagingHosts directory whose browser
// profile folder exists — a browser the machine has never run installs
// nothing. It is idempotent: the same inputs rewrite the same file. Existing
// manifests of this host are overwritten; another host's file is left alone.
func Register(executablePath string, extensionIDs []string) error {
	if runtime.GOOS == "windows" {
		return errWindowsUnsupported
	}
	ids := dedup(extensionIDs)
	if len(ids) == 0 {
		return errors.New("native host: refusing to register without an extension id to allowlist")
	}
	exe, err := filepath.Abs(executablePath)
	if err != nil {
		return fmt.Errorf("native host: resolving the executable path: %w", err)
	}
	data, err := json.MarshalIndent(nativeManifest{
		Name:           NativeHostName,
		Description:    "ogcode browser takeover host",
		Path:           exe,
		Type:           "stdio",
		AllowedOrigins: originsFor(ids),
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("native host: encoding the manifest: %w", err)
	}
	data = append(data, '\n')

	for _, dir := range manifestDirsFor() {
		parent := filepath.Dir(dir)
		if info, err := os.Stat(parent); err != nil || !info.IsDir() {
			continue // the browser this profile belongs to was never run
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("native host: creating %s: %w", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, manifestName), data, 0o644); err != nil {
			return fmt.Errorf("native host: writing %s: %w", dir, err)
		}
	}
	return nil
}

// Uninstall removes every manifest this host wrote, wherever it found one. It
// is idempotent — running it with nothing installed succeeds.
func Uninstall() error {
	if runtime.GOOS == "windows" {
		return errWindowsUnsupported
	}
	for _, dir := range manifestDirsFor() {
		path := filepath.Join(dir, manifestName)
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("native host: removing %s: %w", path, err)
		}
	}
	return nil
}

// ManifestPaths lists the manifest file paths registration would write right
// now — the supported browsers whose profile folder exists — so the CLI can
// report exactly what an install touches (and an uninstall clean up).
func ManifestPaths() []string {
	var paths []string
	for _, dir := range manifestDirsFor() {
		if info, err := os.Stat(filepath.Dir(dir)); err != nil || !info.IsDir() {
			continue
		}
		paths = append(paths, filepath.Join(dir, manifestName))
	}
	return paths
}

// originsFor renders each id as the chrome-extension://<id>/ origin Chrome
// puts in an allowed_origins list.
func originsFor(ids []string) []string {
	origins := make([]string, 0, len(ids))
	for _, id := range ids {
		origins = append(origins, "chrome-extension://"+id+"/")
	}
	return origins
}

// UnpackedExtensionDir names the project folder the companion extension lives
// in while it is built, before the store publishes an id for it.
const UnpackedExtensionDir = "browser-extension"

// ExtensionIDsEnv is the process-wide env var of extra extension ids to
// allowlist, comma-separated.
const ExtensionIDsEnv = "OGCODE_EXTENSION_IDS"

// UnpackedIDFor returns the id Chrome would assign to projectDir's unpacked
// companion, and whether such an extension (a manifest.json) is present there
// at all — the two facts the CLI's install hint names.
func UnpackedIDFor(projectDir string) (string, bool) {
	if projectDir == "" {
		return "", false
	}
	dir := filepath.Join(projectDir, UnpackedExtensionDir)
	if info, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil || info.IsDir() {
		return "", false
	}
	return ExtensionIDForPath(dir), true
}

// installEnv is the kill switch on the server's automatic registration.
const installEnv = "OGCODE_NATIVE_HOST_INSTALL"

// DisabledByEnv reports whether OGCODE_NATIVE_HOST_INSTALL is set to a falsey
// value (0/false/no/off). It gates only the server's boot-time auto-install:
// an explicit `ogcode native-host install` runs regardless, and the way to
// refuse manifests for good is `native-host uninstall`.
func DisabledByEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(installEnv))) {
	case "0", "false", "no", "off":
		return true
	}
	return false
}

// CandidatesFor lists the extension ids projectDir is entitled to allowlist:
// the unpacked browser-extension/ directory when the project ships one, ids
// named in OGCODE_EXTENSION_IDS, and the published web store id once the
// extension ships. Deduplicated in order, empty ids dropped.
func CandidatesFor(projectDir string) []string {
	var ids []string
	if id, ok := UnpackedIDFor(projectDir); ok {
		ids = append(ids, id)
	}
	for _, id := range strings.Split(os.Getenv(ExtensionIDsEnv), ",") {
		ids = append(ids, strings.TrimSpace(id))
	}
	if WebStoreExtensionID != "" {
		ids = append(ids, WebStoreExtensionID)
	}
	return dedup(ids)
}

// DedupeExtensionIDs keeps each id's first occurrence, in order, dropping
// empty strings — the shape CandidatesFor returns and Register accepts.
func DedupeExtensionIDs(ids []string) []string {
	return dedup(ids)
}

// EnsureRegistered is the server's boot-time install: when this project
// entitles any extension id, keep every installed browser's manifest pointing
// at this ogcode executable, so a fresh extension install pairs itself
// without a manual paste. Idempotent — repeated boots rewrite the same bytes
// — and a no-op when there is nothing to allowlist. OGCODE_NATIVE_HOST_INSTALL
// falsey turns the whole thing off for a deployment that wants the manifests
// untouched.
func EnsureRegistered(projectDir string) error {
	if DisabledByEnv() {
		slog.Debug("native host: registration skipped", "reason", installEnv+" is falsey")
		return nil
	}
	ids := CandidatesFor(projectDir)
	if len(ids) == 0 {
		slog.Debug("native host: no extension id to allowlist; no manifest written",
			"expected", filepath.Join(projectDir, UnpackedExtensionDir, "manifest.json"),
			"or_env", ExtensionIDsEnv)
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("native host: locating the executable: %w", err)
	}
	return Register(exe, ids)
}

// dedup keeps the first occurrence of each id, in order, dropping empties.
func dedup(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
