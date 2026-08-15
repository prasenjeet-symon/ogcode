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
	freePool   bool   // provisioned from the community free-tier key pool

	// cachedModels caches models fetched from /v1/models for Ollama cloud.
	// Nil means not yet fetched; empty slice means fetched but none found.
	cachedModels []ModelInfo
	modelsOnce   sync.Once
	modelsMu     sync.Mutex
}

func (p *OpenAIProvider) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	model := p.model
	// Prefer an embedding model if the current model is a chat model.
	// For OpenAI, users typically use text-embedding-3-small/large.
	// Allow override via OGCODE_EMBED_MODEL env var.
	if embedModel := os.Getenv("OGCODE_EMBED_MODEL"); embedModel != "" {
		model = embedModel
	} else if !isEmbeddingModel(model) {
		model = "text-embedding-3-small"
	}

	// OpenAI embeddings endpoint
	url := strings.TrimRight(p.baseURL, "/") + "/embeddings"

	type embedRequest struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}
	type embedResponse struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}

	body, err := json.Marshal(embedRequest{Model: model, Input: inputs})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create embed request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		token := p.apiKey
		if !strings.Contains(token, " ") {
			token = "Bearer " + token
		}
		httpReq.Header.Set("Authorization", token)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send embed request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s embed API error %d: %s", p.id, resp.StatusCode, string(body))
	}

	var out embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}

	vecs := make([][]float32, len(inputs))
	for _, d := range out.Data {
		vecs[d.Index] = d.Embedding
	}
	return vecs, nil
}

func (p *OpenAIProvider) EmbedModel() string {
	if embedModel := os.Getenv("OGCODE_EMBED_MODEL"); embedModel != "" {
		return embedModel
	}
	if isEmbeddingModel(p.model) {
		return p.model
	}
	return "text-embedding-3-small"
}

func isEmbeddingModel(model string) bool {
	m := strings.ToLower(model)
	return strings.Contains(m, "embed")
}

// NewEmbedProvider creates an OpenAIProvider configured for embedding.
// providerID must be "openai", "openrouter", or "ollama".
// If apiKey is non-empty it overrides the env var key.
// If model is non-empty it is stored as the provider model (used for embedding).
// Deprecated: Use NewEmbedProviderWithConfig for full control over baseURL.
func NewEmbedProvider(providerID, apiKey, model string) (*OpenAIProvider, error) {
	return NewEmbedProviderWithConfig(providerID, apiKey, model, "")
}

// NewEmbedProviderWithConfig creates an OpenAIProvider configured for embedding with
// optional apiKey, model, and baseURL overrides. Env-var values are used as the
// base; non-empty parameters override them.
func NewEmbedProviderWithConfig(providerID, apiKey, model, baseURL string) (*OpenAIProvider, error) {
	var p *OpenAIProvider
	switch providerID {
	case "openai":
		p = NewOpenAIProvider()
	case "openrouter":
		p = NewOpenRouterProvider()
	case "ollama":
		p = NewOllamaProvider()
	default:
		return nil, fmt.Errorf("unknown embed provider %q; must be openai, openrouter, or ollama", providerID)
	}
	if apiKey != "" {
		p.apiKey = apiKey
	}
	if model != "" {
		p.model = model
	}
	if baseURL != "" {
		p.baseURL = baseURL
	}
	return p, nil
}

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
	if model == "" {
		if isCloudURL(baseURL) {
			model = "qwen3-coder-next"
		} else {
			model = "qwen3"
		}
	}
	return &OpenAIProvider{
		id:      "ollama",
		apiKey:  apiKey,
		model:   model,
		baseURL: baseURL,
	}
}

func (p *OpenAIProvider) ID() string { return p.id }

// BaseURL returns the API base URL the provider is configured to use. Exposed
// so callers (e.g. the free-pool registration) can compare endpoints without
// reaching into the unexported field directly.
func (p *OpenAIProvider) BaseURL() string { return p.baseURL }

