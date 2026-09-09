package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func projectMCP(t *testing.T, dir string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "ogcode.json"))
	if err != nil {
		return map[string]any{}
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("project ogcode.json not valid JSON: %v", err)
	}
	mcp, _ := root["mcp"].(map[string]any)
	if mcp == nil {
		return map[string]any{}
	}
	return mcp
}

// A project-native server: disable flips the flag in place (keeping command),
// enable clears it and keeps the server in the project file.
func TestSetMCPDisabled_ProjectNative(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, ".git"), "")
	writeJSON(t, filepath.Join(dir, "ogcode.json"),
		`{"mcp":{"local":{"command":"mytool","args":["--x"]}}}`)

	if _, err := SetMCPDisabled(dir, "local", true); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if got := Load(dir).MCP["local"]; !got.Disabled || got.Command != "mytool" {
		t.Errorf("after disable: %+v, want Disabled=true Command=mytool", got)
	}
	if MCPScope(dir, "local") != "project" {
		t.Errorf("scope = %q, want project", MCPScope(dir, "local"))
	}

	if _, err := SetMCPDisabled(dir, "local", false); err != nil {
		t.Fatalf("enable: %v", err)
	}
	got := Load(dir).MCP["local"]
	if got.Disabled || got.Command != "mytool" {
		t.Errorf("after enable: %+v, want Disabled=false Command=mytool", got)
	}
	// A project-native server stays in the project file when enabled.
	if _, ok := projectMCP(t, dir)["local"]; !ok {
		t.Errorf("project-native server should remain in ogcode.json when enabled")
	}
}

// A server defined only in the global config: disabling it per-project pins its
// full definition into the project file (so re-enabling can still reconnect),
// and enabling removes that pin so the file falls back to global and cannot
// drift.
func TestSetMCPDisabled_GlobalServerPinnedThenRestored(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	globalDir := filepath.Join(home, ".config", "ogcode")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(globalDir, "config.json"),
		`{"mcp":{"ctx7":{"url":"https://ctx7.example/mcp"}}}`)

	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, ".git"), "")

	// Baseline: the server is visible via the global config, enabled.
	if got := Load(dir).MCP["ctx7"]; got.URL != "https://ctx7.example/mcp" || got.Disabled {
		t.Fatalf("baseline merged: %+v", got)
	}
	if MCPScope(dir, "ctx7") != "global" {
		t.Fatalf("baseline scope = %q, want global", MCPScope(dir, "ctx7"))
	}

	// Disable: the project file now pins url + disabled:true.
	if _, err := SetMCPDisabled(dir, "ctx7", true); err != nil {
		t.Fatalf("disable: %v", err)
	}
	got := Load(dir).MCP["ctx7"]
	if !got.Disabled || got.URL != "https://ctx7.example/mcp" {
		t.Errorf("after disable: %+v, want Disabled=true with url pinned", got)
	}
	pinned, ok := projectMCP(t, dir)["ctx7"].(map[string]any)
	if !ok || pinned["url"] != "https://ctx7.example/mcp" || pinned["disabled"] != true {
		t.Errorf("project pin = %v, want url + disabled:true", projectMCP(t, dir)["ctx7"])
	}
	if MCPScope(dir, "ctx7") != "project" {
		t.Errorf("scope after disable = %q, want project", MCPScope(dir, "ctx7"))
	}

	// Enable: the pin (identical to global but for the flag) is removed, so the
	// project file no longer mentions the server and it falls back to global.
	if _, err := SetMCPDisabled(dir, "ctx7", false); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, ok := projectMCP(t, dir)["ctx7"]; ok {
		t.Errorf("enabling a pinned global server should drop it from the project file, got %v", projectMCP(t, dir))
	}
	got = Load(dir).MCP["ctx7"]
	if got.Disabled || got.URL != "https://ctx7.example/mcp" {
		t.Errorf("after enable: %+v, want enabled via global", got)
	}
	if MCPScope(dir, "ctx7") != "global" {
		t.Errorf("scope after enable = %q, want global", MCPScope(dir, "ctx7"))
	}
}

// Disabling must not disturb other servers or other config sections.
func TestSetMCPDisabled_PreservesSiblings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, ".git"), "")
	writeJSON(t, filepath.Join(dir, "ogcode.json"), `{
		"providers": { "anthropic": { "apiKey": "keep" } },
		"mcp": {
			"a": { "command": "a-tool" },
			"b": { "url": "https://b.example" }
		}
	}`)

	if _, err := SetMCPDisabled(dir, "a", true); err != nil {
		t.Fatalf("disable a: %v", err)
	}
	cfg := Load(dir)
	if !cfg.MCP["a"].Disabled {
		t.Errorf("a should be disabled")
	}
	if cfg.MCP["b"].Disabled || cfg.MCP["b"].URL != "https://b.example" {
		t.Errorf("sibling b disturbed: %+v", cfg.MCP["b"])
	}
	if cfg.Providers["anthropic"].APIKey != "keep" {
		t.Errorf("providers disturbed: %+v", cfg.Providers)
	}
}
