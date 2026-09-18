// Package config loads the control-plane (master) configuration.
//
// This is the master side only. The worker side lives in the ogcode repo as the
// `ogcode worker` run-mode; the two never share a config file. Config is a
// singleton (not a per-name map), so the merge rule is simply scalar-last-wins:
// a value from a more specific source replaces a less specific one.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Config is the top-level control-plane config, read from a JSON file.
type Config struct {
	Master MasterConfig `json:"master"`
}

// MasterConfig configures the control-plane server. All fields are singletons:
// a value set here is the value used (scalar-last-wins if ever merged).
type MasterConfig struct {
	// Listen is the address the ConnectRPC + panel HTTP server binds, e.g.
	// ":8443". Required.
	Listen string `json:"listen"`
	// PairingSecret is the shared secret a worker must present to Register. It is
	// compared in constant time. Required.
	PairingSecret string `json:"pairingSecret"`
	// TLS, when set, terminates HTTPS/2 at the master. When empty the server
	// serves HTTP/2 cleartext (h2c) — acceptable only for local development,
	// since ConnectRPC bidi streaming requires HTTP/2.
	TLS *TLSConfig `json:"tls,omitempty"`
	// TokenTTLSeconds is how long an issued worker token stays valid before it
	// must be refreshed via Heartbeat. Defaults to 900 (15m) when <= 0.
	TokenTTLSeconds int `json:"tokenTtlSeconds,omitempty"`
	// WorkerTimeoutSeconds is how long the master waits without a heartbeat
	// before marking a worker dead and failing its routed sessions. Defaults to
	// 45 when <= 0.
	WorkerTimeoutSeconds int `json:"workerTimeoutSeconds,omitempty"`
	// OperatorPassword gates the browser-facing worker UI proxy: an operator must
	// log in with it before the master will reverse-proxy any worker's UI. When
	// empty, the proxy is UNAUTHENTICATED (dev only — the server logs a loud
	// warning). It does NOT affect the worker<->master ConnectRPC/tunnel channel,
	// which is authenticated by the pairing secret / worker token.
	OperatorPassword string `json:"operatorPassword,omitempty"`
	// SessionTTLSeconds is how long an operator login session stays valid.
	// Defaults to 43200 (12h) when <= 0.
	SessionTTLSeconds int `json:"sessionTtlSeconds,omitempty"`
	// CookieDomain scopes the operator session cookie so one login covers every
	// worker subdomain (e.g. ".panel.example" for workers at
	// "<id>.panel.example"). Empty makes the cookie host-only, i.e. the operator
	// logs in per worker host — fine for local/dev.
	CookieDomain string `json:"cookieDomain,omitempty"`
	// DBPath is where the control-plane stores its durable state (Phase A:
	// the bbolt registry; Phase B adds the operator accounts). When empty it
	// defaults to ~/.ogcode-control-plane/controlplane.db. An empty value keeps
	// the master fully in-memory — acceptable for ephemeral/dev runs, but the
	// routing table and accounts are then lost on every restart.
	DBPath string `json:"dbPath,omitempty"`
	// Incus, when set, switches assignment to container mode: one Incus
	// container per user-repo assignment, created from ImageAlias with the
	// Profile profile through the local unix Socket. The worker inside
	// registers with the master like any ordinary worker; readiness is
	// signalled by that Register, never by an Incus operation. When nil the
	// master behaves exactly as before (bare workers, pickWorker heuristic).
	Incus *IncusConfig `json:"incus,omitempty"`
}

// IncusConfig configures container mode (INCUS_WORKERS_PLAN.md §Phase B). All
// timeouts apply to master-side bookkeeping only — no Incus operation is ever
// awaited through the ConnectRPC command channel.
type IncusConfig struct {
	// Socket is the path of the Incus unix socket. Empty means the platform
	// default (/var/lib/incus/incus.socket or theIncus user socket).
	Socket string `json:"socket,omitempty"`
	// Profile is the Incus profile applied to created containers. Defaults to
	// "ogcode-worker" when empty (baked by scripts/incus/build-image.sh).
	Profile string `json:"profile,omitempty"`
	// ImageAlias is the image the containers are created from. Defaults to
	// "ogcode-base" when empty.
	ImageAlias string `json:"imageAlias,omitempty"`
	// NamePrefix prefixes every created container name. Defaults to "og-"
	// when empty; it must be DNS-label-safe ([a-z0-9-]).
	NamePrefix string `json:"namePrefix,omitempty"`
	// MaxContainersPerHost caps how many og-* containers this master keeps.
	// Assignments beyond the cap fail with a capacity error. Defaults to 20
	// when <= 0.
	MaxContainersPerHost int `json:"maxContainersPerHost,omitempty"`
	// RegisterTimeoutSeconds is how long a placement may sit in provisioning
	// before the reaper fails it (the container was created but its worker
	// never registered). Defaults to 600 when <= 0.
	RegisterTimeoutSeconds int `json:"registerTimeoutSeconds,omitempty"`
	// MasterURL is the control-plane URL workers dial (the apex/panel host,
	// e.g. https://panel.example.com). Required — it is written into
	// /etc/ogcode/master-url in-guest via cloud-init.
	MasterURL string `json:"masterURL,omitempty"`
	// PairingSecret is the worker pairing secret passed into the guest
	// (written to /etc/ogcode/pairing-secret, 0600). Defaults to the master's
	// own PairingSecret when empty — same secret, one place to rotate it.
	PairingSecret string `json:"pairingSecret,omitempty"`
}

