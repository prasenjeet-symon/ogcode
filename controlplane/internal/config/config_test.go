package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoad_IncusRoundTripAndDefaults(t *testing.T) {
	dir := t.TempDir()

	// Round-trip: every field survives JSON.
	src := `{
	  "master": {
	    "listen": ":8443",
	    "pairingSecret": "s3cret",
	    "incus": {
	      "socket": "/var/lib/incus/incus.socket",
	      "profile": "ogcode-worker",
	      "imageAlias": "ogcode-base",
	      "namePrefix": "og-",
	      "maxContainersPerHost": 7,
	      "registerTimeoutSeconds": 300,
	      "masterURL": "https://panel.example.com",
	      "pairingSecret": "guest-secret"
	    }
	  }
	}`
	full := filepath.Join(dir, "full.json")
	if err := os.WriteFile(full, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(full)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	inc := cfg.Master.Incus
	if inc == nil {
		t.Fatal("incus block dropped by round-trip")
	}
	if inc.Socket != "/var/lib/incus/incus.socket" {
		t.Errorf("socket = %q", inc.Socket)
	}
	if got := inc.IncusProfile(); got != "ogcode-worker" {
		t.Errorf("profile = %q", got)
	}
	if got := inc.IncusImageAlias(); got != "ogcode-base" {
		t.Errorf("imageAlias = %q", got)
	}
	if got := inc.IncusMaxContainers(); got != 7 {
		t.Errorf("maxContainers = %d, want 7", got)
	}
	if got := inc.IncusRegisterTimeout(); got != 300*time.Second {
		t.Errorf("registerTimeout = %v", got)
	}
	if inc.MasterURL != "https://panel.example.com" {
		t.Errorf("masterURL = %q", inc.MasterURL)
	}
	if got := inc.IncusSecret(cfg.Master.PairingSecret); got != "guest-secret" {
		t.Errorf("own secret not honored: %q", got)
	}

	// Defaults: an empty-ish block still loads and applies the plan defaults.
	empty := `{"master": {"listen": ":8443", "pairingSecret": "s", "incus": {"masterURL": "https://panel.example.com"}}}`
	minimal := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(minimal, []byte(empty), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg2, err := Load(minimal)
	if err != nil {
		t.Fatalf("Load minimal: %v", err)
	}
	inc2 := cfg2.Master.Incus
	if inc2.IncusProfile() != "ogcode-worker" || inc2.IncusImageAlias() != "ogcode-base" {
		t.Errorf("profile/image defaults = %q/%q", inc2.IncusProfile(), inc2.IncusImageAlias())
	}
	if inc2.IncusNamePrefix() != "og-" {
		t.Errorf("namePrefix default = %q", inc2.IncusNamePrefix())
	}
	if inc2.IncusMaxContainers() != 20 {
		t.Errorf("maxContainers default = %d", inc2.IncusMaxContainers())
	}
	if inc2.IncusRegisterTimeout() != 600*time.Second {
		t.Errorf("registerTimeout default = %v", inc2.IncusRegisterTimeout())
	}
	if inc2.IncusSecret("master-secret") != "master-secret" {
		t.Errorf("secret default should be the master's own: %q", inc2.IncusSecret("master-secret"))
	}
}

func TestLoad_IncusValidation(t *testing.T) {
	base := func(inc string) string {
		return `{"master": {"listen": ":8443", "pairingSecret": "s", "incus": ` + inc + `}}`
	}
	cases := []struct {
		name  string
		incus string
		want  string // error substring; empty = valid
	}{
		{"missing masterURL", "{}", "masterURL is required"},
		{"zero cap ok (default applies)", `{"masterURL": "https://p.example.com", "maxContainersPerHost": 0}`, ""},
		{"negative cap ok (default applies)", `{"masterURL": "https://p.example.com", "maxContainersPerHost": -3}`, ""},
		{"bad prefix chars", `{"masterURL": "https://p.example.com", "namePrefix": "OG_"}`, "must be [a-z0-9-]"},
		{"valid default prefix", `{"masterURL": "https://p.example.com", "namePrefix": "og-"}`, ""},
		{"valid bare prefix", `{"masterURL": "https://p.example.com", "namePrefix": "og"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.json")
			if err := os.WriteFile(path, []byte(base(tc.incus)), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("want valid, got %v", err)
				}
				if cfg.Master.Incus == nil {
					t.Fatal("incus dropped")
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got none", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestIncusConfigJSONOmitsWhenNil pins the omitempty behavior the bare-mode
// config file relies on: no incus key means bare mode, byte-for-byte.
func TestIncusConfigJSONOmitsWhenNil(t *testing.T) {
	var m MasterConfig
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "incus") {
		t.Errorf("nil incus must not appear: %s", b)
	}
}
