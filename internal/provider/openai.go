package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// OpenAIProvider implements Provider for the OpenAI Chat Completions API.
// Also used for OpenRouter and Ollama (same API format, different base URL).
// When configured for an OpenAI-compatible third party (DeepSeek, Gemini, Groq,
// …) via a custom base URL, the `collection` field tags dynamically-fetched
// models so the UI can group them instead of collapsing them under "openai".
type OpenAIProvider struct {
	id         string
	apiKey     string
	model      string
	baseURL    string
	collection string // grouping label for dynamically-fetched models ("" = none)

	// appID and appSecret, when both set, make this provider assert its
	// first-party identity to the router it calls (the OGX gateway). They are
	// empty for every other endpoint, which leaves its requests unasserted.
	appID     string
	appSecret string

	// cachedModels is the provider's model catalogue: the list last fetched from
	// the endpoint (or seeded from the persisted copy at startup). Models() is a
	// pure read of it and never touches the network. Nil means nothing has been
	// loaded yet, so Models() answers from the compiled-in fallback instead.
	cachedModels []ModelInfo
	modelsMu     sync.Mutex

	// modelExplicit records that `model` came from configuration rather than a
	// guess, so it is never overridden by what the endpoint turns out to serve.
	modelExplicit bool
	// resolvedModel holds a default inferred from the fetched model list
	// (string). Kept separate from `model`, which stays immutable after
	// construction so concurrent readers need no lock.
	resolvedModel atomic.Value
}

// resolveDefaultModel picks the model to use when a request names none, unless
// one was configured explicitly. Preference order: keep the configured value if
// the endpoint actually serves it, else the first enabled model, else the first
// model at all. This is what stops a proxied endpoint — which looks local but
// serves cloud models — from defaulting to a model that does not exist there.
func (p *OpenAIProvider) resolveDefaultModel(list []ModelInfo) {
	if p.modelExplicit || len(list) == 0 {
		return
	}
	for _, m := range list {
		if m.ID == p.model {
			return
		}
	}
	for _, m := range list {
		if m.ActiveByDefault {
			p.resolvedModel.Store(m.ID)
			return
		}
	}
	p.resolvedModel.Store(list[0].ID)
}

// defaultModel returns the model to use when a request does not name one: the
// configured value, or one inferred from the endpoint's actual model list.
func (p *OpenAIProvider) defaultModel() string {
	if v, ok := p.resolvedModel.Load().(string); ok && v != "" {
		return v
	}
	return p.model
}

// NewOpenAIProvider creates an OpenAIProvider from the OPENAI_* env vars.
func NewOpenAIProvider() *OpenAIProvider {
	apiKey := os.Getenv("OPENAI_API_KEY")
	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		model = "gpt-4o"
	}
	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAIProvider{id: "openai", apiKey: apiKey, model: model, baseURL: baseURL, collection: collectionFromBaseURL(baseURL)}
}

// NewOpenRouterProvider creates an OpenAI-compatible provider for OpenRouter.
func NewOpenRouterProvider() *OpenAIProvider {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	model := os.Getenv("OPENROUTER_MODEL")
	if model == "" {
		model = "anthropic/claude-sonnet-4.6"
	}
	return &OpenAIProvider{
		id:      "openrouter",
		apiKey:  apiKey,
		model:   model,
		baseURL: "https://openrouter.ai/api/v1",
	}
}

// NewOllamaProvider creates an OpenAI-compatible provider for Ollama.
// When OLLAMA_BASE_URL points to a cloud endpoint (not localhost), the model
// list is fetched dynamically from /v1/models. For local Ollama, a static
// fallback list is used.
func NewOllamaProvider() *OpenAIProvider {
	baseURL := os.Getenv("OLLAMA_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:11434/v1"
	}
	apiKey := os.Getenv("OLLAMA_API_KEY")
	model := os.Getenv("OLLAMA_MODEL")
	explicit := model != ""
	if model == "" {
		// A placeholder only. This guess is frequently wrong — a proxied
		// endpoint looks local but serves cloud models — so Models() replaces
		// it with something the endpoint actually reports.
		if isCloudURL(baseURL) {
			model = "qwen3-coder-next"
		} else {
			model = "qwen3"
		}
	}
	return &OpenAIProvider{
		id:            "ollama",
		apiKey:        apiKey,
		model:         model,
		baseURL:       baseURL,
		modelExplicit: explicit,
	}
}

func (p *OpenAIProvider) ID() string { return p.id }

// BaseURL returns the API base URL the provider is configured to use. Exposed
// so callers can compare endpoints without reaching into the unexported field
// directly.
func (p *OpenAIProvider) BaseURL() string { return p.baseURL }

// SetCatalog seeds the in-memory catalogue from a persisted copy, so Models()
// answers with the last known list immediately on startup instead of waiting for
// a fetch that has not run yet. An empty list is ignored — it must never wipe a
// live catalogue a refresh has already installed.
func (p *OpenAIProvider) SetCatalog(models []ModelInfo) {
	if len(models) == 0 {
		return
	}
	p.storeCatalog(models)
}

