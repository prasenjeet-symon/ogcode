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

// Local Ollama is free and Ollama Cloud and OGX are subscriptions: a catalogued
// model served there has no per-token price to quote.
func TestPriceOnUnmeteredProvidersQuoteNothing(t *testing.T) {
	cm := pricedClaude(t)
	for _, pid := range []string{"ollama", "ogx"} {
		if got, ok := NewRegistry().PriceOn(pid, cm.ID); ok {
			t.Errorf("PriceOn(%s) = %+v, want no price", pid, got)
		}
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
