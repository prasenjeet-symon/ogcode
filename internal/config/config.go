// Package config loads ogcode's optional file-based configuration: provider
// base URLs and API keys, the skill sources and permissions, and the
// live-preview domain, so they can live in a committed/shared file instead of
// only environment variables.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/prasenjeet-symon/ogcode/internal/gitignore"
)

// ProviderConfig holds per-provider connection overrides. The four built-in
// slots (anthropic, openai, openrouter, ollama) use it for endpoint and key
// overrides; extra named entries in the same map create additional
// OpenAI-compatible providers (see CustomProviders).
type ProviderConfig struct {
	// BaseURL overrides the provider endpoint; for a custom (non-slot) provider
	// it is the endpoint itself and is required.
	BaseURL string `json:"baseUrl,omitempty"`
	// APIKey is the key exported for the provider (built-in slots) or the one
	// sent as a bearer token (custom providers).
	APIKey string `json:"apiKey,omitempty"`
	// Type is the protocol this connection speaks. Today only "openai" — the
	// OpenAI Chat Completions API — is supported, and empty means openai;
	// anything else is rejected for now (CustomProviders skips such entries).
	Type string `json:"type,omitempty"`
	// Model is an optional default model for this provider, tried when the user
	// has not picked one.
	Model string `json:"model,omitempty"`
	// Label is an optional human-readable label shown as this provider's
	// grouping in the model picker.
	Label string `json:"label,omitempty"`
	// Disabled turns the provider off without deleting it, mirroring
	// `disabled` on MCP servers. Off by default — an unset field means enabled.
	Disabled bool `json:"disabled,omitempty"`
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
	// Env supplies the environment variables skills declare in their
	// frontmatter `requires` list — the credential a skill's scripts read, the
	// endpoint they call. Values are exported into the process environment at
	// startup, after which a `requires` name is satisfied exactly as if it had
	// been exported by the shell. A real environment variable always wins, so a
	// secret kept in the environment is never overridden by the file.
	//
	// The file it lives in is the same one holding provider API keys, and it is
	// gitignored for the same reason: it is where secrets go.
	Env map[string]string `json:"env,omitempty"`
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
	// Disabled turns the server off without deleting it: ogcode neither connects
	// to it nor exposes its tools, so nothing about it reaches the agent's
	// prompt (and no tokens are spent on its tool schemas). The server's config
	// and any stored OAuth tokens are left untouched, so flipping this back to
	// false reconnects it. Off by default — an unset field means enabled.
	Disabled bool `json:"disabled,omitempty"`
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

// previewDomainEnv is the variable a configured preview domain is exported
// into. The agent package's PreviewDomain reads it, so a domain set in
// ogcode.json reaches live-service previews exactly as one exported by the
// shell does — and, being read once at startup, it stays fixed for the session.
const previewDomainEnv = "OGCODE_PREVIEW_DOMAIN"

// Config is ogcode's file-based configuration.
type Config struct {
	Providers map[string]ProviderConfig `json:"providers,omitempty"`
	Skills    SkillsConfig              `json:"skills,omitempty"`
	MCP       MCPConfig                 `json:"mcp,omitempty"`
	// PreviewDomain is the host suffix loopback services are served under —
	// the file-config equivalent of the OGCODE_PREVIEW_DOMAIN environment
	// variable, which wins when both are set. Empty keeps the default
	// *.localhost, which reaches this server only from the machine it runs on;
	// a real wildcard domain serves previews to a browser anywhere.
	PreviewDomain string `json:"previewDomain,omitempty"`
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
			if pc.Model != "" {
				existing.Model = pc.Model
			}
			if pc.Label != "" {
				existing.Label = pc.Label
			}
			if pc.Type != "" {
				existing.Type = pc.Type
			}
			// Disabled merges one-way: a project can disable a provider without
			// touching the global config, but it cannot re-enable a globally-
			// disabled one — flipping the bit back would silently undo a deliberate
			// global shutoff on the machine this file is shared with.
			if pc.Disabled {
				existing.Disabled = true
			}
			merged.Providers[id] = existing
		}
		if c.PreviewDomain != "" {
			merged.PreviewDomain = c.PreviewDomain
		}
		merged.Skills.Paths = appendUnique(merged.Skills.Paths, c.Skills.Paths)
		merged.Skills.URLs = appendUnique(merged.Skills.URLs, c.Skills.URLs)
		for pattern, action := range c.Skills.Permissions {
			if merged.Skills.Permissions == nil {
				merged.Skills.Permissions = map[string]string{}
			}
			merged.Skills.Permissions[pattern] = action
		}
		// Skill env merges like permissions: key by key, project-local last, so a
		// secret kept in the global config is available to every project without
		// being restated, and a project can override one name without losing the
		// rest. Global is read first, so a project value overwrites it.
		for name, value := range c.Skills.Env {
			if merged.Skills.Env == nil {
				merged.Skills.Env = map[string]string{}
			}
			merged.Skills.Env[name] = value
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

// CustomProviders returns the providers ogcode.json names beyond the four
// built-in slots — extra OpenAI-compatible endpoints, each served under its
// own name. Built-in ids (the keys of envNames) and "ogx" are never returned:
// those are the first-party slots. An entry is skipped when it is disabled,
// when its type is neither "" nor "openai" (unsupported for now), when its
// baseUrl is empty (a custom provider only makes sense pointed somewhere),
// or when its id is empty. The map may be empty; callers register every
// returned entry under its map key.
func (c *Config) CustomProviders() map[string]ProviderConfig {
	out := make(map[string]ProviderConfig)
	for id, pc := range c.Providers {
		if id == "" {
			continue
		}
		if _, builtin := envNames[id]; builtin || id == "ogx" {
			continue
		}
		if pc.Disabled || pc.BaseURL == "" {
			continue
		}
		if pc.Type != "" && pc.Type != "openai" {
			continue
		}
		if pc.Type == "" {
			pc.Type = "openai"
		}
		out[id] = pc
	}
	return out
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
// all four known providers, the skills section and the preview domain up front
// makes the file self-documenting to open and edit. The file can also hold
// extra named OpenAI-compatible providers beyond those four slots.
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
    "permissions": {},
    "env": {}
  },
  "mcp": {},
  "previewDomain": ""
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

// ApplyEnv exports the config's provider settings, preview domain and skill
// requirements as environment variables, for each variable that isn't already
// set. A real environment variable (or one set earlier from a .env file) always
// wins over the config file.
//
// Skills are exported here rather than at load time because the check a skill
// declares is a plain "is this variable set?" — exporting at startup makes a
// `requires` name and an exported secret the same thing, which is what lets a
// skill's own scripts reach it through the ordinary process environment.
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
	for name, value := range c.Skills.Env {
		if name == "" || value == "" {
			continue
		}
		setIfUnset(name, value)
	}
	if c.PreviewDomain != "" {
		setIfUnset(previewDomainEnv, c.PreviewDomain)
	}
}

func setIfUnset(name, value string) {
	if _, ok := os.LookupEnv(name); !ok {
		os.Setenv(name, value)
	}
}