// DBFile resolves DBPath, applying the default when it is empty. The returned
// path is never empty (the caller may not create a file at "").
func (m MasterConfig) DBFile() string {
	if m.DBPath != "" {
		return m.DBPath
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "controlplane.db"
	}
	return filepath.Join(home, ".ogcode-control-plane", "controlplane.db")
}

// TLSConfig points at a PEM cert/key pair.
type TLSConfig struct {
	Cert string `json:"cert"`
	Key  string `json:"key"`
}

// TokenTTL returns the configured token lifetime, applying the default.
func (m MasterConfig) TokenTTL() time.Duration {
	if m.TokenTTLSeconds <= 0 {
		return 15 * time.Minute
	}
	return time.Duration(m.TokenTTLSeconds) * time.Second
}

// WorkerTimeout returns the configured missed-heartbeat timeout, applying the
// default.
func (m MasterConfig) WorkerTimeout() time.Duration {
	if m.WorkerTimeoutSeconds <= 0 {
		return 45 * time.Second
	}
	return time.Duration(m.WorkerTimeoutSeconds) * time.Second
}

// SessionTTL returns the configured operator-session lifetime, applying the
// default.
func (m MasterConfig) SessionTTL() time.Duration {
	if m.SessionTTLSeconds <= 0 {
		return 12 * time.Hour
	}
	return time.Duration(m.SessionTTLSeconds) * time.Second
}

// IncusProfile returns the configured container profile, defaulting to the
// image baked by scripts/incus/build-image.sh's companion profile.
func (c *IncusConfig) IncusProfile() string {
	if c.Profile == "" {
		return "ogcode-worker"
	}
	return c.Profile
}

// IncusImageAlias returns the configured image alias, defaulting to the image
// produced by scripts/incus/build-image.sh.
func (c *IncusConfig) IncusImageAlias() string {
	if c.ImageAlias == "" {
		return "ogcode-base"
	}
	return c.ImageAlias
}

// IncusNamePrefix returns the configured container-name prefix, defaulting to
// the plan's "og-".
func (c *IncusConfig) IncusNamePrefix() string {
	if c.NamePrefix == "" {
		return "og-"
	}
	return c.NamePrefix
}

// IncusMaxContainers returns the configured container cap, applying the
// default.
func (c *IncusConfig) IncusMaxContainers() int {
	if c.MaxContainersPerHost <= 0 {
		return 20
	}
	return c.MaxContainersPerHost
}

// IncusRegisterTimeout returns the configured provisioning deadline, applying
// the default.
func (c *IncusConfig) IncusRegisterTimeout() time.Duration {
	if c.RegisterTimeoutSeconds <= 0 {
		return 600 * time.Second
	}
	return time.Duration(c.RegisterTimeoutSeconds) * time.Second
}

// IncusSecret returns the pairing secret handed to containers, defaulting to
// the master's own pairing secret.
func (c *IncusConfig) IncusSecret(masterSecret string) string {
	if c.PairingSecret != "" {
		return c.PairingSecret
	}
	return masterSecret
}

// Load reads and validates a config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	if c.Master.Listen == "" {
		return fmt.Errorf("master.listen is required (e.g. \":8443\")")
	}
	if c.Master.PairingSecret == "" {
		return fmt.Errorf("master.pairingSecret is required")
	}
	if c.Master.TLS != nil {
		if c.Master.TLS.Cert == "" || c.Master.TLS.Key == "" {
			return fmt.Errorf("master.tls requires both cert and key")
		}
	}
	if inc := c.Master.Incus; inc != nil {
		if inc.IncusMaxContainers() <= 0 {
			return fmt.Errorf("master.incus.maxContainersPerHost must be > 0")
		}
		if inc.MasterURL == "" {
			return fmt.Errorf("master.incus.masterURL is required (workers dial it, e.g. https://panel.example.com)")
		}
		prefix := inc.IncusNamePrefix()
		for i := 0; i < len(prefix); i++ {
			ch := prefix[i]
			if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
				return fmt.Errorf("master.incus.namePrefix %q must be [a-z0-9-]", prefix)
			}
		}
	}
	return nil
}
