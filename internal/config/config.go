// Package config loads ogcode's optional file-based configuration: provider
// base URLs and API keys, and the skill sources and permissions, so they can
// live in a committed/shared file instead of only environment variables.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/prasenjeet-symon/ogcode/internal/gitignore"
)

// ProviderConfig holds per-provider connection overrides.
type ProviderConfig struct {
	BaseURL string `json:"baseUrl,omitempty"`
	APIKey  string `json:"apiKey,omitempty"`
}

// SkillsConfig configures where skills come from and which of them an agent
// may use. Every field is optional: with none of them set, ogcode still scans
// the standard project and global skill directories.
type SkillsConfig struct {
	// Paths are extra skill directories, relative to the project or absolute.
	Paths []string `json:"paths,omitempty"`
	// URLs are index.json manifests to fetch skills from.
	URLs []string `json:"urls,omitempty"`
	// Permissions maps a skill-name glob to "allow", "deny", or "ask".
	Permissions map[string]string `json:"permissions,omitempty"`
}

// MCPServerConfig configures a single Model Context Protocol server
// connection. A server is either a local subprocess (Command + Args + Env) or
// a remote HTTP endpoint (URL + Headers). Transport is one of "stdio",
// "streamable-http", or "sse" — when empty, it is inferred from whether
// Command or URL is set (stdio if Command, otherwise http).
//
// Authorization: a server with a URL and no Headers gets an OAuth handler
// attached automatically — the first 401 triggers the authorization-code
// (with PKCE) flow, so servers like Cal.com connect with no extra config. A
// server with Headers keeps the static bearer-token path and no OAuth. The
// optional Auth block overrides the defaults (pre-registered client, custom
// scopes, or SkipOAuth to disable the handler entirely).
type MCPServerConfig struct {
	// Transport forces the transport; empty auto-detects from Command/URL.
	Transport string `json:"transport,omitempty"`
	// Command is the executable run as a stdio subprocess server.
	Command string `json:"command,omitempty"`
	// Args are passed to Command.
	Args []string `json:"args,omitempty"`
	// Env augments (not replaces) the parent process environment for Command.
	Env map[string]string `json:"env,omitempty"`
	// URL is the endpoint for a streamable-http or sse server.
	URL string `json:"url,omitempty"`
	// Headers are sent with HTTP requests to a URL-based server. When set, the
	// server uses the static-token path and no OAuth handler is attached.
	Headers map[string]string `json:"headers,omitempty"`
	// Auth configures OAuth for a URL-based server. When nil (the default for a
	// server with a URL and no Headers), Dynamic Client Registration is used
	// with a localhost redirect — the "just works" path. Setting any field
	// opts into an explicit client. Has no effect for stdio servers or when
	// Headers is set.
	Auth *MCPAuthConfig `json:"auth,omitempty"`
}

// MCPAuthConfig configures OAuth authorization for a URL-based MCP server. It
// only applies to streamable-http servers without static Headers. Leave the
// whole block nil (or empty) for the default: Dynamic Client Registration with
// a loopback redirect, which works against servers like Cal.com with no
// pre-registered client.
type MCPAuthConfig struct {
	// ClientID is a pre-registered OAuth client identifier. When set, Dynamic
	// Client Registration is skipped and this client is used directly. Leave
	// empty to use DCR (the default).
	ClientID string `json:"clientId,omitempty"`
	// ClientSecret is the secret for a pre-registered confidential client.
	// Only meaningful when ClientID is set. Leave empty for a public client.
	ClientSecret string `json:"clientSecret,omitempty"`
	// Scopes are requested beyond what the server advertises. Empty = the
	// server's scopes_supported plus offline_access (when refresh tokens are
	// requested).
	Scopes []string `json:"scopes,omitempty"`
	// SkipOAuth disables the OAuth handler for this server even with no
	// Headers — for a server that returns 401 but is not an OAuth server.
	SkipOAuth bool `json:"skipOAuth,omitempty"`
}

// MCPConfig holds the named MCP servers ogcode connects to at startup.
type MCPConfig map[string]MCPServerConfig

// Config is ogcode's file-based configuration.
type Config struct {
	Providers map[string]ProviderConfig `json:"providers,omitempty"`
	Skills    SkillsConfig              `json:"skills,omitempty"`
	MCP       MCPConfig                 `json:"mcp,omitempty"`
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
//
// Skill sources merge differently from provider settings: paths and urls are
// unioned rather than overridden, so a project adds its own skill directories
// to the user's global ones instead of replacing them. Permissions merge
// key-by-key, project-local last, so a project can override one rule without
// restating the rest.
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
		merged.Skills.Paths = appendUnique(merged.Skills.Paths, c.Skills.Paths)
		merged.Skills.URLs = appendUnique(merged.Skills.URLs, c.Skills.URLs)
		for pattern, action := range c.Skills.Permissions {
			if merged.Skills.Permissions == nil {
				merged.Skills.Permissions = map[string]string{}
			}
			merged.Skills.Permissions[pattern] = action
		}
		// MCP servers merge per-name with project-local taking precedence.
		// A project names a server the global config did not → it is added;
		// a project re-states one the global config already had → project-local
		// replaces it wholesale (the fields are independent and re-stating a
		// server implies intent to own its full definition).
		for name, sc := range c.MCP {
			if merged.MCP == nil {
				merged.MCP = map[string]MCPServerConfig{}
			}
			merged.MCP[name] = sc
		}
	}
	return merged
}

// appendUnique adds each value of add to base that is not already there,
// preserving order. Skill directories are scanned in order, and scanning the
// same directory twice would register every skill in it twice.
func appendUnique(base, add []string) []string {
	for _, v := range add {
		if v == "" {
			continue
		}
		seen := false
		for _, existing := range base {
			if existing == v {
				seen = true
				break
			}
		}
		if !seen {
			base = append(base, v)
		}
	}
	return base
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
// ogcode.json. Every field is blank or empty — an unset field behaves
// identically to an absent one everywhere this package is used — but listing
// all four known providers and the skills section up front makes the file
// self-documenting to open and edit.
const projectFileTemplate = `{
  "providers": {
    "anthropic": { "baseUrl": "", "apiKey": "" },
    "openai": { "baseUrl": "", "apiKey": "" },
    "openrouter": { "apiKey": "" },
    "ollama": { "baseUrl": "", "apiKey": "" }
  },
  "skills": {
    "paths": [],
    "urls": [],
    "permissions": {}
  },
  "mcp": {}
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
	// The config file holds local connection overrides — API keys, base URLs —
	// that vary per machine, so it should not be committed. Extend an existing
	// .gitignore to ignore it; if the project keeps no .gitignore, leave it be.
	if dir != "" {
		gitignore.AddPattern(dir, "ogcode.json")
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
