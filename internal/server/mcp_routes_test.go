package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/prasenjeet-symon/ogcode/internal/config"
	"github.com/prasenjeet-symon/ogcode/internal/mcp"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// newMCPTestServer wires a Server around a project whose ogcode.json declares
// one stdio server ("echo") that fails to connect (command "false"). That is
// enough to exercise the list/toggle contract and the config-file writes; the
// live connect success path needs a real MCP server and is covered by the mcp
// package's adapter tests.
func newMCPTestServer(t *testing.T) (http.Handler, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".git"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "ogcode.json"),
		[]byte(`{"mcp":{"echo":{"command":"false"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr, _ := mcp.New(context.Background(), config.Load(project))
	tools, _ := mgr.Connect(context.Background())
	reg := tool.NewRegistry()
	for _, tl := range tools {
		reg.Register(tl)
	}

	srv := &Server{dir: project, mcpManager: mgr, toolRegistry: reg}
	r := chi.NewRouter()
	r.Get("/mcp", srv.handleListMCP)
	r.Post("/mcp/{name}", srv.handleSetMCPEnabled)
	t.Cleanup(func() { _ = mgr.Close() })
	return r, project
}

func fetchMCP(t *testing.T, r http.Handler) map[string]mcpServerSummary {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	var got []mcpServerSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	byName := map[string]mcpServerSummary{}
	for _, s := range got {
		byName[s.Name] = s
	}
	return byName
}

func toggleMCP(t *testing.T, r http.Handler, name string, on bool) (mcpServerSummary, int) {
	t.Helper()
	body := strings.NewReader(fmt.Sprintf(`{"enabled":%t}`, on))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp/"+name, body))
	var got mcpServerSummary
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode set: %v", err)
		}
	}
	return got, rec.Code
}

func TestMCP_ToggleRoundTrip(t *testing.T) {
	r, project := newMCPTestServer(t)

	// Listed, enabled, described from config; not connected (command failed).
	echo := fetchMCP(t, r)["echo"]
	if !echo.Enabled || echo.Connected {
		t.Fatalf("initial echo: enabled=%v connected=%v, want enabled & not connected", echo.Enabled, echo.Connected)
	}
	if echo.Transport != "stdio" || echo.Target != "false" || echo.Scope != "project" {
		t.Errorf("echo metadata: %+v", echo)
	}

	// Disable → response reflects it and the project file carries disabled:true.
	if got, code := toggleMCP(t, r, "echo", false); code != http.StatusOK || got.Enabled {
		t.Fatalf("disable echo: code=%d enabled=%v", code, got.Enabled)
	}
	if fetchMCP(t, r)["echo"].Enabled {
		t.Errorf("echo should be disabled in the listing")
	}
	if cfg := readConfig(t, project); !strings.Contains(cfg, `"disabled": true`) {
		t.Errorf("ogcode.json missing disabled flag:\n%s", cfg)
	}

	// Re-enable → enabled again, and the disabled flag is gone from the file.
	if got, code := toggleMCP(t, r, "echo", true); code != http.StatusOK || !got.Enabled {
		t.Fatalf("enable echo: code=%d enabled=%v", code, got.Enabled)
	}
	if !fetchMCP(t, r)["echo"].Enabled {
		t.Errorf("echo should be enabled again")
	}
	if cfg := readConfig(t, project); strings.Contains(cfg, "disabled") {
		t.Errorf("disabled flag should be gone from ogcode.json:\n%s", cfg)
	}
}

func TestMCP_SetUnknownServerIs404(t *testing.T) {
	r, _ := newMCPTestServer(t)
	if _, code := toggleMCP(t, r, "nope", false); code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
}