// RefreshModels clears the cached model list so the next call to Models()
// will re-fetch from the endpoint (for cloud providers).
// Not safe to call concurrently with Models().
func (p *OpenAIProvider) RefreshModels() {
	p.modelsMu.Lock()
	p.cachedModels = nil
	p.modelsOnce = sync.Once{}
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
	Name    string `json:"name"`    // populated by OpenRouter, empty for Ollama
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
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
	req.Header.Set("Accept", "application/json")
	if p.apiKey != "" {
		token := p.apiKey
		if !strings.Contains(token, " ") {
			token = "Bearer " + token
		}
		req.Header.Set("Authorization", token)
	}

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
	"anthropic/claude-sonnet-4.6":      true,
	"anthropic/claude-opus-4.6":        true,
	"anthropic/claude-haiku-4.5":       true,
	"openai/gpt-4o":                    true,
	"openai/o4-mini":                   true,
	"google/gemini-2.5-pro":            true,
	"deepseek/deepseek-r1":             true,
	"meta-llama/llama-3.3-70b-instruct": true,
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
	{ID: "qwen3.5", Name: "Qwen3.5", ProviderID: "ollama", ActiveByDefault: false},
	{ID: "qwen3-coder-next", Name: "Qwen3 Coder Next", ProviderID: "ollama", ActiveByDefault: false},
	{ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash", ProviderID: "ollama", ActiveByDefault: false},
}

// ollamaCloudFallback is used when the cloud Ollama endpoint is unreachable.
var ollamaCloudFallback = []ModelInfo{
	{ID: "qwen3-coder-next", Name: "Qwen3 Coder Next", ProviderID: "ollama", ActiveByDefault: true},
	{ID: "kimi-k2.6", Name: "Kimi K2.6", ProviderID: "ollama", ActiveByDefault: true},
	{ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash", ProviderID: "ollama", ActiveByDefault: true},
	{ID: "glm-5.1", Name: "GLM-5.1", ProviderID: "ollama", ActiveByDefault: false},
	{ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro", ProviderID: "ollama", ActiveByDefault: false},
	{ID: "mistral-large-3", Name: "Mistral Large 3", ProviderID: "ollama", ActiveByDefault: false},
}

func (p *OpenAIProvider) Models() []ModelInfo {
	var list []ModelInfo

	switch p.id {
	case "openrouter":
		// Fetch all live models once; mark curated subset as active by default.
		p.modelsOnce.Do(func() {
			fetched := p.fetchDynamicModels(context.Background())
			p.modelsMu.Lock()
			if len(fetched) > 0 {
				for i := range fetched {
					fetched[i].ActiveByDefault = openRouterActiveDefaults[fetched[i].ID]
				}
				p.cachedModels = fetched
			} else {
				// Fallback: static curated list, all active
				p.cachedModels = []ModelInfo{
					{ID: "anthropic/claude-sonnet-4.6", Name: "Anthropic: Claude Sonnet 4.6", ProviderID: "openrouter", ActiveByDefault: true},
					{ID: "anthropic/claude-opus-4.6", Name: "Anthropic: Claude Opus 4.6", ProviderID: "openrouter", ActiveByDefault: true},
					{ID: "anthropic/claude-haiku-4.5", Name: "Anthropic: Claude Haiku 4.5", ProviderID: "openrouter", ActiveByDefault: true},
					{ID: "openai/gpt-4o", Name: "OpenAI: GPT-4o", ProviderID: "openrouter", ActiveByDefault: true},
					{ID: "openai/o4-mini", Name: "OpenAI: o4 Mini", ProviderID: "openrouter", ActiveByDefault: true},
					{ID: "google/gemini-2.5-pro", Name: "Google: Gemini 2.5 Pro", ProviderID: "openrouter", ActiveByDefault: true},
					{ID: "deepseek/deepseek-r1", Name: "DeepSeek: R1", ProviderID: "openrouter", ActiveByDefault: true},
					{ID: "meta-llama/llama-3.3-70b-instruct", Name: "Meta: Llama 3.3 70B Instruct", ProviderID: "openrouter", ActiveByDefault: false},
				}
			}
			p.modelsMu.Unlock()
		})
		p.modelsMu.Lock()
		list = p.cachedModels
		p.modelsMu.Unlock()

	case "ollama":
		// Always fetch live from /v1/models (works for both local and cloud endpoints).
		// Local: reflects what the user has actually pulled; all enabled.
		// Cloud: mark curated subset active; rest disabled.
		p.modelsOnce.Do(func() {
			fetched := p.fetchDynamicModels(context.Background())
			p.modelsMu.Lock()
			if len(fetched) > 0 {
				for i := range fetched {
					if isCloudURL(p.baseURL) {
						// For cloud Ollama keep only a few active by default
						fetched[i].ActiveByDefault = false
					} else {
						// Local: user explicitly pulled these — all active
						fetched[i].ActiveByDefault = true
					}
				}
				p.cachedModels = fetched
			} else if isCloudURL(p.baseURL) {
				p.cachedModels = ollamaCloudFallback
			} else {
				p.cachedModels = ollamaLocalFallback
			}
			p.modelsMu.Unlock()
		})
		p.modelsMu.Lock()
		list = p.cachedModels
		p.modelsMu.Unlock()

	default: // openai
		// When the base URL points to a non-OpenAI endpoint (e.g. DeepSeek,
		// Gemini, Groq — OpenAI-compatible providers configured via a custom
		// OPENAI_BASE_URL), fetch the model list dynamically so the user sees
		// the actual models that endpoint serves. Fall back to the static
		// OpenAI catalog for the canonical api.openai.com endpoint.
		if isCloudURL(p.baseURL) && p.baseURL != "https://api.openai.com/v1" {
			p.modelsOnce.Do(func() {
				fetched := p.fetchDynamicModels(context.Background())
				p.modelsMu.Lock()
				if len(fetched) > 0 {
					p.cachedModels = fetched
				} else {
					p.cachedModels = nil // fallback to static catalog below
				}
				p.modelsMu.Unlock()
			})
			p.modelsMu.Lock()
			cached := p.cachedModels
			p.modelsMu.Unlock()
			if len(cached) > 0 {
				list = cached
				break
			}
		}
		list = make([]ModelInfo, 0, len(OpenAIModels))
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
			})
		}
	}

	// Community free-pool providers: restrict/curate the fetched list so a shared
	// public key is safe (free models only for OpenRouter) and every model is
	// enabled by default — a new user lands ready to chat, not on an empty picker.
	if p.freePool {
		list = curateFreePoolModels(list, p.baseURL)
	}
	for i := range list {
		if list[i].ID == p.model {
			list[i].Default = true
		}
	}
	return list
}

