package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
)

// newTestServer builds a Server backed by temp DBs and an empty registry,
// suitable for exercising the provider/config/models HTTP handlers in process.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	// Neutralize any provider env vars so the test is deterministic regardless
	// of the developer's shell.
	for _, k := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL",
		"OPENAI_API_KEY", "OPENAI_BASE_URL",
		"OPENROUTER_API_KEY", "OLLAMA_API_KEY", "OLLAMA_BASE_URL",
	} {
		t.Setenv(k, "")
	}

	tmp := t.TempDir()
	pdb, err := db.Open(filepath.Join(tmp, "ogcode.db"))
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	t.Cleanup(func() { pdb.Close() })
	gdb, err := db.Open(filepath.Join(tmp, "config.db"))
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	t.Cleanup(func() { gdb.Close() })

	return &Server{db: pdb, globalDB: gdb, registry: provider.NewRegistry(), dir: tmp}
}

func modelCount(t *testing.T, h http.Handler) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/models = %d, want 200", rec.Code)
	}
	var models []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &models); err != nil {
		t.Fatalf("decode models: %v (body: %s)", err, rec.Body.String())
	}
	return len(models)
}

// TestSetProviderConfigHotReload verifies the core onboarding chain end-to-end at
// the HTTP layer: with no provider configured the model list is empty; after
// POSTing an Anthropic key the provider hot-reloads in place and its models
// appear immediately — no restart.
func TestSetProviderConfigHotReload(t *testing.T) {
	srv := newTestServer(t)
	h := srv.routes()

	if n := modelCount(t, h); n != 0 {
		t.Fatalf("expected 0 models before configuring a provider, got %d", n)
	}

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"apiKey":"sk-ant-dummy"}`)
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/providers/config/anthropic", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST set provider = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	if n := modelCount(t, h); n == 0 {
		t.Fatal("expected Anthropic models to appear after saving the key (hot-reload), got 0")
	}

	// And the masked GET should now report the key as set.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/providers/config", nil))
	var cfgs []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &cfgs); err != nil {
		t.Fatalf("decode provider configs: %v", err)
	}
	var anthropicSet bool
	for _, c := range cfgs {
		if c["providerId"] == "anthropic" && c["apiKey"] == "__SET__" {
			anthropicSet = true
		}
	}
	if !anthropicSet {
		t.Fatalf("expected anthropic apiKey to be masked as __SET__, got %v", cfgs)
	}
}

// TestValidateProviderConfigStructure verifies the validate endpoint always
// returns a well-formed {ok,error} body. A pre-cancelled request context makes
// the underlying provider call fail fast, so the test needs no network and is
// deterministic.
func TestValidateProviderConfigStructure(t *testing.T) {
	srv := newTestServer(t)
	h := srv.routes()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // force the validation call to fail immediately

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/providers/config/anthropic/validate",
		strings.NewReader(`{"apiKey":"whatever"}`),
	).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("validate = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var res struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode validate result: %v (body: %s)", err, rec.Body.String())
	}
	if res.OK {
		t.Fatal("expected ok=false for a cancelled validation call")
	}
	if res.Error == "" {
		t.Fatal("expected a non-empty error message on failure")
	}
}

// TestOllamaStatusEndpoint verifies the /api/providers/ollama/status endpoint
// returns a well-formed {installed,running,baseUrl} payload and that, with no
// OLLAMA_BASE_URL set, baseUrl defaults to the localhost endpoint.
func TestOllamaStatusEndpoint(t *testing.T) {
	srv := newTestServer(t)
	h := srv.routes()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/providers/ollama/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/providers/ollama/status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var st struct {
		Installed bool   `json:"installed"`
		Running   bool   `json:"running"`
		BaseURL   string `json:"baseUrl"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode ollama status: %v (body: %s)", err, rec.Body.String())
	}
	// With no OLLAMA_BASE_URL set, the base URL must default to localhost.
	if st.BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("expected default baseUrl http://localhost:11434/v1, got %q", st.BaseURL)
	}
}
