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
	srv := ogxGateway(t, []string{"deepseek-v4.1-flash", "glm-5.3-flash"}, nil)
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
	if p.defaultModel() != "deepseek-v4.1-flash" {
		t.Errorf("default = %q, want the first granted model", p.defaultModel())
	}
	for _, m := range list {
		if m.Default != (m.ID == "deepseek-v4.1-flash") {
			t.Errorf("model %q default flag = %v", m.ID, m.Default)
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
	byHost := &OpenAIProvider{id: "openai", baseURL: "https://ogx.ogcode.xyz/v1"}
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
