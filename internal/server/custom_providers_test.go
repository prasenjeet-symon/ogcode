package server

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadProviderMapRegistersCustomProviders wires the whole path end to end:
// an ogcode.json entry beyond the four first-party slots must land in the
// provider map as a real provider, while built-in, disabled and unsupported
// entries must not. config.Load also reads the global file under HOME, so HOME
// is pointed into the temp dir to isolate the test from the developer's own
// ~/.config/ogcode/config.json.
func TestLoadProviderMapRegistersCustomProviders(t *testing.T) {
	srv := newTestServer(t)

	home := filepath.Join(srv.dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	t.Setenv("HOME", home)

	cfg := `{
	  "providers": {
	    "openai": {"apiKey": "sk-builtin-is-not-custom"},
	    "deepseek-local": {"type": "openai", "baseUrl": "https://api.deepseek.com/v1", "apiKey": "sk-1", "model": "deepseek-chat"},
	    "home-lab": {"baseUrl": "http://llm.corp/v1", "label": "Home lab", "disabled": true},
	    "wrong-type": {"type": "anthropic", "baseUrl": "https://x.example/v1"}
	  }
	}`
	if err := os.WriteFile(filepath.Join(srv.dir, "ogcode.json"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write ogcode.json: %v", err)
	}

	m := srv.loadProviderMap()

	p, ok := m["deepseek-local"]
	if !ok {
		t.Fatalf("custom provider deepseek-local missing from map, got %v", m)
	}
	if p.ID() != "deepseek-local" {
		t.Errorf("provider ID = %q, want deepseek-local", p.ID())
	}
	if len(p.Models()) == 0 {
		t.Errorf("custom provider serves no models")
	}
	for _, absent := range []string{"home-lab", "wrong-type", "openai"} {
		if _, ok := m[absent]; ok {
			t.Errorf("%s should have been filtered out of the map", absent)
		}
	}
}
