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
	// Point the free key pool at a dead address so loadProviderMap's free-pool
	// fetch fails instantly instead of stalling on the real GitHub URL.
	t.Setenv("OGCODE_FREE_KEYS_URL", "http://127.0.0.1:9/free-keys-unavailable")
	t.Setenv("OGCODE_EMBED_MODEL_DIR", t.TempDir())

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

	srv := &Server{db: pdb, globalDB: gdb, registry: provider.NewRegistry(), dir: tmp}
	provider.ResetFreePoolForTest()
	return srv
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
// OLLAMA_BASE_URL set and nothing live on the host, baseUrl defaults to the
// localhost endpoint.
//
// The test must not depend on whatever Ollama (or proxy) happens to be running
// on the dev machine: the fallback detector would find it and report its URL
// instead of the default. We pin the primary probe to a dead address and
// disable fallbacks so the "nothing running" path is exercised deterministically.
func TestOllamaStatusEndpoint(t *testing.T) {
	orig := provider.PrimaryOllamaBaseURL
	provider.PrimaryOllamaBaseURL = "http://127.0.0.1:1/v1"
	t.Cleanup(func() { provider.PrimaryOllamaBaseURL = orig })
	t.Setenv("OLLAMA_BASE_URL", "")
	t.Setenv("OLLAMA_FALLBACK_URLS", "")

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
	// With no OLLAMA_BASE_URL set and nothing live, the base URL must default
	// to the (dead) primary endpoint we pinned — which equals the real default.
	if st.BaseURL != "http://127.0.0.1:1/v1" {
		t.Fatalf("expected pinned primary baseUrl http://127.0.0.1:1/v1, got %q", st.BaseURL)
	}
	if st.Running {
		t.Fatalf("expected running=false with a dead primary and no fallbacks, got true")
	}
}

// TestFreePoolProvidersEndToEnd exercises the whole community-key-pool feature
// through the real registration + HTTP path: a pool JSON is served over HTTP,
// loadProviderMap fetches it and registers the free providers, and the
// GET /api/providers/free endpoint reports them in priority order with their
// pool keys masked out.
func TestFreePoolProvidersEndToEnd(t *testing.T) {
	const secretKey = "smb_super_secret_pool_key"
	poolSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"version": 1,
			"providers": {
				"cerebras":      {"collection":"Cerebras","baseURL":"https://api.cerebras.ai/v1","keys":["csk_secret"],"defaultModel":"llama-3.3-70b"},
				"sambanova":     {"collection":"SambaNova","baseURL":"https://api.sambanova.ai/v1","keys":["` + secretKey + `"],"defaultModel":"Meta-Llama-3.3-70B-Instruct"},
				"github_models": {"collection":"GitHub Models","baseURL":"https://models.inference.ai.azure.com","keys":["ghp_secret"],"defaultModel":"gpt-4o-mini"}
			}
		}`))
	}))
	defer poolSrv.Close()

	srv := newTestServer(t)
	// Override the dead URL that newTestServer installs, then reset the pool
	// singleton so the next fetch actually hits our live test server.
	t.Setenv("OGCODE_FREE_KEYS_URL", poolSrv.URL)
	provider.ResetFreePoolForTest()

	// Rebuild the registry so the free-tier providers get provisioned.
	srv.reloadProviders()

	// 1) The free providers must be registered under their "ogcode-<id>" IDs.
	got := map[string]bool{}
	for _, id := range srv.registry.List() {
		got[id] = true
	}
	for _, want := range []string{"ogcode-cerebras", "ogcode-sambanova", "ogcode-github_models"} {
		if !got[want] {
			t.Fatalf("expected free provider %q registered, got registry %v", want, srv.registry.List())
		}
	}

	// 2) The endpoint reports them, Cerebras first (highest priority among the
	//    served providers), with keys never present in the payload.
	h := srv.routes()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/providers/free", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/providers/free = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), secretKey) {
		t.Fatalf("pool key leaked to the client in /api/providers/free response: %s", rec.Body.String())
	}
	var free []struct {
		Collection   string `json:"collection"`
		BaseURL      string `json:"baseUrl"`
		DefaultModel string `json:"defaultModel"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &free); err != nil {
		t.Fatalf("decode free providers: %v (body: %s)", err, rec.Body.String())
	}
	if len(free) != 3 {
		t.Fatalf("expected 3 free providers, got %d (%v)", len(free), free)
	}
	if free[0].Collection != "Cerebras" {
		t.Fatalf("expected Cerebras first (priority order), got %q", free[0].Collection)
	}
	if free[0].DefaultModel == "" || free[0].BaseURL == "" {
		t.Fatalf("free provider payload missing baseUrl/defaultModel: %+v", free[0])
	}
}

// stubProvider is a minimal Provider whose Models() returns a fixed list — used
// to exercise the /api/models handler deterministically without any network.
type stubProvider struct {
	id     string
	models []provider.ModelInfo
}

func (s stubProvider) ID() string                   { return s.id }
func (s stubProvider) Models() []provider.ModelInfo { return s.models }
func (s stubProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	return nil, nil
}

// TestModelsSingleGlobalDefault verifies /api/models collapses the several
// per-provider defaults into exactly one global default — the default model of
// the highest-priority registered provider (ogcode-openrouter ranks above
// ogcode-cerebras) — and that this default is enabled for a fresh user.
func TestModelsSingleGlobalDefault(t *testing.T) {
	srv := newTestServer(t)
	srv.registry.ReplaceProviders(map[string]provider.Provider{
		"ogcode-openrouter": stubProvider{id: "ogcode-openrouter", models: []provider.ModelInfo{
			{ID: "cohere/north-mini-code:free", ProviderID: "ogcode-openrouter", Default: true, ActiveByDefault: true},
			{ID: "qwen/qwen3-coder:free", ProviderID: "ogcode-openrouter", ActiveByDefault: true},
		}},
		"ogcode-cerebras": stubProvider{id: "ogcode-cerebras", models: []provider.ModelInfo{
			{ID: "llama-3.3-70b", ProviderID: "ogcode-cerebras", Default: true, ActiveByDefault: true},
		}},
	})

	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/models = %d, want 200", rec.Code)
	}
	var models []struct {
		ID         string `json:"id"`
		ProviderID string `json:"providerId"`
		Default    bool   `json:"default"`
		Enabled    bool   `json:"enabled"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &models); err != nil {
		t.Fatalf("decode models: %v", err)
	}
	var defaults []string
	northEnabled := false
	for _, m := range models {
		if m.Default {
			defaults = append(defaults, m.ID)
		}
		if m.ID == "cohere/north-mini-code:free" {
			northEnabled = m.Enabled
		}
	}
	if len(defaults) != 1 || defaults[0] != "cohere/north-mini-code:free" {
		t.Fatalf("expected North Mini Code as the sole default, got %v", defaults)
	}
	if !northEnabled {
		t.Fatal("the default free model must be enabled for a new user")
	}
}
