package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ogxGateway stands in for OG Lab's gateway: GET /v1/models returns the model
// list the plan grants, POST /v1/chat/completions records the request body.
func ogxGateway(t *testing.T, models []string, chatBody *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			data := make([]map[string]any, 0, len(models))
			for _, id := range models {
				data = append(data, map[string]any{"id": id, "object": "model", "owned_by": "oglab"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
		case "/chat/completions":
			if chatBody != nil {
				_ = json.NewDecoder(r.Body).Decode(chatBody)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestOGXGatewayURL pins the env override and the default.
func TestOGXGatewayURL(t *testing.T) {
	t.Setenv("OGX_GATEWAY_URL", "  ")
	if got := OGXGatewayURL(); got != DefaultOGXGatewayURL {
		t.Errorf("blank env: got %q, want the default", got)
	}
	t.Setenv("OGX_GATEWAY_URL", " http://127.0.0.1:9999/v1 ")
	if got := OGXGatewayURL(); got != "http://127.0.0.1:9999/v1" {
		t.Errorf("env override: got %q (should be trimmed)", got)
	}
}

func TestNewOGXProviderRejectsEmptyToken(t *testing.T) {
	if _, err := NewOGXProvider("   "); err == nil {
		t.Fatal("empty token accepted")
	}
	p, err := NewOGXProvider(" tok ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.ID() != OGXProviderID {
		t.Errorf("id = %q, want %q", p.ID(), OGXProviderID)
	}
	if p.apiKey != "tok" {
		t.Errorf("token = %q, want it trimmed", p.apiKey)
	}
}

// TestOGXProviderModelsComeFromTheGateway pins the whole design: every model the
// plan grants arrives enabled, the collection stays empty so the UI groups them
// under "ogx", and the default is resolved from the catalogue because the
// provider is constructed with no model of its own.
func TestOGXProviderModelsComeFromTheGateway(t *testing.T) {
	srv := ogxGateway(t, []string{"ogx-deepseek-v4.1-flash", "ogx-glm-5.3-flash"}, nil)
	t.Setenv("OGX_GATEWAY_URL", srv.URL)

	p, err := NewOGXProvider("tok")
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	// The catalogue is fetched by an explicit RefreshCatalog; Models() is a pure
	// read of whatever that installed (the freeze fix).
	if got := p.RefreshCatalog(context.Background()); len(got) != 2 {
		t.Fatalf("RefreshCatalog got %d models, want 2: %+v", len(got), got)
	}
	list := p.Models()
	if len(list) != 2 {
		t.Fatalf("got %d models, want 2: %+v", len(list), list)
	}
	for _, m := range list {
		if !m.ActiveByDefault {
			t.Errorf("model %q not enabled by default — everything the plan grants should be", m.ID)
		}
		if m.ProviderID != OGXProviderID {
			t.Errorf("model %q providerId = %q", m.ID, m.ProviderID)
		}
		if m.Collection != "" {
			t.Errorf("model %q collection = %q, want empty so the UI groups by provider", m.ID, m.Collection)
		}
	}
	if p.defaultModel() != "ogx-deepseek-v4.1-flash" {
		t.Errorf("default = %q, want the first granted model", p.defaultModel())
	}
	for _, m := range list {
		if m.Default != (m.ID == "ogx-deepseek-v4.1-flash") {
			t.Errorf("model %q default flag = %v", m.ID, m.Default)
		}
	}
}

// TestDynamicModelsFallBackToCatalogNames pins the naming fallback for a silent
// endpoint that is not OGX (a local Ollama, say). It reports no human name, so
// the model shows the catalogue's curated name for an id we recognise, and the
// raw id only when the id is unknown to the catalogue.
func TestDynamicModelsFallBackToCatalogNames(t *testing.T) {
	srv := ogxGateway(t, []string{
		"deepseek-v4.1-flash",
		"glm-5.3-flash",
		"totally-unknown-model-9",
	}, nil)

	p := &OpenAIProvider{id: "ollama", baseURL: srv.URL}
	got := p.RefreshCatalog(context.Background())
	if len(got) != 3 {
		t.Fatalf("RefreshCatalog got %d models, want 3: %+v", len(got), got)
	}

	want := map[string]string{
		"deepseek-v4.1-flash":     "DeepSeek V4.1 Flash",
		"glm-5.3-flash":           "GLM-5.3 Flash",
		"totally-unknown-model-9": "totally-unknown-model-9",
	}
	for _, m := range got {
		if m.Name != want[m.ID] {
			t.Errorf("model %q name = %q, want %q", m.ID, m.Name, want[m.ID])
		}
		if m.ID == "" {
			t.Errorf("model lost its id: %+v", m)
		}
	}
}

// TestOGXProviderShowsRawModelIDs pins the deliberate exception to the naming
// fallback: the OGX gateway advertises house ids that ARE what the plan grants,
// so unlike every other silent endpoint it shows the raw id as the name rather
// than substituting a catalogue name.
func TestOGXProviderShowsRawModelIDs(t *testing.T) {
	srv := ogxGateway(t, []string{"ogx-deepseek-v4.1-flash", "ogx-glm-5.3-flash"}, nil)
	t.Setenv("OGX_GATEWAY_URL", srv.URL)

	p, err := NewOGXProvider("tok")
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	got := p.RefreshCatalog(context.Background())
	if len(got) != 2 {
		t.Fatalf("RefreshCatalog got %d models, want 2: %+v", len(got), got)
	}
	for _, m := range got {
		if m.Name != m.ID {
			t.Errorf("model %q name = %q, want the raw id", m.ID, m.Name)
		}
	}
}

// TestOGXProviderEmptyCatalogueHasNoFallback is the deliberate difference from
// every other OpenAI-compatible provider here: a plan that grants nothing yields
// no models at all, rather than a static catalogue of things the token cannot
// reach.
func TestOGXProviderEmptyCatalogueHasNoFallback(t *testing.T) {
	srv := ogxGateway(t, nil, nil)
	t.Setenv("OGX_GATEWAY_URL", srv.URL)

	p, _ := NewOGXProvider("tok")
	if got := p.RefreshCatalog(context.Background()); len(got) != 0 {
		t.Fatalf("RefreshCatalog got %d models from an empty plan, want none: %+v", len(got), got)
	}
	if list := p.Models(); len(list) != 0 {
		t.Fatalf("got %d models from an empty plan, want none: %+v", len(list), list)
	}
}

// TestOGXProviderSendsPromptCacheKey pins the gateway's session identity: the
// field is not a cache hint here, so it must ride every request.
func TestOGXProviderSendsPromptCacheKey(t *testing.T) {
	p, _ := NewOGXProvider("tok")
	if !p.sendsPromptCacheKey() {
		t.Fatal("ogx provider must send prompt_cache_key")
	}
	// A custom slot pointed straight at the gateway host, without the id.
	byHost := &OpenAIProvider{id: "openai", baseURL: "https://ogx.ogcode.in/v1"}
	if !byHost.sendsPromptCacheKey() {
		t.Error("a base URL on the ogx gateway host must send prompt_cache_key")
	}
}

// TestOGXProviderChatRequestCarriesCacheKey goes one level below the predicate:
// the key must actually appear in the posted body, since that is what the
// gateway reads to attribute token spend.
func TestOGXProviderChatRequestCarriesCacheKey(t *testing.T) {
	var body map[string]any
	srv := ogxGateway(t, nil, &body)
	t.Setenv("OGX_GATEWAY_URL", srv.URL)

	p, _ := NewOGXProvider("tok")
	ch, err := p.StreamChat(context.Background(), StreamRequest{
		Model:    "m",
		Messages: []ModelMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
		CacheKey: "ses_abc",
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	for range ch {
	}

	if got, _ := body["prompt_cache_key"].(string); got != "ses_abc" {
		t.Fatalf("prompt_cache_key = %v, want ses_abc (body: %v)", body["prompt_cache_key"], body)
	}
}
