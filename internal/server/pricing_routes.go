package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// providerPriceCache is an in-memory cache for one provider's pricing data.
type providerPriceCache struct {
	mu        sync.RWMutex
	prices    map[string]float64 // model ID → USD per 1 million input tokens
	fetchedAt time.Time
}

var (
	priceCachesMu sync.Mutex
	priceCaches   = map[string]*providerPriceCache{}
)

func getProviderPriceCache(provider string) *providerPriceCache {
	priceCachesMu.Lock()
	defer priceCachesMu.Unlock()
	if c, ok := priceCaches[provider]; ok {
		return c
	}
	c := &providerPriceCache{}
	priceCaches[provider] = c
	return c
}

// handleGetPricing returns a map of model ID → price (USD per 1M input tokens).
// Query params: provider (required).
func (s *Server) handleGetPricing(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	switch provider {
	case "openrouter":
		s.handleOpenRouterPricing(w, r)
	case "ollama":
		// Local Ollama is free; cloud Ollama has no public pricing API.
		writeJSON(w, http.StatusOK, map[string]float64{})
	case "":
		writeError(w, http.StatusBadRequest, "provider is required")
	default:
		writeError(w, http.StatusBadRequest, "real-time pricing not available for provider: "+provider)
	}
}

func (s *Server) handleOpenRouterPricing(w http.ResponseWriter, r *http.Request) {
	cache := getProviderPriceCache("openrouter")

	cache.mu.RLock()
	if cache.prices != nil && time.Since(cache.fetchedAt) < time.Hour {
		prices := cache.prices
		cache.mu.RUnlock()
		writeJSON(w, http.StatusOK, prices)
		return
	}
	cache.mu.RUnlock()

	// Resolve API key: DB config takes precedence over env var. Provider configs
	// are stored in the global config DB (shared across projects), so read there —
	// s.db (per-project) never holds them.
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if cfg, err := session.GetProviderConfig(s.globalDB, "openrouter"); err == nil && cfg.APIKey != "" {
		apiKey = cfg.APIKey
	}

	prices, err := fetchOpenRouterPrices(r.Context(), apiKey)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to fetch OpenRouter pricing: "+err.Error())
		return
	}

	cache.mu.Lock()
	cache.prices = prices
	cache.fetchedAt = time.Now()
	cache.mu.Unlock()

	writeJSON(w, http.StatusOK, prices)
}

func fetchOpenRouterPrices(ctx context.Context, apiKey string) (map[string]float64, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "GET", "https://openrouter.ai/api/v1/models", nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Data []struct {
			ID      string `json:"id"`
			Pricing struct {
				Prompt string `json:"prompt"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	prices := make(map[string]float64, len(result.Data))
	for _, m := range result.Data {
		p, err := strconv.ParseFloat(strings.TrimSpace(m.Pricing.Prompt), 64)
		if err != nil || p <= 0 {
			continue
		}
		// pricing.prompt is per token → convert to per million tokens.
		prices[m.ID] = p * 1_000_000
	}
	return prices, nil
}