// RefreshCatalog fetches the endpoint's live model list, installs it as the
// cached catalogue, and returns it so the caller can persist it. It is the ONLY
// path that talks to the network for models — Models() never does.
//
// A nil return means "no live catalogue": the fetch failed, or this provider has
// a compiled-in list that needs no fetch. The cached catalogue is left untouched
// in that case, so a transient failure never wipes a good list — Models() falls
// back to the compiled-in one. A non-nil (possibly empty) return is a real
// answer to persist, including an empty plan that grants nothing.
func (p *OpenAIProvider) RefreshCatalog(ctx context.Context) []ModelInfo {
	switch p.id {
	case "openrouter":
		// Fetch all live models; mark the curated subset active by default.
		fetched := p.fetchDynamicModels(ctx)
		if len(fetched) == 0 {
			return nil
		}
		for i := range fetched {
			fetched[i].ActiveByDefault = openRouterActiveDefaults[fetched[i].ID]
		}
		p.storeCatalog(fetched)
		return fetched

	case "ollama":
		// The instance's own /v1/models reports only what has been pulled. The
		// cloud catalog adds every hosted model, and a signed-in instance
		// resolves those remotely without a pull — so the picker ends up showing
		// what is actually usable, not just what happens to be on disk.
		fetched := p.fetchDynamicModels(ctx)
		for i := range fetched {
			// Local: the user pulled these deliberately, so enable them.
			// A cloud endpoint returns a long list — curate instead.
			fetched[i].ActiveByDefault = !isCloudURL(p.baseURL)
		}

		var catalog []ModelInfo
		if ollamaCatalogEnabled() {
			c, err := FetchOllamaCloudCatalog(ctx, p.baseURL)
			if err != nil {
				// Undocumented endpoint — a failure here is routine, not
				// something to surface. The static fallbacks still apply.
				slog.Debug("ollama cloud catalog unavailable", "err", err)
			} else {
				catalog = c
			}
		}

		// With nothing pulled locally, every model would arrive disabled and
		// the user would land on an empty picker. Enable the cheapest few —
		// the catalog is sorted smallest-first — so the endpoint works out
		// of the box.
		if len(fetched) == 0 {
			for i := range catalog {
				if i >= ollamaCatalogDefaultActive {
					break
				}
				catalog[i].ActiveByDefault = true
			}
		}

		merged := mergeOllamaModels(fetched, catalog)
		if len(merged) == 0 {
			return nil
		}
		p.storeCatalog(merged)
		return merged

	case OGXProviderID:
		// The gateway's catalogue is the plan: it reports exactly what the
		// account can reach. Everything it lists is what the user paid for, so
		// all of it starts enabled, and there is no static fallback — an empty
		// or failed fetch means no models, not a guess at what might work.
		fetched := p.fetchDynamicModels(ctx)
		if fetched == nil {
			return nil // failed: keep the last known plan
		}
		for i := range fetched {
			fetched[i].ActiveByDefault = true
		}
		p.storeCatalog(fetched)
		return fetched

	default: // openai
		// When the base URL points to a non-OpenAI endpoint (e.g. DeepSeek,
		// Gemini, Groq — OpenAI-compatible providers configured via a custom
		// OPENAI_BASE_URL), fetch the model list so the user sees the actual
		// models that endpoint serves. The canonical api.openai.com endpoint
		// has a compiled-in catalogue and fetches nothing.
		if isCloudURL(p.baseURL) && p.baseURL != "https://api.openai.com/v1" {
			fetched := p.fetchDynamicModels(ctx)
			if len(fetched) > 0 {
				p.storeCatalog(fetched)
				return fetched
			}
		}
		return nil
	}
}

// storeCatalog installs list as the cached catalogue and re-resolves the
// default model from it. The list is not copied: it is freshly built by the
// caller and owned by the provider from here on.
func (p *OpenAIProvider) storeCatalog(list []ModelInfo) {
	p.modelsMu.Lock()
	p.cachedModels = list
	p.resolveDefaultModel(list)
	p.modelsMu.Unlock()
}

// isCloudOllama returns true if the base URL points to a remote/cloud endpoint
// (i.e. not localhost or a local network address).
func isCloudURL(baseURL string) bool {
	u := strings.ToLower(baseURL)
	if strings.Contains(u, "localhost") || strings.Contains(u, "127.0.0.1") || strings.Contains(u, "0.0.0.0") {
		return false
	}
	if strings.Contains(u, "://10.") || strings.Contains(u, "://192.168.") || strings.Contains(u, "://172.16.") || strings.Contains(u, "://172.17.") || strings.Contains(u, "://172.18.") || strings.Contains(u, "://172.19.") || strings.Contains(u, "://172.20.") || strings.Contains(u, "://172.21.") || strings.Contains(u, "://172.22.") || strings.Contains(u, "://172.23.") || strings.Contains(u, "://172.24.") || strings.Contains(u, "://172.25.") || strings.Contains(u, "://172.26.") || strings.Contains(u, "://172.27.") || strings.Contains(u, "://172.28.") || strings.Contains(u, "://172.29.") || strings.Contains(u, "://172.30.") || strings.Contains(u, "://172.31.") {
		return false
	}
	return true
}

// oaiModelsResponse is the response from GET /v1/models (OpenAI-compatible).
type oaiModelsResponse struct {
	Data []oaiModelEntry `json:"data"`
}

type oaiModelEntry struct {
	ID      string `json:"id"`
	Name    string `json:"name"` // populated by OpenRouter, empty for Ollama
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
	// ContextLength is populated by OpenRouter's /models (and any other
	// OpenAI-compatible endpoint that mirrors the field); Ollama leaves it out.
	// Parsed leniently below — some mirrors send it as a string — and 0 means
	// "endpoint did not report one"; the caller must not guess.
	ContextLength any `json:"context_length,omitempty"`
}

// oaiContextWindow coerces a models-list context_length into an int. OpenRouter
// sends a JSON number; some OpenAI-compatible mirrors send it as a string.
// Anything non-positive or unparsable is 0 — unknown, never guessed.
func oaiContextWindow(v any) int {
	switch n := v.(type) {
	case float64:
		if n > 0 && n <= float64(int(^uint(0)>>1)) {
			return int(n)
		}
	case string:
		s := strings.TrimSpace(n)
		if s != "" {
			if f, err := strconv.ParseFloat(s, 64); err == nil && f > 0 && f <= float64(int(^uint(0)>>1)) {
				return int(f)
			}
		}
	}
	return 0
}

