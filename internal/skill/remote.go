package skill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Limits on what a remote index may pull down. A skills URL is a third party:
// these bound what a hostile or simply broken manifest can do to the disk and
// to the turn that is waiting on it.
const (
	remoteTimeout    = 20 * time.Second
	maxIndexBytes    = 1 << 20  // 1 MB manifest
	maxRemoteFile    = 1 << 20  // 1 MB per shipped file
	maxRemoteTotal   = 20 << 20 // 20 MB per index
	maxRemoteSkills  = 100
	maxFilesPerSkill = 64
)

// Manifest is the index.json a skills URL serves.
//
//	{
//	  "version": "2026-08-01",
//	  "skills": [
//	    {"name": "git-release", "files": ["SKILL.md", "scripts/release.sh"]}
//	  ]
//	}
//
// Version identifies the contents: a cached copy of a version is reused as-is,
// so publishing a change means publishing a new version string.
type Manifest struct {
	Version string          `json:"version"`
	Skills  []ManifestSkill `json:"skills"`
}

// ManifestSkill describes one skill in a Manifest. Name and Description are
// advisory — the downloaded SKILL.md is parsed like any other, and its
// frontmatter is what ogcode actually uses, so a manifest cannot describe a
// skill as one thing and ship another.
type ManifestSkill struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Files       []string `json:"files"`
}

// fetchRemote resolves a skills URL to local skill directories, downloading
// them into the cache on first use.
//
// Refresh is version-keyed: a manifest version already present in the cache is
// used without downloading a single file. A new version is assembled in a
// staging directory and moved into place with one rename, so a failure halfway
// through leaves the previous version intact rather than a half-written one.
//
// When the network is unavailable the newest cached version is used instead. A
// skills URL that cannot be reached should cost the agent the newest skills, not
// every skill it had yesterday.
func fetchRemote(ctx context.Context, client *http.Client, cacheRoot, rawURL string) ([]string, error) {
	base, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("skills url %q: %w", rawURL, err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("skills url %q: only http and https are supported", rawURL)
	}

	root := filepath.Join(cacheRoot, urlKey(rawURL))

	manifest, err := fetchManifest(ctx, client, rawURL)
	if err != nil {
		if dirs, ok := newestCached(root); ok {
			return dirs, fmt.Errorf("skills url %q unreachable, using cached copy: %w", rawURL, err)
		}
		return nil, fmt.Errorf("skills url %q: %w", rawURL, err)
	}

	versionDir := filepath.Join(root, versionKey(manifest.Version))
	if entries, err := os.ReadDir(versionDir); err == nil && len(entries) > 0 {
		return childDirs(versionDir), nil
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("skills cache %s: %w", root, err)
	}
	staging, err := os.MkdirTemp(root, ".staging-")
	if err != nil {
		return nil, fmt.Errorf("skills cache %s: %w", root, err)
	}
	defer os.RemoveAll(staging) // no-op once the rename below has moved it

	if err := download(ctx, client, base, manifest, staging); err != nil {
		if dirs, ok := newestCached(root); ok {
			return dirs, fmt.Errorf("skills url %q download failed, using cached copy: %w", rawURL, err)
		}
		return nil, fmt.Errorf("skills url %q: %w", rawURL, err)
	}

	if err := os.Rename(staging, versionDir); err != nil {
		// A concurrent ogcode process may have published the same version first,
		// which is a success for this one too.
		if entries, statErr := os.ReadDir(versionDir); statErr == nil && len(entries) > 0 {
			return childDirs(versionDir), nil
		}
		return nil, fmt.Errorf("skills cache %s: %w", versionDir, err)
	}
	return childDirs(versionDir), nil
}

// fetchManifest downloads and validates the index.json at rawURL.
func fetchManifest(ctx context.Context, client *http.Client, rawURL string) (*Manifest, error) {
	body, err := get(ctx, client, rawURL, maxIndexBytes)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("parse index.json: %w", err)
	}
	if len(m.Skills) == 0 {
		return nil, fmt.Errorf("index.json lists no skills")
	}
	if len(m.Skills) > maxRemoteSkills {
		return nil, fmt.Errorf("index.json lists %d skills, over the %d limit", len(m.Skills), maxRemoteSkills)
	}
	return &m, nil
}

