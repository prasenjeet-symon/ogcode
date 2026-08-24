// Package config loads ogcode's optional file-based configuration: provider
// base URLs and API keys, so they can live in a committed/shared file
// instead of only environment variables.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// ProviderConfig holds per-provider connection overrides.
type ProviderConfig struct {
	BaseURL string `json:"baseUrl,omitempty"`
	APIKey  string `json:"apiKey,omitempty"`
}

// Config is ogcode's file-based configuration.
type Config struct {
	Providers map[string]ProviderConfig `json:"providers,omitempty"`
}

// envNames maps each provider ID ogcode knows about to the environment
// variables it already reads for that provider elsewhere (server, cli).
var envNames = map[string]struct{ Key, BaseURL string }{
	"anthropic":  {"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL"},
	"openai":     {"OPENAI_API_KEY", "OPENAI_BASE_URL"},
	"openrouter": {"OPENROUTER_API_KEY", ""},
	"ollama":     {"OLLAMA_API_KEY", "OLLAMA_BASE_URL"},
}

// Load reads the global config (~/.config/ogcode/config.json) and the
// project-local config (ogcode.json, found by searching dir and its parents,
// stopping once the repo root — the directory holding .git — has been
// checked), merging them provider-by-provider with project-local values
// taking precedence. Missing or unreadable files are silently skipped;
// Load always returns a usable, non-nil Config.
func Load(dir string) *Config {
	merged := &Config{Providers: map[string]ProviderConfig{}}
	for _, path := range []string{globalPath(), findProjectFile(dir)} {
		c := readFile(path)
		if c == nil {
			continue
		}
		for id, pc := range c.Providers {
			existing := merged.Providers[id]
			if pc.BaseURL != "" {
				existing.BaseURL = pc.BaseURL
			}
			if pc.APIKey != "" {
				existing.APIKey = pc.APIKey
			}
			merged.Providers[id] = existing
		}
	}
	return merged
}

func globalPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "ogcode", "config.json")
}

// findProjectFile searches dir and each parent directory for ogcode.json, so
// ogcode still finds project config when launched from a subdirectory. The
// search stops after checking the repo root (the directory containing .git),
// so it never picks up an unrelated ogcode.json from outside the repo. .git
// is checked for existence only, not that it's a directory: a git worktree
// or submodule has a .git *file* pointing elsewhere, and that still marks a
// repo-root boundary just as well as a normal .git directory does.
func findProjectFile(dir string) string {
	for {
		candidate := filepath.Join(dir, "ogcode.json")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// projectFileTemplate is what EnsureProjectFile writes for a brand new
// ogcode.json. Every field is blank — an unset field behaves identically to
// an absent one everywhere this package is used — but listing all four known
// providers up front makes the file self-documenting to open and edit.
const projectFileTemplate = `{
  "providers": {
    "anthropic": { "baseUrl": "", "apiKey": "" },
    "openai": { "baseUrl": "", "apiKey": "" },
    "openrouter": { "apiKey": "" },
    "ollama": { "baseUrl": "", "apiKey": "" }
  }
}
`

// EnsureProjectFile creates a blank project-local ogcode.json in dir if no
// ogcode.json is found in dir or any of its ancestors (see findProjectFile).
// An existing file — found anywhere in that search — is left untouched, and
// so is dir itself if creation fails for any reason (e.g. a read-only
// filesystem): this is a best-effort convenience, never a requirement for
// startup. Returns the path that was created, or "" if nothing was created.
func EnsureProjectFile(dir string) string {
	if findProjectFile(dir) != "" {
		return ""
	}
	path := filepath.Join(dir, "ogcode.json")
	// O_EXCL: never clobber a file that appeared between the check above and
	// this write (e.g. a second ogcode process started in the same dir).
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := f.WriteString(projectFileTemplate); err != nil {
		return ""
	}
	return path
}

func readFile(path string) *Config {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil
	}
	return &c
}

// ApplyEnv exports the config's provider settings as environment variables,
// for each variable that isn't already set. A real environment variable (or
// one set earlier from a .env file) always wins over the config file.
func (c *Config) ApplyEnv() {
	for id, pc := range c.Providers {
		names, ok := envNames[id]
		if !ok {
			continue
		}
		if pc.APIKey != "" && names.Key != "" {
			setIfUnset(names.Key, pc.APIKey)
		}
		if pc.BaseURL != "" && names.BaseURL != "" {
			setIfUnset(names.BaseURL, pc.BaseURL)
		}
	}
}

func setIfUnset(name, value string) {
	if _, ok := os.LookupEnv(name); !ok {
		os.Setenv(name, value)
	}
}
