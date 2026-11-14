// Package version provides version information and update checking.
package version

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Build info set via ldflags during build.
var (
	Version = "v0.59.2"
	Commit  = "none"
	Date    = "unknown"
)

// GitHub API constants.
const (
	githubOwner = "prasenjeet-symon"
	githubRepo  = "ogcode"
	cacheTTL    = 1 * time.Hour
	// failedCheckTTL is how long a failed check is remembered before GitHub is
	// asked again. The web UI asks for the version on every page load, so
	// without it an offline or rate-limited client would walk into the client
	// timeout once per page.
	failedCheckTTL = 1 * time.Minute
	// releaseNotesLimit caps the release-notes excerpt returned to clients.
	releaseNotesLimit = 500
)

// defaultAPIBase is the GitHub API root. Tests point a Manager elsewhere.
const defaultAPIBase = "https://api.github.com"

// Info holds current version information.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"goVersion"`
}

// UpdateInfo holds information about available updates.
type UpdateInfo struct {
	LatestVersion   string     `json:"latestVersion"`
	UpdateAvailable bool       `json:"updateAvailable"`
	ReleaseURL      string     `json:"releaseUrl"`
	PublishedAt     *time.Time `json:"publishedAt,omitempty"`
	ReleaseNotes    string     `json:"releaseNotes,omitempty"`
	InstallCommand  string     `json:"installCommand,omitempty"`
}

// Combined version and update response.
type Response struct {
	Info
	UpdateInfo
	// UpdateError explains why the update check failed. When it is set the
	// update fields hold the last release fetched successfully, or are empty
	// if none ever was. It is omitted when the check succeeded.
	UpdateError string `json:"updateError,omitempty"`
}

// Manager handles version checking with caching.
type Manager struct {
	mu       sync.RWMutex
	cached   *githubRelease
	cachedAt time.Time
	lastErr  error
	failedAt time.Time
	http     *http.Client
	apiBase  string
}

// Option configures a Manager. The defaults talk to GitHub; the options exist
// so tests can point a Manager at a fake server.
type Option func(*Manager)

// WithAPIBase overrides the GitHub API root the update check reads (for tests).
func WithAPIBase(base string) Option {
	return func(m *Manager) { m.apiBase = strings.TrimSuffix(base, "/") }
}

