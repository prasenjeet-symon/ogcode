// Package version provides version information and update checking.
package version

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Build info set via ldflags during build.
var (
	Version = "v0.37.2"
	Commit  = "none"
	Date    = "unknown"
)

// GitHub API constants.
const (
	githubOwner = "prasenjeet-symon"
	githubRepo  = "ogcode"
	cacheTTL    = 1 * time.Hour
)

// Info holds current version information.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"goVersion"`
}

// UpdateInfo holds information about available updates.
type UpdateInfo struct {
	LatestVersion   string    `json:"latestVersion"`
	UpdateAvailable bool      `json:"updateAvailable"`
	ReleaseURL      string    `json:"releaseUrl"`
	PublishedAt     time.Time `json:"publishedAt,omitempty"`
	ReleaseNotes    string    `json:"releaseNotes,omitempty"`
	InstallCommand  string    `json:"installCommand,omitempty"`
}

// Combined version and update response.
type Response struct {
	Info
	UpdateInfo
}

// Manager handles version checking with caching.
type Manager struct {
	mu       sync.RWMutex
	cached   *githubRelease
	cachedAt time.Time
	http     *http.Client
}

// githubRelease represents a GitHub release API response.
type githubRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
}

// New creates a new version manager.
func New() *Manager {
	return &Manager{
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// GetInfo returns current version information.
func GetInfo() Info {
	return Info{
		Version:   Version,
		Commit:    Commit,
		Date:      Date,
		GoVersion: runtime.Version(),
	}
}

// CheckUpdate checks for available updates from GitHub releases.
func (m *Manager) CheckUpdate() (*UpdateInfo, error) {
	release, err := m.fetchLatestRelease()
	if err != nil {
		return nil, err
	}

	// Strip 'v' prefix for comparison
	latest := strings.TrimPrefix(release.TagName, "v")
	current := strings.TrimPrefix(Version, "v")

	updateAvailable := compareVersions(latest, current) > 0

	return &UpdateInfo{
		LatestVersion:   release.TagName,
		UpdateAvailable: updateAvailable,
		ReleaseURL:      release.HTMLURL,
		PublishedAt:     release.PublishedAt,
		ReleaseNotes:    summarizeReleaseNotes(release.Body),
		InstallCommand:  detectInstallCommand(),
	}, nil
}

// GetResponse returns combined version and update info.
func (m *Manager) GetResponse() (*Response, error) {
	info := GetInfo()
	update, err := m.CheckUpdate()
	if err != nil {
		// Return version info even if update check fails
		return &Response{Info: info}, nil
	}
	return &Response{
		Info:       info,
		UpdateInfo: *update,
	}, nil
}

// ClearCache invalidates the cached version info.
func (m *Manager) ClearCache() {
	m.mu.Lock()
	m.cached = nil
	m.cachedAt = time.Time{}
	m.mu.Unlock()
}

// GetResponseFallback returns basic version info without update check.
func (m *Manager) GetResponseFallback() *Response {
	return &Response{Info: GetInfo()}
}

// fetchLatestRelease gets the latest release from GitHub API with caching.
func (m *Manager) fetchLatestRelease() (*githubRelease, error) {
	// Check cache
	m.mu.RLock()
	if m.cached != nil && time.Since(m.cachedAt) < cacheTTL {
		cached := m.cached
		m.mu.RUnlock()
		return cached, nil
	}
	m.mu.RUnlock()

	// Fetch from GitHub
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", githubOwner, githubRepo)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", fmt.Sprintf("ogcode/%s", Version))

	resp, err := m.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to check for updates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github API returned %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to parse release info: %w", err)
	}

	// Update cache
	m.mu.Lock()
	m.cached = &release
	m.cachedAt = time.Now()
	m.mu.Unlock()

	return &release, nil
}

// compareVersions compares two semantic version strings.
// Returns 1 if a > b, -1 if a < b, 0 if equal.
func compareVersions(a, b string) int {
	// Strip 'v' prefix first
	a = strings.TrimPrefix(a, "v")
	b = strings.TrimPrefix(b, "v")

	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")

	for i := 0; i < len(aParts) && i < len(bParts); i++ {
		aNum := parseVersionPart(aParts[i])
		bNum := parseVersionPart(bParts[i])
		if aNum > bNum {
			return 1
		}
		if aNum < bNum {
			return -1
		}
	}

	// Check if one has more parts (e.g., 1.2.3 vs 1.2)
	if len(aParts) > len(bParts) {
		return 1
	}
	if len(aParts) < len(bParts) {
		return -1
	}
	return 0
}

// parseVersionPart parses a version component as int, ignoring suffixes like "-beta".
func parseVersionPart(s string) int {
	// Extract leading digits
	var numStr strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			numStr.WriteRune(r)
		} else {
			break
		}
	}
	if numStr.Len() == 0 {
		return 0
	}
	var num int
	fmt.Sscanf(numStr.String(), "%d", &num)
	return num
}

// summarizeReleaseNotes creates a brief summary of release notes.
func summarizeReleaseNotes(notes string) string {
	// Limit length
	if len(notes) > 500 {
		return notes[:500] + "..."
	}
	return notes
}

// detectInstallCommand attempts to detect the best update command for the current installation.
func detectInstallCommand() string {
	return detectInstallCommandFor(runtime.GOOS, currentExecPath(), os.Getenv)
}

// currentExecPath returns the running binary's path with symlinks resolved, or
// "" when the path cannot be determined. The resolved path is what carries the
// install-channel fingerprint: package managers symlink the binary into a
// prefix bin directory from a path they own behind it, while the install
// scripts drop a plain file.
func currentExecPath() string {
	execPath, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(execPath); err == nil {
		execPath = resolved
	}
	return filepath.Clean(execPath)
}

