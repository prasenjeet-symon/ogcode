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
	return nil
}