// fetchDynamicModels fetches the model list from /v1/models for cloud providers.
// Returns nil if fetching fails (use static fallback). Returns an empty non-nil
// slice if the endpoint returns an empty list (cached to avoid re-fetching).
func (p *OpenAIProvider) fetchDynamicModels(ctx context.Context) []ModelInfo {
	url := strings.TrimRight(p.baseURL, "/") + "/models"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		slog.Warn("failed to create models request", "provider", p.id, "err", err)
		return nil
	}
	p.setChatHeaders(req)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		slog.Warn("failed to fetch models from endpoint", "provider", p.id, "url", url, "err", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		slog.Warn("models endpoint returned non-200", "provider", p.id, "status", resp.StatusCode, "body", string(body))
		return nil
	}

	var listResp oaiModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		slog.Warn("failed to decode models response", "provider", p.id, "err", err)
		return nil
	}

	var models []ModelInfo
	for _, m := range listResp.Data {
		name := m.Name
		if name == "" {
			name = m.ID
		}
		models = append(models, ModelInfo{
			ID:             m.ID,
			Name:           name,
			ProviderID:     p.id,
			SupportsImages: modelNameSuggestsVision(m.ID),
			Collection:     p.collection,
			// Only an endpoint-reported length is stored; 0 stays 0 so callers
			// keep treating the window as unknown rather than guessing.
			ContextWindow: oaiContextWindow(m.ContextLength),
		})
	}
	slog.Info("dynamically fetched models from endpoint", "provider", p.id, "count", len(models))
	// Ensure we return a non-nil (possibly empty) slice so the caller can cache it.
	if models == nil {
		models = []ModelInfo{}
	}
	return models
}

// openRouterActiveDefaults is the curated subset that starts enabled.
// All other live-fetched OpenRouter models are fetched but disabled until the user enables them.
var openRouterActiveDefaults = map[string]bool{
	"anthropic/claude-sonnet-4.6":       true,
	"anthropic/claude-opus-4.6":         true,
	"anthropic/claude-haiku-4.5":        true,
	"openai/gpt-4o":                     true,
	"openai/o4-mini":                    true,
	"google/gemini-2.5-pro":             true,
	"deepseek/deepseek-r1":              true,
	"meta-llama/llama-3.3-70b-instruct": true,
}

// openRouterStaticCatalog is the compiled-in fallback used when the endpoint
// cannot be reached (or has not been reached yet). Every entry starts enabled,
// since it is the curated list the user sees on a fresh install.
var openRouterStaticCatalog = []ModelInfo{
	{ID: "anthropic/claude-sonnet-4.6", Name: "Anthropic: Claude Sonnet 4.6", ProviderID: "openrouter", ActiveByDefault: true},
	{ID: "anthropic/claude-opus-4.6", Name: "Anthropic: Claude Opus 4.6", ProviderID: "openrouter", ActiveByDefault: true},
	{ID: "anthropic/claude-haiku-4.5", Name: "Anthropic: Claude Haiku 4.5", ProviderID: "openrouter", ActiveByDefault: true},
	{ID: "openai/gpt-4o", Name: "OpenAI: GPT-4o", ProviderID: "openrouter", ActiveByDefault: true},
	{ID: "openai/o4-mini", Name: "OpenAI: o4 Mini", ProviderID: "openrouter", ActiveByDefault: true},
	{ID: "google/gemini-2.5-pro", Name: "Google: Gemini 2.5 Pro", ProviderID: "openrouter", ActiveByDefault: true},
	{ID: "deepseek/deepseek-r1", Name: "DeepSeek: R1", ProviderID: "openrouter", ActiveByDefault: true},
	{ID: "meta-llama/llama-3.3-70b-instruct", Name: "Meta: Llama 3.3 70B Instruct", ProviderID: "openrouter", ActiveByDefault: false},
}

// ollamaLocalFallback is used when local Ollama is not running or has no models pulled.
// visionModelHints are substrings of model IDs from known multimodal families.
// Used to infer image support for dynamically-fetched models (OpenRouter/Ollama)
// that carry no capability metadata. Conservative by design — unknown models
// stay text-only.
var visionModelHints = []string{
	"gpt-4o", "gpt-4.1", "gpt-5", "o1", "o3", "o4",
	"claude", "gemini", "grok-vision", "pixtral",
	"llava", "bakllava", "moondream", "minicpm-v",
	"-vision", "vision-", "qwen2-vl", "qwen2.5-vl", "llama-3.2-11b", "llama-3.2-90b",
}

// modelNameSuggestsVision reports whether a model ID looks like a multimodal model.
func modelNameSuggestsVision(modelID string) bool {
	id := strings.ToLower(modelID)
	for _, hint := range visionModelHints {
		if strings.Contains(id, hint) {
			return true
		}
	}
	return false
}

