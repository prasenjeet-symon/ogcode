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
	Version = "v0.19.3"
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
	// Check for package managers in order of likelihood
	switch {
	case isWinget():
		return "winget upgrade ogcode"
	case isHomebrew():
		return "brew upgrade ogcode"
	case isScoop():
		return "scoop update ogcode"
	case isCargo():
		return "cargo install ogcode --force"
	default:
		// For manual installs, use the curl install script
		return "curl -fsSL https://ogcode.xyz/install.sh | sh"
	}
}

// Installation detection helpers.
func isWinget() bool {
	// Check if running on Windows and in a typical winget location
	if runtime.GOOS != "windows" {
		return false
	}
	// Could check registry or winget list output
	// For now, assume if on Windows, winget is the primary method
	return true
}

func isHomebrew() bool {
	// Check common Homebrew paths
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return false
	}
	// Get the executable path to verify it's actually in Homebrew
	execPath, err := os.Executable()
	if err != nil {
		return false
	}
	execPath = filepath.Clean(execPath)
	// Check if binary is in /opt/homebrew or /usr/local (typical Homebrew locations)
	return strings.HasPrefix(execPath, "/opt/homebrew/") || strings.HasPrefix(execPath, "/usr/local/")
}

func isScoop() bool {
	if runtime.GOOS != "windows" {
		return false
	}
	// Scoop typically installs to ~/scoop
	return false // Not yet implemented
}

func isCargo() bool {
	// Check if installed via cargo
	// Could check if binary is in ~/.cargo/bin
	return false // Not yet implemented
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