// WithHTTPClient overrides the HTTP client used for the update check.
func WithHTTPClient(client *http.Client) Option {
	return func(m *Manager) { m.http = client }
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
func New(opts ...Option) *Manager {
	m := &Manager{
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
		apiBase: defaultAPIBase,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
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

// CheckUpdate checks for available updates from GitHub releases. When the
// fetch fails it still returns the last release it knows about — however old —
// alongside the error, so callers can show stale data and say why it is stale.
func (m *Manager) CheckUpdate(ctx context.Context) (*UpdateInfo, error) {
	release, err := m.fetchLatestRelease(ctx)
	if release == nil {
		return nil, err
	}

	// Strip 'v' prefix for comparison
	latest := strings.TrimPrefix(release.TagName, "v")
	current := strings.TrimPrefix(Version, "v")

	updateAvailable := compareVersions(latest, current) > 0

	info := &UpdateInfo{
		LatestVersion:   release.TagName,
		UpdateAvailable: updateAvailable,
		ReleaseURL:      release.HTMLURL,
		ReleaseNotes:    summarizeReleaseNotes(release.Body),
		InstallCommand:  detectInstallCommand(),
	}
	if !release.PublishedAt.IsZero() {
		info.PublishedAt = &release.PublishedAt
	}
	return info, err
}

// GetResponse returns combined version and update info. The response is never
// nil: the installed version is local, so a failed update check is reported in
// UpdateError (and in the returned error) rather than by dropping the fields
// the caller can still answer for.
func (m *Manager) GetResponse(ctx context.Context) (*Response, error) {
	response := &Response{Info: GetInfo()}

	update, err := m.CheckUpdate(ctx)
	if update != nil {
		response.UpdateInfo = *update
	}
	if err != nil {
		response.UpdateError = err.Error()
		return response, err
	}
	return response, nil
}

// ClearCache marks the cached release stale, so the next check fetches from
// GitHub. The release itself is kept as the fallback a failed check serves.
func (m *Manager) ClearCache() {
	m.mu.Lock()
	m.cachedAt = time.Time{}
	m.lastErr = nil
	m.failedAt = time.Time{}
	m.mu.Unlock()
}

// fetchLatestRelease gets the latest release from GitHub API with caching. On
// failure it returns the last release fetched successfully, which may be nil
// when none ever was.
func (m *Manager) fetchLatestRelease(ctx context.Context) (*githubRelease, error) {
	m.mu.RLock()
	cached := m.cached
	fresh := cached != nil && time.Since(m.cachedAt) < cacheTTL
	lastErr, failedAt := m.lastErr, m.failedAt
	m.mu.RUnlock()

	if fresh {
		return cached, nil
	}
	// A check that just failed is retried at most once per failedCheckTTL, so
	// an offline or rate-limited client does not pay the timeout on every
	// request.
	if lastErr != nil && time.Since(failedAt) < failedCheckTTL {
		return cached, lastErr
	}

	release, err := m.fetchRemote(ctx)
	if err != nil {
		// A cancelled context says nothing about GitHub, so it is not
		// remembered as the outcome of a check.
		if ctx.Err() == nil {
			m.mu.Lock()
			m.lastErr = err
			m.failedAt = time.Now()
			m.mu.Unlock()
		}
		return cached, err
	}

	m.mu.Lock()
	m.cached = release
	m.cachedAt = time.Now()
	m.lastErr = nil
	m.failedAt = time.Time{}
	m.mu.Unlock()

	return release, nil
}

// fetchRemote performs the GitHub API request, bypassing the cache.
func (m *Manager) fetchRemote(ctx context.Context) (*githubRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", m.apiBase, githubOwner, githubRepo)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
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

// summarizeReleaseNotes creates a brief summary of release notes. The cut is
// made on rune boundaries, so a note with an em dash or emoji does not end in a
// split UTF-8 sequence (which JSON encoding would replace with U+FFFD).
func summarizeReleaseNotes(notes string) string {
	if utf8.RuneCountInString(notes) <= releaseNotesLimit {
		return notes
	}
	return string([]rune(notes)[:releaseNotesLimit]) + "..."
}

// detectInstallCommand attempts to detect the best update command for the current installation.
func detectInstallCommand() string {
	return detectInstallCommandFor(runtime.GOOS, currentExecPath(), os.Getenv)
}

// DetectInstallChannel labels how the running binary was installed, as one of a
// small fixed set of values. It is meant for analytics: the label is deliberately
// low-cardinality and never carries a machine-specific path.
func DetectInstallChannel() string {
	return installChannelFor(detectInstallCommand())
}

// installChannelFor maps an update command (one of the exact strings
// detectInstallCommandFor returns) to its install-channel label. An unrecognized
// command yields "unknown" rather than an empty string, so the property is always
// present on the event.
func installChannelFor(command string) string {
	switch command {
	case "scoop update ogcode":
		return "scoop"
	case "brew upgrade ogcode":
		return "homebrew"
	case "cargo install ogcode --force":
		return "cargo"
	case "irm https://ogcode.in/install.ps1 | iex":
		return "install-ps1"
	case "winget upgrade ogcode":
		return "winget"
	case "curl -fsSL https://ogcode.in/install.sh | sh":
		return "install-sh"
	default:
		return "unknown"
	}
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
		return "irm https://ogcode.in/install.ps1 | iex"
	case goos == "windows":
		// winget is the documented default Windows channel, but it writes no
		// install fingerprint of its own, so it claims any Windows install the
		// narrower checks did not recognize.
		return "winget upgrade ogcode"
	default:
		// For manual installs, use the curl install script
		return "curl -fsSL https://ogcode.in/install.sh | sh"
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
