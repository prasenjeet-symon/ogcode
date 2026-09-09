package mcp_test

import (
	"context"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/config"
	mcp "github.com/prasenjeet-symon/ogcode/internal/mcp"
)

func statusNames(m *mcp.Manager) map[string]bool {
	names := map[string]bool{}
	for _, st := range m.Statuses() {
		names[st.Name] = true
	}
	return names
}

// A disabled server is never dialled: Connect skips it, so it produces no error
// and no runtime status entry, while an enabled sibling is still attempted.
func TestManager_ConnectSkipsDisabled(t *testing.T) {
	cfg := &config.Config{
		MCP: map[string]config.MCPServerConfig{
			"on":  {Command: "false"},
			"off": {Command: "false", Disabled: true},
		},
	}
	m, _ := mcp.New(context.Background(), cfg)
	_, err := m.Connect(context.Background())
	if err == nil || !strings.Contains(err.Error(), `"on"`) {
		t.Fatalf("expected a connect error naming the enabled server, got %v", err)
	}
	if strings.Contains(err.Error(), `"off"`) {
		t.Errorf("a disabled server must not be dialled: %v", err)
	}
	names := statusNames(m)
	if !names["on"] {
		t.Errorf("enabled server should have a runtime status entry")
	}
	if names["off"] {
		t.Errorf("disabled server should have no runtime status entry")
	}
	_ = m.Close()
}

// SetServerEnabled toggles a server's runtime presence: disabling drops its
// entry (and would remove its tools); a failed enable leaves an errored entry
// rather than nothing.
func TestManager_SetServerEnabledTogglesRuntime(t *testing.T) {
	cfg := &config.Config{
		MCP: map[string]config.MCPServerConfig{"s": {Command: "false"}},
	}
	m, _ := mcp.New(context.Background(), cfg)
	_, _ = m.Connect(context.Background())
	if !statusNames(m)["s"] {
		t.Fatalf("server should have a status entry after Connect")
	}

	// Disable: the entry is removed. (This server errored, so it exposed no
	// tools; the point is the entry disappears.)
	added, _, err := m.SetServerEnabled(context.Background(), "s", cfg.MCP["s"], false)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if len(added) != 0 {
		t.Errorf("disabling should add no tools, got %d", len(added))
	}
	if statusNames(m)["s"] {
		t.Errorf("disabled server's entry should be gone")
	}

	// Re-enable with a still-failing command: the connect errors, and the
	// manager keeps an errored entry so the UI can show why.
	_, _, err = m.SetServerEnabled(context.Background(), "s", config.MCPServerConfig{Command: "false"}, true)
	if err == nil {
		t.Errorf("enabling a broken server should return the connect error")
	}
	if !statusNames(m)["s"] {
		t.Errorf("a failed enable should leave an errored status entry")
	}
	_ = m.Close()
}
