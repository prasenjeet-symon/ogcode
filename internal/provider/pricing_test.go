package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// pricedClaude is a catalogued Claude model with published cache prices, so the
// tests follow the catalogue rather than pinning numbers that change with it.
func pricedClaude(t *testing.T) CatalogModel {
	t.Helper()
	for _, m := range AnthropicModels {
		if m.InputPricePerM > 0 && m.CacheReadPricePerM > 0 && m.CacheWritePricePerM > 0 {
			return m
		}
	}
	t.Fatal("no Claude model with published cache prices")
	return CatalogModel{}
}

// A first-party API bills the vendor's list price, cache prices included, under
// whatever spelling of the id the user typed.
func TestPriceOnQuotesTheCatalogueForMeteredAPIs(t *testing.T) {
	cm := pricedClaude(t)
	want := ModelPrice{Input: cm.InputPricePerM, Output: cm.OutputPricePerM, CacheRead: cm.CacheReadPricePerM, CacheWrite: cm.CacheWritePricePerM}
	for _, id := range []string{cm.ID, "anthropic/" + cm.ID} {
		got, ok := NewRegistry().PriceOn("anthropic", id)
		if !ok || got != want {
			t.Errorf("PriceOn(anthropic, %q) = %+v, %v; want %+v", id, got, ok, want)
		}
	}
}

// Local Ollama is free and Ollama Cloud is a subscription: a catalogued model
// served there has no per-token price to quote. OGX meters its own published
// rates and is pinned separately below.
func TestPriceOnUnmeteredProvidersQuoteNothing(t *testing.T) {
	cm := pricedClaude(t)
	if got, ok := NewRegistry().PriceOn("ollama", cm.ID); ok {
		t.Errorf("PriceOn(ollama) = %+v, want no price", got)
	}
}

// OGX meters the plan's own rates: what the gateway publishes is the bill, the
// vendor's catalogue price is not a stand-in for it, and the user's edits layer
// on top of the published rate. Nothing published means nothing quotes —
// unpriced, not free, not the vendor's price.
func TestPriceOnOGXQuotesOnlyTheGateway(t *testing.T) {
	cm := pricedClaude(t)
	if got, ok := NewRegistry().PriceOn("ogx", cm.ID); ok {
		t.Errorf("unpublished ogx quote = %+v, %v; want none — the catalogue is not the plan's rates", got, ok)
	}
	reg := NewRegistry()
	reg.Register(windowProvider{id: "ogx", models: []ModelInfo{
		// The gateway publishes its own cache-read rate alongside the plan's
		// input/output rates (input_cache_read on the wire); every figure must
		// come through, and the vendor's catalogue prices must not leak in.
		{ID: cm.ID, InputPricePerM: 1, OutputPricePerM: 5, CacheReadPricePerM: 0.009},
	}})
	got, ok := reg.PriceOn("ogx", cm.ID)
	if want := (ModelPrice{Input: 1, Output: 5, CacheRead: 0.009}); got != want || !ok {
		t.Errorf("published ogx rate = %+v, %v; want %+v", got, ok, want)
	}
	// The user's edit layers on top, field by field — but the gateway's own
	// cache rate survives it: the user edits only the input/output fields.
	reg.SetModelOverride(cm.ID, CustomModelInfo{ProviderID: "ogx", InputPricePerM: 2})
	got, ok = reg.PriceOn("ogx", cm.ID)
	if want := (ModelPrice{Input: 2, Output: 5, CacheRead: 0.009}); got != want || !ok {
		t.Errorf("edited ogx rate = %+v, %v; want %+v", got, ok, want)
	}

	// A cache rate the user records wins over the gateway's published one;
	// an unset cache field still falls back to the gateway's own figure, even
	// when the user edits the input price or a different cache field.
	reg.Register(windowProvider{id: "ogx", models: []ModelInfo{
		{ID: "ogx/user-read", InputPricePerM: 1, OutputPricePerM: 5, CacheReadPricePerM: 0.009, CacheWritePricePerM: 0.03},
		{ID: "ogx/user-write", InputPricePerM: 1, OutputPricePerM: 5, CacheReadPricePerM: 0.009, CacheWritePricePerM: 0.03},
	}})
	reg.SetModelOverride("ogx/user-read", CustomModelInfo{ProviderID: "ogx", CacheReadPricePerM: 0.02})
	got, ok = reg.PriceOn("ogx", "ogx/user-read")
	if want := (ModelPrice{Input: 1, Output: 5, CacheRead: 0.02, CacheWrite: 0.03}); got != want || !ok {
		t.Errorf("user ogx cache-read rate = %+v, %v; want %+v", got, ok, want)
	}
	reg.SetModelOverride("ogx/user-write", CustomModelInfo{ProviderID: "ogx", InputPricePerM: 2, CacheWritePricePerM: 0.3})
	got, ok = reg.PriceOn("ogx", "ogx/user-write")
	if want := (ModelPrice{Input: 2, Output: 5, CacheRead: 0.009, CacheWrite: 0.3}); got != want || !ok {
		t.Errorf("user ogx cache-write rate = %+v, %v; want %+v — the gateway's read rate survives", got, ok, want)
	}
}

