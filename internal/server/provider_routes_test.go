package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/agent"
	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
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

	srv := &Server{db: pdb, globalDB: gdb, registry: provider.NewRegistry(), dir: tmp}
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
// the highest-priority registered provider (openrouter ranks above ollama)
// — and that this default is enabled for a fresh user.
func TestModelsSingleGlobalDefault(t *testing.T) {
	srv := newTestServer(t)
	srv.registry.ReplaceProviders(map[string]provider.Provider{
		"openrouter": stubProvider{id: "openrouter", models: []provider.ModelInfo{
			{ID: "anthropic/claude-sonnet-4-6", ProviderID: "openrouter", Default: true, ActiveByDefault: true},
			{ID: "qwen/qwen3-coder", ProviderID: "openrouter", ActiveByDefault: true},
		}},
		"ollama": stubProvider{id: "ollama", models: []provider.ModelInfo{
			{ID: "llama3.1", ProviderID: "ollama", Default: true, ActiveByDefault: true},
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
	sonnetEnabled := false
	for _, m := range models {
		if m.Default {
			defaults = append(defaults, m.ID)
		}
		if m.ID == "anthropic/claude-sonnet-4-6" {
			sonnetEnabled = m.Enabled
		}
	}
	if len(defaults) != 1 || defaults[0] != "anthropic/claude-sonnet-4-6" {
		t.Fatalf("expected Claude Sonnet as the sole default, got %v", defaults)
	}
	if !sonnetEnabled {
		t.Fatal("the default model must be enabled for a new user")
	}
}

// TestModelPreferenceIsScopedToProvider pins the read side of the (id,
// provider_id) preference key: the same model id served by two providers —
// glm-5.3-flash exists under both the OGX plan and a custom OpenAI-compatible
// endpoint — carries two independent enabled states. A toggle on one must
// leave the other exactly as its own default has it, or a disable somewhere
// else would darken a plan model the user never touched.
func TestModelPreferenceIsScopedToProvider(t *testing.T) {
	srv := newTestServer(t)
	h := srv.routes()
	srv.registry.ReplaceProviders(map[string]provider.Provider{
		"ogx": stubProvider{id: "ogx", models: []provider.ModelInfo{
			{ID: "glm-5.3-flash", ProviderID: "ogx", ActiveByDefault: true},
		}},
		"openai": stubProvider{id: "openai", models: []provider.ModelInfo{
			{ID: "glm-5.3-flash", ProviderID: "openai", ActiveByDefault: true},
		}},
	})

	// Disable the custom endpoint's copy; the plan's copy must not follow.
	body := strings.NewReader(`{"id":"glm-5.3-flash","providerId":"openai","displayName":"GLM","enabled":false}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/models/preference", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST preference = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/models", nil))
	var models []struct {
		ID         string `json:"id"`
		ProviderID string `json:"providerId"`
		Enabled    bool   `json:"enabled"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &models); err != nil {
		t.Fatalf("decode models: %v (body: %s)", err, rec.Body.String())
	}
	seen := make(map[string]bool)
	for _, m := range models {
		if m.ID != "glm-5.3-flash" {
			continue
		}
		seen[m.ProviderID] = m.Enabled
	}
	if !seen["ogx"] {
		t.Fatal("the OGX plan's model must stay enabled when the same id is disabled elsewhere")
	}
	if seen["openai"] {
		t.Fatal("the disabled provider's copy must read back disabled")
	}
}

// TestSessionCarriesTheChosenProvider pins the session-side fix at the HTTP
// boundary: the provider chosen in the picker rides the create POST and comes
// back on the session, and a later PATCH can change it. Without this, a model id
// served by two providers (glm-5.3-flash) resolves by whichever the registry
// walks first rather than by what the user picked.
func TestSessionCarriesTheChosenProvider(t *testing.T) {
	srv := newTestServer(t)
	srv.store = session.NewStore(srv.db)
	srv.bus = bus.New(64)
	h := srv.routes()

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"directory":"` + srv.dir + `","model":"glm-5.3-flash","provider":"ogx"}`)
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/session", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/session = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID       string `json:"id"`
		Model    string `json:"model"`
		Provider string `json:"provider"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode session: %v (body: %s)", err, rec.Body.String())
	}
	if created.Model != "glm-5.3-flash" || created.Provider != "ogx" {
		t.Fatalf("created session = model %q provider %q, want glm-5.3-flash/ogx", created.Model, created.Provider)
	}

	// A PATCH must be able to move the session to the other provider.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/session/"+created.ID,
		strings.NewReader(`{"provider":"openai"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/session = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var updated struct {
		Provider string `json:"provider"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated session: %v", err)
	}
	if updated.Provider != "openai" {
		t.Fatalf("after PATCH provider = %q, want openai", updated.Provider)
	}
}

// TestCustomModelMetadataRoundTrips pins the custom-model settings contract at
// the HTTP boundary: the window, output ceiling, per-1M prices and image support
// the user typed for a model no catalogue knows go in on
// POST /api/models/preference and come back on GET /api/models — with the
// compaction threshold derived from the configured window, so the context meter
// agrees with what the loop will do.
func TestCustomModelMetadataRoundTrips(t *testing.T) {
	srv := newTestServer(t)
	h := srv.routes()
	srv.registry.ReplaceProviders(map[string]provider.Provider{
		"openai": stubProvider{id: "openai"},
	})

	body := strings.NewReader(`{"id":"acme-vision-1","providerId":"openai","displayName":"Acme Vision","enabled":true,"isCustom":true,"collection":"Mine","contextWindow":200000,"maxOutputTokens":32000,"inputPricePerM":3.5,"outputPricePerM":14,"cacheReadPricePerM":0.18,"cacheWritePricePerM":2,"supportsImages":true}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/models/preference", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST preference = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/models = %d, want 200", rec.Code)
	}
	var models []struct {
		ID                  string  `json:"id"`
		ProviderID          string  `json:"providerId"`
		IsCustom            bool    `json:"isCustom"`
		ContextWindow       int     `json:"contextWindow"`
		CompactAtTokens     int     `json:"compactAtTokens"`
		MaxOutputTokens     int     `json:"maxOutputTokens"`
		InputPricePerM      float64 `json:"inputPricePerM"`
		OutputPricePerM     float64 `json:"outputPricePerM"`
		CacheReadPricePerM  float64 `json:"cacheReadPricePerM"`
		CacheWritePricePerM float64 `json:"cacheWritePricePerM"`
		SupportsImages      bool    `json:"supportsImages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &models); err != nil {
		t.Fatalf("decode models: %v (body: %s)", err, rec.Body.String())
	}

	var found bool
	for _, m := range models {
		if m.ID != "acme-vision-1" || m.ProviderID != "openai" {
			continue
		}
		found = true
		if !m.IsCustom {
			t.Error("isCustom = false, want true")
		}
		if m.ContextWindow != 200000 {
			t.Errorf("contextWindow = %d, want 200000", m.ContextWindow)
		}
		if want := agent.CompactionThreshold(200000); m.CompactAtTokens != want {
			t.Errorf("compactAtTokens = %d, want %d", m.CompactAtTokens, want)
		}
		if m.MaxOutputTokens != 32000 {
			t.Errorf("maxOutputTokens = %d, want 32000", m.MaxOutputTokens)
		}
		if m.InputPricePerM != 3.5 || m.OutputPricePerM != 14 {
			t.Errorf("prices = %v/%v, want 3.5/14", m.InputPricePerM, m.OutputPricePerM)
		}
		if m.CacheReadPricePerM != 0.18 || m.CacheWritePricePerM != 2 {
			t.Errorf("cache prices = %v/%v, want 0.18/2", m.CacheReadPricePerM, m.CacheWritePricePerM)
		}
		if !m.SupportsImages {
			t.Error("supportsImages = false, want true")
		}
	}
	if !found {
		t.Fatalf("custom model missing from GET /api/models (body: %s)", rec.Body.String())
	}
}

// TestListedModelMetadataOverrideRoundTrips pins the edit path for a model the
// catalogue or a provider already lists: POST /api/models/preference with the
// five facts records them as an override layered over the listing, so GET
// /api/models answers with the user's window, output ceiling, prices and image
// support while the model stays a listed model (isCustom false) with its
// collection and catalogue name intact. The override rides the response for the
// editor to prefill, survives a plain enable/disable save, and disappears when
// every field is cleared so the listing answers again.
func TestListedModelMetadataOverrideRoundTrips(t *testing.T) {
	srv := newTestServer(t)
	h := srv.routes()
	srv.registry.ReplaceProviders(map[string]provider.Provider{
		"openrouter": stubProvider{id: "openrouter", models: []provider.ModelInfo{{
			ID: "acme/listed-1", ProviderID: "openrouter", ActiveByDefault: true,
			ContextWindow: 64000, MaxOutputTokens: 4096,
		}}},
	})

	post := func(body string) {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/models/preference", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("POST preference = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
	}
	type override struct {
		ContextWindow       int     `json:"contextWindow"`
		MaxOutputTokens     int     `json:"maxOutputTokens"`
		InputPricePerM      float64 `json:"inputPricePerM"`
		OutputPricePerM     float64 `json:"outputPricePerM"`
		CacheReadPricePerM  float64 `json:"cacheReadPricePerM"`
		CacheWritePricePerM float64 `json:"cacheWritePricePerM"`
		SupportsImages      bool    `json:"supportsImages"`
	}
	type entry struct {
		ID              string    `json:"id"`
		ProviderID      string    `json:"providerId"`
		IsCustom        bool      `json:"isCustom"`
		Enabled         bool      `json:"enabled"`
		ContextWindow   int       `json:"contextWindow"`
		CompactAtTokens int       `json:"compactAtTokens"`
		MaxOutputTokens int       `json:"maxOutputTokens"`
		InputPricePerM  float64   `json:"inputPricePerM"`
		OutputPricePerM float64   `json:"outputPricePerM"`
		SupportsImages  bool      `json:"supportsImages"`
		Override        *override `json:"override"`
	}
	list := func() entry {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/models", nil))
		var models []entry
		if err := json.Unmarshal(rec.Body.Bytes(), &models); err != nil {
			t.Fatalf("decode models: %v (body: %s)", err, rec.Body.String())
		}
		for _, m := range models {
			if m.ID == "acme/listed-1" && m.ProviderID == "openrouter" {
				return m
			}
		}
		t.Fatalf("listed model missing from GET /api/models (body: %s)", rec.Body.String())
		return entry{}
	}

	// The same save the settings editor makes: the row's identity, its enabled
	// state, and the figures the user typed.
	post(`{"id":"acme/listed-1","providerId":"openrouter","displayName":"Acme Listed","enabled":true,"contextWindow":200000,"maxOutputTokens":32000,"inputPricePerM":3.5,"outputPricePerM":14,"cacheReadPricePerM":0.18,"cacheWritePricePerM":2,"supportsImages":true}`)

	got := list()
	if got.IsCustom {
		t.Error("isCustom = true, want false: an edit must not turn a listed model into a custom one")
	}
	if got.ContextWindow != 200000 {
		t.Errorf("contextWindow = %d, want the user's 200000", got.ContextWindow)
	}
	if want := agent.CompactionThreshold(200000); got.CompactAtTokens != want {
		t.Errorf("compactAtTokens = %d, want %d", got.CompactAtTokens, want)
	}
	if got.MaxOutputTokens != 32000 {
		t.Errorf("maxOutputTokens = %d, want the user's 32000", got.MaxOutputTokens)
	}
	if got.InputPricePerM != 3.5 || got.OutputPricePerM != 14 {
		t.Errorf("prices = %v/%v, want the user's 3.5/14", got.InputPricePerM, got.OutputPricePerM)
	}
	if !got.SupportsImages {
		t.Error("supportsImages = false, want the user's yes")
	}
	if got.Override == nil || got.Override.ContextWindow != 200000 || got.Override.InputPricePerM != 3.5 ||
		got.Override.CacheReadPricePerM != 0.18 || got.Override.CacheWritePricePerM != 2 || !got.Override.SupportsImages {
		t.Errorf("override = %+v, want the user's own record, cache prices included", got.Override)
	}

	// A toggle resends the override (the row replaces every metadata column), so
	// disabling the model must not silently forget what the user configured.
	post(`{"id":"acme/listed-1","providerId":"openrouter","displayName":"Acme Listed","enabled":false,"contextWindow":200000,"maxOutputTokens":32000,"inputPricePerM":3.5,"outputPricePerM":14,"cacheReadPricePerM":0.18,"cacheWritePricePerM":2,"supportsImages":true}`)
	got = list()
	if got.Enabled {
		t.Error("enabled = true, want false after the toggle")
	}
	if got.ContextWindow != 200000 || !got.SupportsImages {
		t.Errorf("the toggle dropped the edit: window %d, images %v", got.ContextWindow, got.SupportsImages)
	}

	// Clearing every field is how the editor says "use what the listing says".
	post(`{"id":"acme/listed-1","providerId":"openrouter","displayName":"Acme Listed","enabled":false}`)
	got = list()
	if got.Override != nil {
		t.Errorf("override = %+v, want none after clearing every field", got.Override)
	}
	if got.ContextWindow != 64000 {
		t.Errorf("contextWindow after clearing = %d, want the listed 64000", got.ContextWindow)
	}
	if got.MaxOutputTokens != 4096 {
		t.Errorf("maxOutputTokens after clearing = %d, want the listed 4096", got.MaxOutputTokens)
	}
	if got.SupportsImages {
		t.Error("supportsImages = true, want the listed no")
	}
}