// download writes every skill in the manifest into staging.
func download(ctx context.Context, client *http.Client, base *url.URL, m *Manifest, staging string) error {
	total := 0
	for _, ms := range m.Skills {
		if !namePattern.MatchString(ms.Name) || len(ms.Name) > MaxNameLen {
			return fmt.Errorf("skill name %q in index.json is not a valid name", ms.Name)
		}
		files := ms.Files
		if len(files) == 0 {
			files = []string{Filename}
		}
		if len(files) > maxFilesPerSkill {
			return fmt.Errorf("skill %q lists %d files, over the %d limit", ms.Name, len(files), maxFilesPerSkill)
		}
		if !containsFile(files, Filename) {
			files = append([]string{Filename}, files...)
		}

		dir := filepath.Join(staging, ms.Name)
		for _, rel := range files {
			clean, err := safeRelPath(rel)
			if err != nil {
				return fmt.Errorf("skill %q: %w", ms.Name, err)
			}
			fileURL, err := base.Parse(path.Join(ms.Name, clean))
			if err != nil {
				return fmt.Errorf("skill %q: resolve %s: %w", ms.Name, clean, err)
			}
			body, err := get(ctx, client, fileURL.String(), maxRemoteFile)
			if err != nil {
				return fmt.Errorf("skill %q: %s: %w", ms.Name, clean, err)
			}
			total += len(body)
			if total > maxRemoteTotal {
				return fmt.Errorf("index exceeds the %d byte total download limit", maxRemoteTotal)
			}
			dest := filepath.Join(dir, filepath.FromSlash(clean))
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(dest, body, 0o644); err != nil {
				return err
			}
		}
		// Parse what was written before it is published, so a manifest whose
		// SKILL.md is malformed fails here rather than silently contributing a
		// skill that never loads.
		if _, err := loadFrom(dir, SourceRemote); err != nil {
			return err
		}
	}
	return nil
}

// get fetches a URL, refusing a body over limit.
func get(ctx context.Context, client *http.Client, rawURL string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	// Read one byte past the limit so an oversized body is reported rather than
	// silently truncated into a file that then fails to parse for the wrong
	// reason.
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("body exceeds the %d byte limit", limit)
	}
	return body, nil
}

// safeRelPath validates a manifest file path. The manifest is written by
// whoever hosts the URL, so a path in it is untrusted input: an absolute path
// or a "../" escape would write outside the cache directory entirely.
func safeRelPath(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", fmt.Errorf("empty file path")
	}
	if strings.ContainsAny(rel, "\\:") || strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("file path %q must be relative", rel)
	}
	clean := path.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("file path %q escapes the skill directory", rel)
	}
	return clean, nil
}

func containsFile(files []string, name string) bool {
	for _, f := range files {
		if path.Clean(strings.TrimSpace(f)) == name {
			return true
		}
	}
	return false
}

// urlKey is a filesystem-safe, stable directory name for a skills URL.
func urlKey(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(sum[:8])
}

// versionKey makes a manifest version safe to use as a directory name.
//
// A version is a string the remote chose and may contain anything, so unsafe
// characters are replaced. That collapsing can map two different versions onto
// the same name — "1.0/beta" and "1.0-beta" both become "1.0-beta" — and the
// second would then be served the first's cached contents with nothing to
// signal it. A short digest of the raw version is appended so distinct versions
// stay distinct, while the readable part survives for anyone looking in the
// cache directory.
func versionKey(version string) string {
	version = strings.TrimSpace(version)
	sum := sha256.Sum256([]byte(version))
	digest := hex.EncodeToString(sum[:4])

	var b strings.Builder
	for _, r := range version {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	key := strings.Trim(b.String(), ".-")
	if key == "" {
		key = "unversioned"
	}
	return key + "-" + digest
}

// newestCached returns the skill directories of the most recently written
// cached version under root, for use when the network is unavailable.
func newestCached(root string) ([]string, bool) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, false
	}
	var best string
	var bestTime time.Time
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".staging-") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if best == "" || info.ModTime().After(bestTime) {
			best, bestTime = filepath.Join(root, e.Name()), info.ModTime()
		}
	}
	if best == "" {
		return nil, false
	}
	dirs := childDirs(best)
	return dirs, len(dirs) > 0
}

// childDirs lists the skill directories directly under a cached version.
func childDirs(versionDir string) []string {
	entries, err := os.ReadDir(versionDir)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(versionDir, e.Name()))
		}
	}
	sort.Strings(dirs)
	return dirs
}