// detectInstallCommandFor is the testable core of detectInstallCommand. Checks
// are ordered narrow-first: scoop, cargo, and install.ps1 are recognized by
// directories only those channels write, Homebrew by the Cellar path behind
// its symlinks, and the two catch-alls (winget on Windows, the install.sh
// script everywhere else) come last.
func detectInstallCommandFor(goos, execPath string, getenv func(string) string) string {
	switch {
	case isScoopInstall(goos, execPath, getenv):
		return "scoop update ogcode"
	case isHomebrewInstall(goos, execPath):
		return "brew upgrade ogcode"
	case isCargoInstall(execPath, getenv):
		return "cargo install ogcode --force"
	case isScriptInstall(goos, execPath, getenv):
		return "irm https://ogcode.xyz/install.ps1 | iex"
	case goos == "windows":
		// winget is the documented default Windows channel, but it writes no
		// install fingerprint of its own, so it claims any Windows install the
		// narrower checks did not recognize.
		return "winget upgrade ogcode"
	default:
		// For manual installs, use the curl install script
		return "curl -fsSL https://ogcode.xyz/install.sh | sh"
	}
}

// isScoopInstall reports whether execPath sits inside a scoop root. Scoop shims
// live in <root>\shims and app payloads in <root>\apps\<app>\<version>, and the
// process runs the shim, so the root prefix is the fingerprint.
func isScoopInstall(goos, execPath string, getenv func(string) string) bool {
	if goos != "windows" {
		return false
	}
	for _, root := range scoopRoots(getenv) {
		if underDir(execPath, root) {
			return true
		}
	}
	return false
}

// scoopRoots lists the directories a scoop install can live under: $env:SCOOP
// and $env:SCOOP_GLOBAL override the default $env:USERPROFILE\scoop.
func scoopRoots(getenv func(string) string) []string {
	var roots []string
	for _, key := range []string{"SCOOP", "SCOOP_GLOBAL"} {
		if root := getenv(key); root != "" {
			roots = append(roots, root)
		}
	}
	if home := getenv("USERPROFILE"); home != "" {
		roots = append(roots, filepath.Join(home, "scoop"))
	}
	return roots
}

// isHomebrewInstall reports whether execPath is a Homebrew install. Homebrew
// symlinks each formula binary from Cellar/<formula>/<version>/bin into its
// prefix bin directory (/opt/homebrew/bin on Apple Silicon, /usr/local/bin for
// an Intel install, /home/linuxbrew/.linuxbrew/bin on Linux), so the Cellar
// directory in the resolved path is the fingerprint. The curl install script
// also drops a plain binary into /usr/local/bin — the shared prefix is exactly
// why the Cellar path, not the prefix, is what may match.
func isHomebrewInstall(goos, execPath string) bool {
	if goos != "darwin" && goos != "linux" {
		return false
	}
	if strings.Contains(execPath, string(filepath.Separator)+"Cellar"+string(filepath.Separator)) {
		return true
	}
	// Installs directly under the Apple Silicon prefix with no Cellar behind
	// them are still Homebrew territory; the install scripts never write there.
	return strings.HasPrefix(execPath, "/opt/homebrew/")
}

// isCargoInstall reports whether execPath is inside cargo's bin directory.
// cargo installs plain files (no symlinks) into $CARGO_HOME/bin, by default
// ~/.cargo/bin.
func isCargoInstall(execPath string, getenv func(string) string) bool {
	return underDir(execPath, cargoBinDir(getenv))
}

// cargoBinDir returns cargo's bin directory, or "" when no home directory can
// be determined.
func cargoBinDir(getenv func(string) string) string {
	if cargoHome := getenv("CARGO_HOME"); cargoHome != "" {
		return filepath.Join(cargoHome, "bin")
	}
	home := homeDir(getenv)
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".cargo", "bin")
}

// isScriptInstall reports whether execPath sits in the directory the Windows
// install.ps1 one-liner uses ($env:LOCALAPPDATA\ogcode). That script replaces
// the whole install directory, so updating is the same one-liner again.
func isScriptInstall(goos, execPath string, getenv func(string) string) bool {
	if goos != "windows" {
		return false
	}
	localAppData := getenv("LOCALAPPDATA")
	if localAppData == "" {
		return false
	}
	return underDir(execPath, filepath.Join(localAppData, "ogcode"))
}

// homeDir mirrors the HOME (unix) then USERPROFILE (Windows) lookup os
// packages make, read through getenv so tests can inject the environment.
func homeDir(getenv func(string) string) string {
	if home := getenv("HOME"); home != "" {
		return home
	}
	return getenv("USERPROFILE")
}

// underDir reports whether path is inside dir (dir itself does not count). It
// compares raw strings rather than going through filepath, because the callers
// pass Windows-style paths that must mean the same thing whichever GOOS this
// code is compiled for: backslashes are normalised to slashes and the
// comparison is case-insensitive, since Windows paths and the
// environment-provided roots may be spelled with any casing.
func underDir(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	path = strings.ReplaceAll(strings.ToLower(path), "\\", "/")
	dir = strings.ReplaceAll(strings.ToLower(dir), "\\", "/")
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" || !strings.HasPrefix(path, dir) {
		return false
	}
	return strings.HasPrefix(path[len(dir):], "/")
}

// IsDev returns true if running a development build.
func IsDev() bool {
	return Version == "dev" || Version == ""
}

// IsNewerRelease reports whether otherVersion is newer than current version.
func IsNewerRelease(otherVersion string) bool {
	latest := strings.TrimPrefix(otherVersion, "v")
	current := strings.TrimPrefix(Version, "v")
	return compareVersions(latest, current) > 0
}