// curateFreePoolModels tailors a free-tier community-pool provider's
// dynamically-fetched model list. For OpenRouter the shared pool key is public
// and can reach paid models, so the list is restricted to the free (":free")
// variants — this honours the "use OpenRouter's free models" intent and keeps a
// public key from being used to drain credits on paid models through the app.
// Every surviving model is marked ActiveByDefault so a brand-new user lands with
// usable free models already enabled instead of an empty, all-disabled picker.
func curateFreePoolModels(fetched []ModelInfo, baseURL string) []ModelInfo {
	openRouter := strings.Contains(strings.ToLower(baseURL), "openrouter.ai")
	out := make([]ModelInfo, 0, len(fetched))
	for _, m := range fetched {
		if openRouter && !strings.HasSuffix(m.ID, ":free") {
			continue
		}
		m.ActiveByDefault = true
		out = append(out, m)
	}
	// If the free-only filter removed everything (e.g. OpenRouter renamed its
	// free tier), don't leave the provider empty — enable the raw list instead.
	if len(out) == 0 {
		out = make([]ModelInfo, 0, len(fetched))
		for _, m := range fetched {
			m.ActiveByDefault = true
			out = append(out, m)
		}
	}
	return out
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

// CollectionFromBaseURL is the exported form of collectionFromBaseURL for use
// outside the provider package (e.g. server-side provider registration).
func CollectionFromBaseURL(baseURL string) string {
	return collectionFromBaseURL(baseURL)
}

func (p *OpenAIProvider) StreamChat(ctx context.Context, req StreamRequest) (<-chan StreamEvent, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}

	messages := make([]oaiMessage, 0, len(req.Messages)+len(req.System))
	if len(req.System) > 0 {
		messages = append(messages, oaiMessage{
			Role:    "system",
			Content: strings.Join(req.System, "\n\n"),
		})
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

	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		token := p.apiKey
		if !strings.Contains(token, " ") {
			token = "Bearer " + token
		}
		httpReq.Header.Set("Authorization", token)
	}
	if p.id == "openrouter" {
		httpReq.Header.Set("HTTP-Referer", "https://ogcode.xyz")
		httpReq.Header.Set("X-Title", "ogcode")
	}

	client := &http.Client{Timeout: 600 * time.Second}

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
				bodyBytes, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if strings.Contains(string(bodyBytes), "failed to read request body") {
					slog.Warn("transient request body error, retrying", "provider", p.id, "attempt", attempt+1)
					shouldRetry = true
					// Reconstruct resp.Body so the generic error path can read it if all retries fail.
					resp = &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(bytes.NewReader(bodyBytes))}
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
		httpReq.Header.Set("Content-Type", "application/json")
		if p.apiKey != "" {
			token := p.apiKey
			if !strings.Contains(token, " ") {
				token = "Bearer " + token
			}
			httpReq.Header.Set("Authorization", token)
		}
		if p.id == "openrouter" {
			httpReq.Header.Set("HTTP-Referer", "https://ogcode.xyz")
			httpReq.Header.Set("X-Title", "ogcode")
		}
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

func (p *OpenAIProvider) streamEvents(body io.ReadCloser, ch chan<- StreamEvent, cancel context.CancelFunc, toolNames map[string]bool) {
	defer body.Close()
	defer close(ch)
	defer cancel()

	// Idle watchdog: if the stream goes silent for streamIdleTimeout, cancel the
	// request context so the blocked read unblocks and the stream ends, instead
	// of hanging until the 600s HTTP client timeout. Reset on every line received.
	idle := time.AfterFunc(streamIdleTimeout, cancel)
	defer idle.Stop()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// Track active tool calls by index so we can match deltas
	activeToolCalls := make(map[int]string) // index -> callID

	// Weak/open models (e.g. many served via Ollama) sometimes emit a tool call
	// as plain text — a JSON object in the content — instead of via the structured
	// tool_calls field. That leaves the agent with nothing to execute, so the turn
	// stops mid-task. Accumulate the text content and, if the stream produced no
	// structured tool call, try to recover one from the text at the end.
	var contentBuf strings.Builder
	sawToolCall := false

	for scanner.Scan() {
		idle.Reset(streamIdleTimeout)
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
			usage := &TokenUsage{
				InputTokens:  evt.Usage.PromptTokens,
				OutputTokens: evt.Usage.CompletionTokens,
			}
			if evt.Usage.PromptTokensDetails != nil {
				usage.CacheReadTokens = evt.Usage.PromptTokensDetails.CachedTokens
			}
			if evt.Usage.CompletionTokensDetails != nil {
				usage.ReasoningTokens = evt.Usage.CompletionTokensDetails.ReasoningTokens
			}
			ch <- StreamEvent{Type: EventUsage, Usage: usage}
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
					ch <- StreamEvent{
						Type:       EventToolCallStart,
						ToolCallID: tc.ID,
						ToolName:   tc.Function.Name,
						ToolInput:  []byte(tc.Function.Arguments),
					}
				} else if tc.Function.Arguments != "" {
					// Argument delta — use the tracked callID
					callID := activeToolCalls[tc.Index]
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
	Model         string             `json:"model"`
	Messages      []oaiMessage       `json:"messages"`
	Tools         []oaiTool          `json:"tools,omitempty"`
	Stream        bool               `json:"stream"`
	StreamOptions *oaiStreamOptions  `json:"stream_options,omitempty"`
	Temperature   float64            `json:"temperature,omitempty"`
	MaxTokens     int                `json:"max_tokens,omitempty"`
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
	PromptTokens            int                       `json:"prompt_tokens"`
	CompletionTokens        int                       `json:"completion_tokens"`
	TotalTokens             int                       `json:"total_tokens"`
	PromptTokensDetails     *oaiPromptTokensDetails   `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *oaiCompletionTokenDetails `json:"completion_tokens_details,omitempty"`
}

type oaiPromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type oaiCompletionTokenDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

type oaiChoice struct {
	Index        int           `json:"index"`
	Delta        *oaiDelta     `json:"delta,omitempty"`
	FinishReason *string       `json:"finish_reason,omitempty"`
}

type oaiDelta struct {
	Role             string           `json:"role,omitempty"`
	Content          string           `json:"content,omitempty"`
	ToolCalls        []oaiToolCallDelta `json:"tool_calls,omitempty"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
	Reasoning        string           `json:"reasoning,omitempty"`
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