var ollamaLocalFallback = []ModelInfo{
	{ID: "qwen3", Name: "Qwen3", ProviderID: "ollama", ActiveByDefault: true},
	{ID: "llama3.1", Name: "Llama 3.1", ProviderID: "ollama", ActiveByDefault: true},
	{ID: "deepseek-coder-v2", Name: "DeepSeek Coder V2", ProviderID: "ollama", ActiveByDefault: true},
	{ID: "mistral", Name: "Mistral", ProviderID: "ollama", ActiveByDefault: false},
	{ID: "codellama", Name: "Code Llama", ProviderID: "ollama", ActiveByDefault: false},
	{ID: "qwen3.5", Name: "Qwen3.5", ProviderID: "ollama", ActiveByDefault: false, ContextWindow: 262144},
	{ID: "qwen3-coder-next", Name: "Qwen3 Coder Next", ProviderID: "ollama", ActiveByDefault: false},
	{ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash", ProviderID: "ollama", ActiveByDefault: false, ContextWindow: 1048576},
}

// ollamaCloudFallback is used when the cloud Ollama endpoint is unreachable.
var ollamaCloudFallback = []ModelInfo{
	{ID: "qwen3-coder-next", Name: "Qwen3 Coder Next", ProviderID: "ollama", ActiveByDefault: true},
	{ID: "kimi-k2.6", Name: "Kimi K2.6", ProviderID: "ollama", ActiveByDefault: true, ContextWindow: 262144},
	{ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash", ProviderID: "ollama", ActiveByDefault: true, ContextWindow: 1048576},
	{ID: "glm-5.1", Name: "GLM-5.1", ProviderID: "ollama", ActiveByDefault: false, ContextWindow: 202752},
	{ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro", ProviderID: "ollama", ActiveByDefault: false, ContextWindow: 1048576},
	{ID: "mistral-large-3", Name: "Mistral Large 3", ProviderID: "ollama", ActiveByDefault: false},
}

// Models returns the provider's model catalogue. It is a PURE READ: it never
// touches the network. The catalogue is populated by RefreshCatalog (the
// background refresh) or SetCatalog (the persisted copy seeded at startup), and
// callers get whatever is currently known — the compiled-in fallback until the
// first refresh lands.
//
// This used to fetch synchronously behind a sync.Once, which made every read
// block on a 10s network call — the source of the UI freeze. Nothing on a read
// path may block on a fetch now.
func (p *OpenAIProvider) Models() []ModelInfo {
	p.modelsMu.Lock()
	cached := p.cachedModels
	p.modelsMu.Unlock()

	list := cached
	if len(list) == 0 {
		list = p.staticFallback()
	}

	// Copy before stamping Default. The compiled-in fallbacks are package-level
	// slices shared by every provider instance, so mutating one in place would
	// both race and leak one provider's default onto another; the cached list is
	// the provider's own but is re-stamped on every read, so it must not be
	// edited either.
	out := make([]ModelInfo, len(list))
	copy(out, list)

	def := p.defaultModel()
	for i := range out {
		out[i].Default = out[i].ID == def
	}
	return out
}

// staticFallback returns the compiled-in list for this provider, used before any
// catalogue has been fetched or seeded. It is the same catalogue RefreshCatalog
// leaves in place on a failed fetch, so a cold start and a failed refresh show
// the user the same thing. OGX deliberately has none — a planless gateway serves
// nothing, and a guess at what might work is worse than an empty list.
func (p *OpenAIProvider) staticFallback() []ModelInfo {
	switch p.id {
	case "openrouter":
		return openRouterStaticCatalog
	case "ollama":
		if isCloudURL(p.baseURL) {
			return ollamaCloudFallback
		}
		return ollamaLocalFallback
	case OGXProviderID:
		return nil
	}
	// openai: the canonical endpoint and a custom OpenAI-compatible one alike
	// fall back to the compiled-in catalogue until a fetch lands (a custom
	// endpoint's real list replaces it via RefreshCatalog). This mirrors the
	// pre-existing behavior of showing the OpenAI list rather than nothing.
	return openAICatalog()
}

// openAICatalog renders the compiled-in OpenAI model list.
func openAICatalog() []ModelInfo {
	list := make([]ModelInfo, 0, len(OpenAIModels))
	for _, m := range OpenAIModels {
		list = append(list, ModelInfo{
			ID:              m.ID,
			Name:            m.Name,
			ProviderID:      "openai",
			ActiveByDefault: m.ActiveByDefault,
			InputPricePerM:  m.InputPricePerM,
			OutputPricePerM: m.OutputPricePerM,
			SupportsImages:  m.SupportsImages,
			ContextWindow:   m.ContextWindow,
			MaxOutputTokens: m.MaxOutputTokens,
		})
	}
	return list
}

// collectionFromBaseURL infers a grouping label from an OpenAI-compatible base
// URL so dynamically-fetched models can be grouped in the UI. Returns "" when
// the URL is the canonical OpenAI endpoint (no grouping needed).
func collectionFromBaseURL(baseURL string) string {
	u := strings.ToLower(baseURL)
	switch {
	case strings.Contains(u, "generativelanguage.googleapis.com"):
		return "Gemini"
	case strings.Contains(u, "deepseek.com"):
		return "DeepSeek"
	case strings.Contains(u, "groq.com"):
		return "Groq"
	case strings.Contains(u, "openrouter.ai"):
		return "OpenRouter"
	case strings.Contains(u, "cerebras.ai"):
		return "Cerebras"
	case strings.Contains(u, "sambanova"):
		return "SambaNova"
	case strings.Contains(u, "models.inference.ai.azure.com"):
		return "GitHub Models"
	case strings.Contains(u, "integrate.api.nvidia.com"):
		return "NVIDIA"
	case strings.Contains(u, "together.xyz"):
		return "Together"
	case strings.Contains(u, "mistral.ai"):
		return "Mistral"
	}
	return ""
}

// Prompt caching on this code path is two unrelated problems, because one
// struct serves every OpenAI-compatible endpoint.
//
// OpenAI caches automatically and needs no breakpoints, but its cache lives
// behind a pool of machines and routes by prefix hash; prompt_cache_key is the
// documented way to keep one conversation landing on the node that already
// holds its prefix. Without it, hit rate sags exactly when the service is busy.
//
// OpenRouter is a passthrough. For its OpenAI and Gemini models caching is
// automatic and there is nothing to do — but for its Anthropic models it
// forwards cache_control and invents nothing, so a request with no breakpoints
// gets NO caching at all. That is the default configuration: OpenRouter's
// default model here is anthropic/claude-sonnet-4.6 and three Claude models are
// active by default, so the untouched setup was paying full price for roughly
// 7.8k tokens of tools plus system on every step of every turn.
//
// Ollama gets the key too, matched by provider id rather than URL: its traffic
// rides addresses that name no vendor — the local daemon on :11434, or a relay
// such as the multi-account router on :8090 — so the URL says nothing. Ollama's
// decoder ignores unknown fields (its OpenAI compatibility layer is a
// non-strict Go decoder, end-to-end through the router included), and a relay
// between ogcode and ollama.com can read the key as a ready-made session
// identity for cache-preserving account affinity, instead of inventing one.
//
// Everything else is gated on the endpoint rather than sent everywhere. This
// struct also serves Groq, DeepSeek, Cerebras, SambaNova and anything a user
// points a base URL at; an unknown top-level field or an unexpected
// content-part shape is a 400 on a strict server, and breaking a request to
// save tokens is a bad trade.
//
// OGX is matched by id and by its gateway hostname. The field is not about
// caching there at all: OG Lab's gateway records prompt_cache_key as the
// session identity it attributes token spend to, so omitting it would leave the
// plan's usage unattributed rather than uncached.

// sendsPromptCacheKey reports whether this endpoint understands
// prompt_cache_key. OpenRouter is included: it accepts the field for its
// OpenAI-backed models and ignores it elsewhere. Ollama is included by id (any
// base URL) and by its cloud hostname, for a custom slot pointed straight at
// ollama.com. OGX is included by id and by its gateway hostname, where the
// field is the session identity rather than a cache hint.
func (p *OpenAIProvider) sendsPromptCacheKey() bool {
	if p.id == "ollama" || p.id == OGXProviderID {
		return true
	}
	u := strings.ToLower(p.baseURL)
	return strings.Contains(u, "api.openai.com") ||
		strings.Contains(u, "openrouter.ai") ||
		strings.Contains(u, "ollama.com") ||
		strings.Contains(u, "ogx.ogcode.xyz")
}

// needsExplicitCacheBreakpoints reports whether this request must carry
// Anthropic-style cache_control markers to be cached at all: an Anthropic model
// reached through OpenRouter's passthrough.
func (p *OpenAIProvider) needsExplicitCacheBreakpoints(model string) bool {
	return p.isOpenRouter() && strings.HasPrefix(strings.ToLower(model), "anthropic/")
}

// cachedTextPart renders one string as a content part carrying a cache
// breakpoint, in the shape OpenRouter forwards to Anthropic.
func cachedTextPart(text string) map[string]any {
	return map[string]any{
		"type":          "text",
		"text":          text,
		"cache_control": map[string]any{"type": "ephemeral"},
	}
}

// systemMessageFor builds the system message, with a cache breakpoint when the
// endpoint needs one.
//
// The breakpoint goes at the end of entry [0] — the session-static base — and
// never later. Entries [1:] are the per-turn tail (viewport, index status, the
// date, the skill list, a compaction summary), so a breakpoint past them would
// be invalidated by the very content it was meant to protect. This mirrors what
// the Anthropic provider does with the same entries.
func systemMessageFor(system []string, breakpoints bool) (oaiMessage, bool) {
	if len(system) == 0 {
		return oaiMessage{}, false
	}
	if !breakpoints {
		return oaiMessage{Role: "system", Content: strings.Join(system, "\n\n")}, true
	}
	parts := []any{cachedTextPart(system[0])}
	if rest := strings.Join(system[1:], "\n\n"); rest != "" {
		parts = append(parts, map[string]any{"type": "text", "text": rest})
	}
	return oaiMessage{Role: "system", Content: parts}, true
}

// attachOAIMessageBreakpoint marks the conversation prefix so a turn's history
// is cached across its steps, not just the tools and system block.
//
// It walks back to the last user or assistant message carrying plain string
// content. A tool-result message is skipped deliberately: it is the last message
// on most steps of an agent loop, and its content maps to an Anthropic
// tool_result block whose part shape is not the one this marker belongs on.
// Stopping at the message before it still caches nearly all of the history and
// cannot produce a request the passthrough rejects.
func attachOAIMessageBreakpoint(messages []oaiMessage) {
	for i := len(messages) - 1; i >= 0; i-- {
		m := &messages[i]
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		if m.ToolCallID != "" {
			continue
		}
		text, ok := m.Content.(string)
		if !ok || text == "" {
			continue
		}
		m.Content = []any{cachedTextPart(text)}
		return
	}
}

func (p *OpenAIProvider) StreamChat(ctx context.Context, req StreamRequest) (<-chan StreamEvent, error) {
	model := req.Model
	if model == "" {
		model = p.defaultModel()
	}

	explicitBreakpoints := p.needsExplicitCacheBreakpoints(model)

	messages := make([]oaiMessage, 0, len(req.Messages)+len(req.System))
	if sysMsg, ok := systemMessageFor(req.System, explicitBreakpoints); ok {
		messages = append(messages, sysMsg)
	}
	// OpenAI-compatible APIs reject images inside a tool result. Buffer any
	// images attached to tool results and emit them as a follow-up user message
	// once all of the turn's consecutive tool messages have been appended.
	var pendingImages []MessageImage
	flushImages := func() {
		if len(pendingImages) == 0 {
			return
		}
		parts := []any{map[string]any{
			"type": "text",
			"text": "Rendered image(s) for the preceding tool result:",
		}}
		for _, img := range pendingImages {
			parts = append(parts, map[string]any{
				"type": "image_url",
				"image_url": map[string]any{
					"url": fmt.Sprintf("data:%s;base64,%s", img.MediaType, img.Data),
				},
			})
		}
		messages = append(messages, oaiMessage{Role: "user", Content: parts})
		pendingImages = nil
	}

	for _, m := range req.Messages {
		// A non-tool message ends the run of tool results; flush buffered images first.
		if m.ToolCallID == "" {
			flushImages()
		}
		msg := oaiMessage{Role: m.Role}
		if m.ToolCallID != "" {
			// Tool result message: role=tool, content=output, tool_call_id, name
			msg.ToolCallID = m.ToolCallID
			msg.Name = m.Name
			var content any
			if err := json.Unmarshal(m.Content, &content); err != nil {
				content = string(m.Content)
			}
			msg.Content = content
			if len(m.Images) > 0 {
				pendingImages = append(pendingImages, m.Images...)
			}
		} else if len(m.ToolCalls) > 0 {
			// Assistant message with tool calls
			msg.ToolCalls = m.ToolCalls
			if len(m.Content) > 0 {
				var content any
				if err := json.Unmarshal(m.Content, &content); err != nil {
					msg.Content = string(m.Content)
				} else {
					msg.Content = content
				}
			} else {
				// OpenAI requires content to be null (not omitted) when only tool calls
				msg.Content = nil
			}
		} else if len(m.Images) > 0 {
			// Plain message with image attachments (e.g. a capability probe or a
			// user-supplied image): content is an array of text + image_url parts.
			var text string
			json.Unmarshal(m.Content, &text)
			parts := []any{}
			if text != "" {
				parts = append(parts, map[string]any{"type": "text", "text": text})
			}
			for _, img := range m.Images {
				parts = append(parts, map[string]any{
					"type": "image_url",
					"image_url": map[string]any{
						"url": fmt.Sprintf("data:%s;base64,%s", img.MediaType, img.Data),
					},
				})
			}
			msg.Content = parts
		} else {
			var content any
			if err := json.Unmarshal(m.Content, &content); err != nil {
				content = string(m.Content)
			}
			msg.Content = content
		}
		messages = append(messages, msg)
	}
	// Flush any images from a trailing run of tool results (e.g. the current turn).
	flushImages()

	if explicitBreakpoints {
		attachOAIMessageBreakpoint(messages)
	}

	tools := make([]oaiTool, 0, len(req.Tools))
	toolNames := make(map[string]bool, len(req.Tools))
	for _, t := range req.Tools {
		tools = append(tools, oaiTool{
			Type: "function",
			Function: oaiFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
		toolNames[t.Name] = true
	}

	body := oaiRequest{
		Model:       model,
		Messages:    messages,
		Tools:       tools,
		Stream:      true,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
	if p.sendsPromptCacheKey() {
		body.PromptCacheKey = req.CacheKey
	}
	// stream_options.include_usage is supported by OpenAI, OpenRouter, and Ollama (v0.5+).
	// The final chunk will contain a usage object alongside an empty choices array.
	body.StreamOptions = &oaiStreamOptions{IncludeUsage: true}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := strings.TrimRight(p.baseURL, "/") + "/chat/completions"
	slog.Info("streaming chat request", "provider", p.id, "model", model, "url", url, "body_bytes", len(jsonBody))

	// Derive a cancellable request context so the idle watchdog in streamEvents
	// can abort a silently-stalled stream. On any early return (before the reader
	// goroutine takes ownership) the deferred guard cancels it so it never leaks.
	reqCtx, reqCancel := context.WithCancel(ctx)
	streamStarted := false
	defer func() {
		if !streamStarted {
			reqCancel()
		}
	}()

	httpReq, err := http.NewRequestWithContext(reqCtx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.ContentLength = int64(len(jsonBody))

	p.setChatHeaders(httpReq)

	client := streamHTTPClient

	var resp *http.Response
	retryDelays := []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second}
	for attempt := 0; ; attempt++ {
		var reqErr error
		resp, reqErr = client.Do(httpReq)
		if reqErr != nil {
			return nil, fmt.Errorf("send request: %w", reqErr)
		}
		shouldRetry := false
		if attempt < len(retryDelays) {
			if resp.StatusCode == http.StatusTooManyRequests {
				shouldRetry = true
			} else if resp.StatusCode == http.StatusBadRequest {
				// Retry transient "failed to read request body" errors from cloud providers.
				// Inspecting the body consumes it, so put it back unconditionally: the
				// generic error path below reads it to build the APIError, and a body
				// lost here surfaces as a bare "API error 400: " that hides the
				// provider's actual complaint — and reads as a context overflow.
				bodyBytes, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
				if strings.Contains(string(bodyBytes), "failed to read request body") {
					slog.Warn("transient request body error, retrying", "provider", p.id, "attempt", attempt+1)
					shouldRetry = true
				}
			}
		}
		if !shouldRetry {
			break
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		delay := retryDelays[attempt]
		slog.Warn("retrying request", "provider", p.id, "attempt", attempt+1, "status", resp.StatusCode, "delay", delay)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		httpReq, err = http.NewRequestWithContext(reqCtx, "POST", url, bytes.NewReader(jsonBody))
		if err != nil {
			return nil, fmt.Errorf("create retry request: %w", err)
		}
		httpReq.ContentLength = int64(len(jsonBody))
		p.setChatHeaders(httpReq)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		slog.Error("API error response", "provider", p.id, "status", resp.StatusCode, "body", string(body))
		return nil, NewAPIError(p.id, resp, string(body))
	}
	slog.Info("stream connected", "provider", p.id, "model", model)

	ch := make(chan StreamEvent, 256)
	streamStarted = true
	go p.streamEvents(resp.Body, ch, reqCancel, toolNames)
	return ch, nil
}

// isOpenRouter reports whether this provider's endpoint is OpenRouter. The gate
// is the base URL rather than the provider id because OpenRouter is reached
// under two different ids: the user's own "openrouter", and a generic "openai"
// provider pointed at openrouter.ai by custom base URL. Keying on id attributed
// only the first.
func (p *OpenAIProvider) isOpenRouter() bool {
	return strings.Contains(strings.ToLower(p.baseURL), "openrouter.ai")
}

// setChatHeaders applies the headers every /chat/completions request needs. The
// initial attempt and each retry build a fresh *http.Request, so this lives in
// one place to keep those copies from drifting apart.
func (p *OpenAIProvider) setChatHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		token := p.apiKey
		if !strings.Contains(token, " ") {
			token = "Bearer " + token
		}
		req.Header.Set("Authorization", token)
	}
	if p.isOpenRouter() {
		// OpenRouter credits an app for its traffic via these headers. Without
		// them the request is anonymous on the dashboard and the leaderboard.
		req.Header.Set("HTTP-Referer", "https://ogcode.xyz")
		req.Header.Set("X-Title", "ogcode")
	}
	// The gateway refuses an unasserted request once it has been given any
	// app secret, so a provider carrying an identity signs every call.
	signRequestAssertion(req, p.appID, p.appSecret)
}

// idleTimeout is how long this endpoint may go silent before its stream is
// treated as dead. This one Provider type fronts OpenAI, OpenRouter, Ollama and
// any custom OpenAI-compatible endpoint, and they do not behave alike: OpenAI
// streams tool-call arguments incrementally, while Ollama sends the finished
// call in a single frame and says nothing at all until the model stops writing.
// Holding both to the same budget aborts healthy long-file turns on the latter.
func (p *OpenAIProvider) idleTimeout() time.Duration {
	if p.id == "ollama" || isLocalEndpoint(p.baseURL) {
		return resolveIdleTimeout(streamIdleTimeoutBuffered)
	}
	return resolveIdleTimeout(streamIdleTimeout)
}

func (p *OpenAIProvider) streamEvents(body io.ReadCloser, ch chan<- StreamEvent, cancel context.CancelFunc, toolNames map[string]bool) {
	defer body.Close()
	defer close(ch)
	defer cancel()

	// Idle watchdog: if the stream goes silent for streamIdleTimeout, cancel the
	// request context so the blocked read unblocks and the stream ends instead of
	// hanging. It wraps the body so it resets on bytes read off the wire, not on
	// lines handed downstream — see idleWatchdog.
	idle := newIdleWatchdog(body, cancel, p.idleTimeout())
	defer idle.Stop()

	scanner := bufio.NewScanner(idle)
	scanner.Buffer(make([]byte, 0, 64*1024), streamMaxLineBytes)

	// Track active tool calls by index so we can match deltas. lastToolID is the
	// fallback when a provider sends argument deltas without a usable index.
	activeToolCalls := make(map[int]string) // index -> callID
	var lastToolID string

	// Weak/open models (e.g. many served via Ollama) sometimes emit a tool call
	// as plain text — a JSON object in the content — instead of via the structured
	// tool_calls field. That leaves the agent with nothing to execute, so the turn
	// stops mid-task. Accumulate the text content and, if the stream produced no
	// structured tool call, try to recover one from the text at the end.
	var contentBuf strings.Builder
	sawToolCall := false

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var evt oaiStreamResponse
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			continue
		}

		// Usage chunks (with stream_options.include_usage) typically arrive
		// in the final SSE chunk and may have zero choices. Surface them as
		// a separate event before the stream closes.
		if evt.Usage != nil {
			ch <- StreamEvent{Type: EventUsage, Usage: usageFromOAI(evt.Usage)}
		}

		if len(evt.Choices) == 0 {
			continue
		}

		choice := evt.Choices[0]
		delta := choice.Delta

		if delta == nil {
			// Still check finish_reason
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				ch <- StreamEvent{Type: EventFinish, FinishReason: choice.FinishReason}
			}
			continue
		}

		if delta.Content != "" {
			contentBuf.WriteString(delta.Content)
			ch <- StreamEvent{Type: EventTextDelta, Text: delta.Content}
		}

		if len(delta.ToolCalls) > 0 {
			sawToolCall = true
			for _, tc := range delta.ToolCalls {
				if tc.ID != "" {
					// New tool call starting
					activeToolCalls[tc.Index] = tc.ID
					lastToolID = tc.ID
					ch <- StreamEvent{
						Type:       EventToolCallStart,
						ToolCallID: tc.ID,
						ToolName:   tc.Function.Name,
						ToolInput:  []byte(tc.Function.Arguments),
					}
				} else if tc.Function.Arguments != "" {
					// Argument delta. Route by index to the call it belongs to, but
					// fall back to the most recently started call when the provider
					// omitted or mismatched the index (some non-conforming endpoints
					// send every delta at index 0). Never emit a delta with an empty
					// id: the agent matches deltas to a call by id, so an unroutable
					// one silently drops the bytes and truncates the arguments.
					callID := activeToolCalls[tc.Index]
					if callID == "" {
						callID = lastToolID
					}
					if callID == "" {
						continue
					}
					ch <- StreamEvent{
						Type:       EventToolCallDelta,
						ToolCallID: callID,
						ToolInput:  []byte(tc.Function.Arguments),
					}
				}
			}
		}

		if delta.ReasoningContent != "" {
			ch <- StreamEvent{Type: EventReasoning, Text: delta.ReasoningContent}
		}
		if delta.Reasoning != "" {
			ch <- StreamEvent{Type: EventReasoning, Text: delta.Reasoning}
		}

		// Check finish reason on the same chunk as the delta
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			ch <- StreamEvent{Type: EventFinish, FinishReason: choice.FinishReason}
		}
	}

	// The scan can stop for reasons other than end-of-stream: a read error, a
	// cancelled request, an over-long line (providers that send a whole tool call
	// in one chunk hit this on large writes). Report it and stop — returning
	// silently would close the channel with no finish event and no error, leaving
	// the agent loop to guess, and the recovery below would be working from a
	// buffer that was cut off mid-response.
	if err := scanner.Err(); err != nil {
		msg := describeStreamReadError(err, idle.Fired(), idle.Timeout())
		// Count it against the IPv6 path if that is what gave out, so a host
		// whose IPv6 keeps dropping established connections stops using it.
		noteIPv6Failure(err)
		slog.Warn("openai stream read failed", "provider", p.id, "err", err, "idleTimeout", idle.Fired())
		ch <- StreamEvent{Type: EventError, Error: msg, Err: err}
		return
	}

	// Fallback: the model produced no structured tool call. If its text content
	// contains a tool call it emitted as JSON prose (naming a tool that was
	// actually offered), synthesise a real tool call so the agent executes it and
	// the task continues instead of stalling. Guarded by the offered-tool-name set
	// to avoid mistaking an illustrative JSON snippet for a call.
	if !sawToolCall {
		if name, args, ok := parseTextToolCall(contentBuf.String(), toolNames); ok {
			id := fmt.Sprintf("call_txt_%d", textToolCallSeq.Add(1))
			slog.Info("recovered tool call emitted as text", "provider", p.id, "tool", name, "callID", id)
			ch <- StreamEvent{Type: EventToolCallStart, ToolCallID: id, ToolName: name, ToolInput: args}
			ch <- StreamEvent{Type: EventToolCallEnd, ToolCallID: id, ToolName: name}
		}
	}
}

// textToolCallSeq generates process-unique IDs for tool calls recovered from
// text, so each synthesised call has a distinct tool_call id like a real one.
var textToolCallSeq atomic.Uint64

// parseTextToolCall scans free-form model output for a tool call the model wrote
// as text — a JSON object with a "name" (or "tool") field plus "arguments"/
// "parameters" — instead of using the structured tool_calls API. It returns the
// tool name and its arguments as a JSON object. To avoid false positives it only
// accepts a name present in offered (the set of tools actually advertised this
// request); ok is false when nothing qualifies.
func parseTextToolCall(text string, offered map[string]bool) (name string, args json.RawMessage, ok bool) {
	for _, obj := range candidateJSONObjects(text) {
		var probe struct {
			Name       string          `json:"name"`
			Tool       string          `json:"tool"`
			Arguments  json.RawMessage `json:"arguments"`
			Parameters json.RawMessage `json:"parameters"`
		}
		if json.Unmarshal(obj, &probe) != nil {
			continue
		}
		n := probe.Name
		if n == "" {
			n = probe.Tool
		}
		if n == "" || !offered[n] {
			continue
		}
		raw := probe.Arguments
		if len(raw) == 0 {
			raw = probe.Parameters
		}
		return n, normalizeToolArgs(raw), true
	}
	return "", nil, false
}

// candidateJSONObjects returns every top-level balanced {...} region in text, in
// order. It tracks string literals so braces inside JSON strings are ignored.
func candidateJSONObjects(text string) []json.RawMessage {
	var out []json.RawMessage
	depth, start := 0, -1
	inStr, esc := false, false
	for i := 0; i < len(text); i++ {
		c := text[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth > 0 {
				depth--
				if depth == 0 && start >= 0 {
					out = append(out, json.RawMessage(text[start:i+1]))
					start = -1
				}
			}
		}
	}
	return out
}

// normalizeToolArgs coerces a tool call's arguments into a JSON object. The value
// may be an object, a JSON string that itself contains an object (double-encoded,
// as some models emit), or absent — anything that isn't a usable object becomes {}.
func normalizeToolArgs(raw json.RawMessage) json.RawMessage {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	if raw[0] == '{' {
		return raw
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			s = strings.TrimSpace(s)
			if len(s) > 0 && s[0] == '{' && json.Valid([]byte(s)) {
				return json.RawMessage(s)
			}
		}
	}
	return json.RawMessage("{}")
}