// A provider's own listed price is what the user pays there. The vendor's cache
// prices apply only when the listing matches the vendor's list price.
func TestPriceOnPrefersAListedPrice(t *testing.T) {
	cm := pricedClaude(t)
	reg := NewRegistry()
	reg.Register(windowProvider{id: "openrouter", models: []ModelInfo{
		{ID: "anthropic/" + cm.ID, InputPricePerM: cm.InputPricePerM, OutputPricePerM: cm.OutputPricePerM},
		{ID: "vendor/marked-up", InputPricePerM: 7, OutputPricePerM: 21},
	}})

	got, ok := reg.PriceOn("openrouter", "anthropic/"+cm.ID)
	want := ModelPrice{Input: cm.InputPricePerM, Output: cm.OutputPricePerM, CacheRead: cm.CacheReadPricePerM, CacheWrite: cm.CacheWritePricePerM}
	if !ok || got != want {
		t.Errorf("same-as-vendor listing = %+v, %v; want %+v", got, ok, want)
	}
	got, ok = reg.PriceOn("openrouter", "vendor/marked-up")
	if want := (ModelPrice{Input: 7, Output: 21}); !ok || got != want {
		t.Errorf("listed price = %+v, %v; want %+v with cache prices left to the caller", got, ok, want)
	}
}

func TestPriceOnFreeVariantIsFree(t *testing.T) {
	cm := pricedClaude(t)
	got, ok := NewRegistry().PriceOn("openrouter", "anthropic/"+cm.ID+":free")
	if !ok || got != (ModelPrice{}) {
		t.Errorf("free variant = %+v, %v; want a zero price", got, ok)
	}
}

func TestPriceOnUnknownModelQuotesNothing(t *testing.T) {
	if got, ok := NewRegistry().PriceOn("openai", "vendor/nobody-prices-this"); ok {
		t.Errorf("unknown model priced at %+v", got)
	}
	if got, ok := NewRegistry().PriceOn("openai", ""); ok {
		t.Errorf("empty id priced at %+v", got)
	}
}

