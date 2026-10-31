package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestOAIContextWindowCoercion pins the tolerant parse of a models-list
// context_length: number, numeric string, and everything that must stay 0
// (absent, junk, zero, negative) — 0 is "unknown", never guessed.
func TestOAIContextWindowCoercion(t *testing.T) {
	cases := []struct {
		in   any
		want int
	}{
		{float64(1048576), 1048576},
		{"262144", 262144},
		{" 131072 ", 131072},
		{"131,072", 0},
		{"", 0},
		{nil, 0},
		{float64(0), 0},
		{float64(-8192), 0},
		{"nonsense", 0},
		{true, 0},
		{float64(1e300), 0}, // overflow must not wrap into a positive int
	}
	for _, c := range cases {
		if got := oaiContextWindow(c.in); got != c.want {
			t.Errorf("oaiContextWindow(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

// End-to-end through fetchDynamicModels: an endpoint that reports
// context_length (OpenRouter does) must carry it into ModelInfo, and an
// endpoint that omits it (Ollama) must leave the window at 0.
func TestFetchDynamicModelsCarriesContextLength(t *testing.T) {
	body := `{"data":[
		{"id":"anthropic/claude-sonnet-4.6","name":"Claude Sonnet 4.6","context_length":200000},
		{"id":"a/str","name":"String length","context_length":"400000"},
		{"id":"mystery/no-length","name":"No length"}
	]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	p := &OpenAIProvider{id: "openrouter", baseURL: srv.URL, model: "m"}
	got := p.fetchDynamicModels(context.Background())
	if len(got) != 3 {
		t.Fatalf("got %d models, want 3", len(got))
	}
	byID := map[string]int{}
	for _, m := range got {
		byID[m.ID] = m.ContextWindow
	}
	if byID["anthropic/claude-sonnet-4.6"] != 200000 {
		t.Errorf("numeric context_length = %d, want 200000", byID["anthropic/claude-sonnet-4.6"])
	}
	if byID["a/str"] != 400000 {
		t.Errorf("string context_length = %d, want 400000", byID["a/str"])
	}
	if byID["mystery/no-length"] != 0 {
		t.Errorf("missing context_length = %d, want 0 (unknown)", byID["mystery/no-length"])
	}
}

// fetchDynamicModels must carry a listing's own cache prices into ModelInfo
// (OpenRouter and the OGX gateway send input_cache_read/input_cache_write —
// "-1" or absent = unknown, 0). Without this a self-metered gateway's
// cache-read rate is dropped and the biller guesses 0.1x the input price.
func TestFetchDynamicModelsCarriesCachePrices(t *testing.T) {
	body := `{"data":	[
		{"id":"ogx-deepseek-v4.1-flash","name":"DeepSeek V4.1 Flash","pricing":{"prompt":"0.00000045","completion":"0.0000018","input_cache_read":"0.000000009"}},
		{"id":"router/marked","name":"Marked","pricing":{"prompt":"0.000002","completion":"0.000006","input_cache_read":"0.0000002","input_cache_write":"-1"}},
		{"id":"plain/no-pricing","name":"No pricing object"}
	]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	p := &OpenAIProvider{id: "ogx", baseURL: srv.URL, model: "m"}
	got := p.fetchDynamicModels(context.Background())
	if len(got) != 3 {
		t.Fatalf("got %d models, want 3", len(got))
	}
	byID := map[string]ModelInfo{}
	for _, m := range got {
		byID[m.ID] = m
	}
	if m := byID["ogx-deepseek-v4.1-flash"]; m.InputPricePerM != 0.45 || m.OutputPricePerM != 1.8 ||
		m.CacheReadPricePerM != 0.009 || m.CacheWritePricePerM != 0 {
		t.Errorf("gateway listing cache prices = %+v; want input 0.45, output 1.8, cache read 0.009, cache write 0", m)
	}
	if m := byID["router/marked"]; m.CacheReadPricePerM != 0.2 || m.CacheWritePricePerM != 0 {
		t.Errorf("marked-up listing cache prices = read %v, write %v; want 0.2 and 0 (-1 = unknown)", m.CacheReadPricePerM, m.CacheWritePricePerM)
	}
	if m := byID["plain/no-pricing"]; m.CacheReadPricePerM != 0 || m.CacheWritePricePerM != 0 {
		t.Errorf("unpriced listing cache prices = read %v, write %v; want 0 and 0", m.CacheReadPricePerM, m.CacheWritePricePerM)
	}
}

// Registry.ContextWindow must resolve the window through the provider's model
// list, returning 0 for an unknown model id.
func TestRegistryContextWindowResolvesListedModels(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&fakeProvider{id: "mock", models: []ModelInfo{
		{ID: "known-model", ProviderID: "mock", ContextWindow: 200000},
		{ID: "no-window", ProviderID: "mock", ContextWindow: 0},
	}})

	if got := reg.ContextWindow("known-model"); got != 200000 {
		t.Errorf("ContextWindow(known-model) = %d, want 200000", got)
	}
	if got := reg.ContextWindow("no-window"); got != 0 {
		t.Errorf("ContextWindow(no-window) = %d, want 0", got)
	}
	if got := reg.ContextWindow("absent-model"); got != 0 {
		t.Errorf("ContextWindow(absent-model) = %d, want 0", got)
	}
	if got := reg.ContextWindow(""); got != 0 {
		t.Errorf("ContextWindow(\"\") = %d, want 0", got)
	}
}

// The oaiModelsResponse must tolerate endpoints that send context_length as a
// number without breaking the existing id/name-only decoding.
func TestOAIModelsResponseDecode(t *testing.T) {
	var resp oaiModelsResponse
	body := []byte(`{"data":[{"id":"m1","context_length":8192},{"id":"m2"}]}`)
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("got %d entries, want 2", len(resp.Data))
	}
	if got := oaiContextWindow(resp.Data[0].ContextLength); got != 8192 {
		t.Errorf("m1 window = %d, want 8192", got)
	}
	if got := oaiContextWindow(resp.Data[1].ContextLength); got != 0 {
		t.Errorf("m2 window = %d, want 0", got)
	}
}