// OpenAI API types

type oaiRequest struct {
	Model         string            `json:"model"`
	Messages      []oaiMessage      `json:"messages"`
	Tools         []oaiTool         `json:"tools,omitempty"`
	Stream        bool              `json:"stream"`
	StreamOptions *oaiStreamOptions `json:"stream_options,omitempty"`
	Temperature   float64           `json:"temperature,omitempty"`
	MaxTokens     int               `json:"max_tokens,omitempty"`
	// PromptCacheKey routes a conversation's requests to the cache node that
	// already holds its prefix. Sent only where it is known to be understood —
	// see sendsPromptCacheKey.
	PromptCacheKey string `json:"prompt_cache_key,omitempty"`
}

type oaiStreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

type oaiMessage struct {
	Role       string          `json:"role"`
	Content    any             `json:"content,omitempty"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`
}

type oaiTool struct {
	Type     string      `json:"type"`
	Function oaiFunction `json:"function"`
}

type oaiFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type oaiStreamResponse struct {
	ID      string      `json:"id,omitempty"`
	Choices []oaiChoice `json:"choices"`
	Usage   *oaiUsage   `json:"usage,omitempty"`
}

type oaiUsage struct {
	PromptTokens            int                        `json:"prompt_tokens"`
	CompletionTokens        int                        `json:"completion_tokens"`
	TotalTokens             int                        `json:"total_tokens"`
	PromptCacheHitTokens    int                        `json:"prompt_cache_hit_tokens,omitempty"`
	PromptCacheMissTokens   int                        `json:"prompt_cache_miss_tokens,omitempty"`
	PromptTokensDetails     *oaiPromptTokensDetails    `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *oaiCompletionTokenDetails `json:"completion_tokens_details,omitempty"`
}

type oaiPromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type oaiCompletionTokenDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// usageFromOAI converts OpenAI-shaped usage into the provider-neutral TokenUsage.
//
// The one thing it exists to get right: OpenAI nests cached_tokens INSIDE
// prompt_tokens, whereas Anthropic reports input_tokens exclusive of its cache
// fields. Callers sum InputTokens + CacheReadTokens + CacheWriteTokens, so the
// cached portion is subtracted out here and InputTokens means the same thing on
// every provider — input the model actually re-read.
//
// Reporting the raw prompt_tokens instead makes a well-cached step look up to
// twice its true size, which floors the output budget (truncating long tool
// arguments mid-write) and trips proactive compaction at half its threshold.
func usageFromOAI(u *oaiUsage) *TokenUsage {
	if u == nil {
		return nil
	}
	usage := &TokenUsage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
	}
	// The cache hit count is reported two ways in the wild: OpenAI nests
	// cached_tokens inside prompt_tokens_details, while DeepSeek's native API
	// puts a top-level prompt_cache_hit_tokens beside it. Prefer the nested
	// field when it carries a count and fall back to the top-level one.
	cached := u.PromptCacheHitTokens
	if u.PromptTokensDetails != nil && u.PromptTokensDetails.CachedTokens > 0 {
		cached = u.PromptTokensDetails.CachedTokens
	}
	if cached > 0 {
		usage.CacheReadTokens = cached
		usage.InputTokens -= usage.CacheReadTokens
		// A server that reports the cached count exclusive of prompt_tokens, or
		// simply reports the two inconsistently, must not drive input negative.
		if usage.InputTokens < 0 {
			usage.InputTokens = 0
		}
	}
	if u.CompletionTokensDetails != nil {
		usage.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
	}
	return usage
}

type oaiChoice struct {
	Index        int       `json:"index"`
	Delta        *oaiDelta `json:"delta,omitempty"`
	FinishReason *string   `json:"finish_reason,omitempty"`
}

type oaiDelta struct {
	Role             string             `json:"role,omitempty"`
	Content          string             `json:"content,omitempty"`
	ToolCalls        []oaiToolCallDelta `json:"tool_calls,omitempty"`
	ReasoningContent string             `json:"reasoning_content,omitempty"`
	Reasoning        string             `json:"reasoning,omitempty"`
}

type oaiToolCallDelta struct {
	Index    int              `json:"index"`
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function oaiFunctionDelta `json:"function"`
}

type oaiFunctionDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}