func TestOAIPricePerMCoercion(t *testing.T) {
	for _, c := range []struct {
		in   any
		want float64
	}{
		{"0.000003", 3},
		{"0.00000015", 0.15},
		{" 0.0000125 ", 12.5},
		{float64(0.0000011), 1.1},
		{"0", 0},
		{"-1", 0}, // a router's "depends on the model" marker
		{"", 0},
		{"free", 0},
		{nil, 0},
		{true, 0},
	} {
		if got := oaiPricePerM(c.in); got != c.want {
			t.Errorf("oaiPricePerM(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// End-to-end through fetchDynamicModels: OpenRouter's per-token strings become
// per-million prices, and an endpoint that sends none leaves the price unknown.
func TestFetchDynamicModelsCarriesPricing(t *testing.T) {
	body := `{"data":[
		{"id":"anthropic/claude-sonnet-4.6","pricing":{"prompt":"0.000003","completion":"0.000015"}},
		{"id":"openrouter/auto","pricing":{"prompt":"-1","completion":"-1"}},
		{"id":"deepseek-chat"}
	]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	p := &OpenAIProvider{id: "openrouter", baseURL: srv.URL, model: "m"}
	byID := map[string][2]float64{}
	for _, m := range p.fetchDynamicModels(context.Background()) {
		byID[m.ID] = [2]float64{m.InputPricePerM, m.OutputPricePerM}
	}
	for id, want := range map[string][2]float64{
		"anthropic/claude-sonnet-4.6": {3, 15},
		"openrouter/auto":             {0, 0},
		"deepseek-chat":               {0, 0},
	} {
		if byID[id] != want {
			t.Errorf("%s: price = %v, want %v", id, byID[id], want)
		}
	}
}

// Cost bills each kind of token at its own price: cache traffic at the published
// cache prices, or at the standard multiples of the input price when there are
// none.
func TestModelPriceCostPricesEachKind(t *testing.T) {
	published := ModelPrice{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}
	// 1M of each: 3 + 15 + 0.3 + 3.75.
	if got, want := published.Cost(1e6, 1e6, 1e6, 1e6), 22.05; !near(got, want) {
		t.Errorf("published Cost = %v, want %v", got, want)
	}
	derived := ModelPrice{Input: 2, Output: 8}
	want := 2*CacheReadMultiplier + 2*CacheWriteMultiplier
	if got := derived.Cost(0, 0, 1e6, 1e6); !near(got, want) {
		t.Errorf("derived cache Cost = %v, want %v", got, want)
	}
}

// ListPrice is the catalogue's price whatever serves the model — the yardstick
// for a flat plan — and nothing for a model the catalogue does not price.
func TestListPriceIgnoresTheProvider(t *testing.T) {
	cm := pricedClaude(t)
	got, ok := ListPrice(cm.ID)
	if !ok || got.Input != cm.InputPricePerM || got.Output != cm.OutputPricePerM {
		t.Errorf("ListPrice(%q) = %+v, %v; want the catalogue's price", cm.ID, got, ok)
	}
	// Served on Ollama Cloud under its host name, it still has a list price.
	if _, ok := ListPrice("glm-5.3-flash:cloud"); !ok {
		t.Error("ListPrice(glm-5.3-flash:cloud) = none, want GLM-5.3 Flash's list price")
	}
	if _, ok := ListPrice("no-such-model-anywhere"); ok {
		t.Error("ListPrice of an unknown model should be none")
	}
}

func near(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }

// A slot is named by the host it calls only when that host is not the slot's
// own: Z.ai behind the OpenAI slot and DeepSeek behind the Anthropic one are
// named; the default endpoints, OGX and a local Ollama are not.
func TestEndpointHostNamesOnlyRepointedSlots(t *testing.T) {
	for _, env := range []string{"OPENAI_BASE_URL", "ANTHROPIC_BASE_URL", "OLLAMA_BASE_URL"} {
		t.Setenv(env, "")
	}
	cases := []struct {
		id, base, want string
	}{
		{"openai", "https://api.z.ai/api/paas/v4", "api.z.ai"},
		{"anthropic", "https://api.deepseek.com/anthropic", "api.deepseek.com"},
		{"openai", "", ""},
		{"anthropic", "", ""},
		{"ollama", "http://localhost:11434/v1", ""},
		{"ollama", "https://ollama.com/v1", ""},
		{"ollama", "https://gpu-box.example.net/v1", "gpu-box.example.net"},
	}
	for _, c := range cases {
		p, err := NewProviderWithConfig(c.id, "test-key", c.base)
		if err != nil {
			t.Fatalf("%s %q: %v", c.id, c.base, err)
		}
		if got := EndpointHost(p); got != c.want {
			t.Errorf("EndpointHost(%s at %q) = %q, want %q", c.id, c.base, got, c.want)
		}
	}
	if got := EndpointHost(nil); got != "" {
		t.Errorf("EndpointHost(nil) = %q", got)
	}
}

// TestPriceOnHonoursUserPricesForCustomModels pins the user's own per-1M prices
// for a model the catalogue does not know: they win over the provider's listing
// and the catalogue, field by field, so a user who knows only one of the two
// fills in the other. The rules that decide before any price is read still win —
// an unmetered provider bills nothing and a ":free" variant stays free.
func TestPriceOnHonoursUserPricesForCustomModels(t *testing.T) {
	cm := pricedClaude(t)
	reg := NewRegistry()
	// The provider's own listing is what the user's price has to beat.
	reg.Register(windowProvider{id: "openrouter", models: []ModelInfo{
		{ID: "vendor/hand-added", InputPricePerM: 1, OutputPricePerM: 2},
		{ID: "vendor/output-only", InputPricePerM: 1, OutputPricePerM: 2},
		{ID: "vendor/cached", InputPricePerM: 1, OutputPricePerM: 2, CacheReadPricePerM: 0.5, CacheWritePricePerM: 1.25},
		{ID: "vendor/cache-read-only", InputPricePerM: 1, OutputPricePerM: 2, CacheReadPricePerM: 0.75, CacheWritePricePerM: 1.25},
	}})
	reg.RegisterCustomModelInfo("vendor/hand-added", CustomModelInfo{ProviderID: "openrouter", InputPricePerM: 3, OutputPricePerM: 9})
	reg.RegisterCustomModelInfo("vendor/output-only", CustomModelInfo{ProviderID: "openrouter", OutputPricePerM: 99})
	reg.RegisterCustomModelInfo("vendor/hand-added:free", CustomModelInfo{ProviderID: "openrouter", InputPricePerM: 3})
	reg.RegisterCustomModelInfo("vendor/other-provider", CustomModelInfo{ProviderID: "openai", InputPricePerM: 3, OutputPricePerM: 9})
	// Cache rates the user recorded by hand — the real figure a self-metered
	// gateway publishes, not a multiple of anything: each wins over the
	// listing's and the catalogue's on its own; an unset one keeps today's
	// fallback exactly.
	reg.RegisterCustomModelInfo("vendor/cached", CustomModelInfo{ProviderID: "openrouter", InputPricePerM: 3, OutputPricePerM: 9, CacheReadPricePerM: 0.18, CacheWritePricePerM: 2})
	reg.RegisterCustomModelInfo("vendor/cache-read-only", CustomModelInfo{ProviderID: "openrouter", CacheReadPricePerM: 0.2})
	// A catalogued model the user repriced: only its input price is theirs.
	reg.RegisterCustomModelInfo(cm.ID, CustomModelInfo{ProviderID: "anthropic", InputPricePerM: 5})

	tests := []struct {
		name       string
		providerID string
		modelID    string
		want       ModelPrice
		wantOK     bool
	}{
		{
			name:       "user prices win over the provider's listing",
			providerID: "openrouter", modelID: "vendor/hand-added",
			want: ModelPrice{Input: 3, Output: 9}, wantOK: true,
		},
		{
			name:       "a zero field falls back to the listing",
			providerID: "openrouter", modelID: "vendor/output-only",
			want: ModelPrice{Input: 1, Output: 99}, wantOK: true,
		},
		{
			name:       "user cache rates win over the listing, field by field",
			providerID: "openrouter", modelID: "vendor/cached",
			want: ModelPrice{Input: 3, Output: 9, CacheRead: 0.18, CacheWrite: 2}, wantOK: true,
		},
		{
			name:       "a lone cache rate still edits, and unset cache fields fall back",
			providerID: "openrouter", modelID: "vendor/cache-read-only",
			want: ModelPrice{Input: 1, Output: 2, CacheRead: 0.2, CacheWrite: 1.25}, wantOK: true,
		},
		{
			name:       "a user input price drops the catalogue's cache multipliers",
			providerID: "anthropic", modelID: cm.ID,
			want: ModelPrice{Input: 5, Output: cm.OutputPricePerM}, wantOK: true,
		},
		{
			name:       "an unmetered provider bills nothing whatever the user typed",
			providerID: "ollama", modelID: "vendor/hand-added",
			want: ModelPrice{}, wantOK: false,
		},
		{
			name:       "a free variant stays free",
			providerID: "openrouter", modelID: "vendor/hand-added:free",
			want: ModelPrice{}, wantOK: true,
		},
		{
			name:       "a price configured for another provider is not applied",
			providerID: "openrouter", modelID: "vendor/other-provider",
			want: ModelPrice{}, wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := reg.PriceOn(tt.providerID, tt.modelID)
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("PriceOn(%s, %s) = %+v, %v; want %+v, %v", tt.providerID, tt.modelID, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
