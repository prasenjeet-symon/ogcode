package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

type StreamEventType string

const (
	EventTextDelta      StreamEventType = "text-delta"
	EventToolCallStart  StreamEventType = "tool-call-start"
	EventToolCallDelta  StreamEventType = "tool-call-delta"
	EventToolCallEnd    StreamEventType = "tool-call-end"
	EventReasoning      StreamEventType = "reasoning"
	EventFinish         StreamEventType = "finish"
	EventUsage          StreamEventType = "usage"
	EventError          StreamEventType = "error"
)

// TokenUsage carries per-message token accounting from a provider.
// Fields are non-zero where the provider reports them.
type TokenUsage struct {
	InputTokens      int `json:"inputTokens,omitempty"`
	OutputTokens     int `json:"outputTokens,omitempty"`
	ReasoningTokens  int `json:"reasoningTokens,omitempty"`
	CacheReadTokens  int `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens int `json:"cacheWriteTokens,omitempty"`
}

type StreamEvent struct {
	Type         StreamEventType  `json:"type"`
	Text         string           `json:"text,omitempty"`
	ToolCallID   string           `json:"toolCallId,omitempty"`
	ToolName     string           `json:"toolName,omitempty"`
	ToolInput    json.RawMessage  `json:"toolInput,omitempty"`
	FinishReason *string          `json:"finishReason,omitempty"`
	Usage        *TokenUsage      `json:"usage,omitempty"`
	Error        string           `json:"error,omitempty"`
}

type ContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// MessageImage is an image attached to a message, carried provider-neutrally.
// Data is base64-encoded image bytes; MediaType is e.g. "image/jpeg".
type MessageImage struct {
	MediaType string `json:"mediaType"`
	Data      string `json:"data"`
}

type ModelMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`
	// Images carries image attachments for a tool-result message. Providers
	// render these per their API: Anthropic embeds them in the tool_result
	// content block; OpenAI-family inject a follow-up user message.
	Images []MessageImage `json:"images,omitempty"`
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type StreamRequest struct {
	Model       string           `json:"model"`
	System      []string        `json:"system"`
	Messages    []ModelMessage  `json:"messages"`
	Tools       []ToolDefinition `json:"tools"`
	Temperature float64          `json:"temperature,omitempty"`
	MaxTokens   int              `json:"maxTokens,omitempty"`
	Abort       context.Context  `json:"-"`
}

type ModelInfo struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	ProviderID      string  `json:"providerId"`
	Default         bool    `json:"default"`
	ActiveByDefault bool    `json:"activeByDefault"`
	InputPricePerM  float64 `json:"inputPricePerM"`
	OutputPricePerM float64 `json:"outputPricePerM"`
	SupportsImages  bool    `json:"supportsImages"`
	// Collection is an optional grouping label for dynamically-fetched models
	// from OpenAI-compatible providers (e.g. "DeepSeek", "Gemini") so the UI can
	// group them instead of collapsing everything under the OpenAI provider id.
	Collection string `json:"collection,omitempty"`
}

type Provider interface {
	ID() string
	Models() []ModelInfo
	StreamChat(ctx context.Context, req StreamRequest) (<-chan StreamEvent, error)
}

// Embedder is an optional interface that providers can implement to support
// text embeddings (used for agentic memory semantic recall).
type Embedder interface {
	// Embed returns embedding vectors for the given input strings.
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
	EmbedModel() string
}

// ModelRefresher is an optional interface that providers can implement
// to support dynamic model list refreshing.
type ModelRefresher interface {
	RefreshModels()
}

type Registry struct {
	mu           sync.RWMutex // protects providers
	providers    map[string]Provider
	customModels map[string]string // modelID -> providerID
	customMu     sync.RWMutex
}

func NewRegistry() *Registry {
	return &Registry{
		providers:    make(map[string]Provider),
		customModels: make(map[string]string),
	}
}

func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	r.providers[p.ID()] = p
	r.mu.Unlock()
}

func (r *Registry) Get(id string) Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.providers[id]
}

func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var ids []string
	for id := range r.providers {
		ids = append(ids, id)
	}
	return ids
}

// snapshot returns the registered providers as a slice under a read lock, so
// callers can iterate and call Models() (which may hit the network) without
// holding the registry lock or racing with ReplaceProviders.
func (r *Registry) snapshot() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ps := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		ps = append(ps, p)
	}
	return ps
}

func (r *Registry) ListModels() []ModelInfo {
	var models []ModelInfo
	for _, p := range r.snapshot() {
		models = append(models, p.Models()...)
	}
	return models
}

// ModelSupportsImages reports whether the given model accepts image input.
// Unknown models default to false.
func (r *Registry) ModelSupportsImages(modelID string) bool {
	if modelID == "" {
		return false
	}
	for _, p := range r.snapshot() {
		for _, m := range p.Models() {
			if m.ID == modelID {
				return m.SupportsImages
			}
		}
	}
	return false
}

func (r *Registry) RegisterCustomModel(modelID, providerID string) {
	r.customMu.Lock()
	r.customModels[modelID] = providerID
	r.customMu.Unlock()
}

func (r *Registry) UnregisterCustomModel(modelID string) {
	r.customMu.Lock()
	delete(r.customModels, modelID)
	r.customMu.Unlock()
}

func (r *Registry) ResolveProvider(modelID string) Provider {
	// Check custom model routing first
	r.customMu.RLock()
	providerID, customOk := r.customModels[modelID]
	r.customMu.RUnlock()
	if customOk {
		if p := r.Get(providerID); p != nil {
			return p
		}
	}
	ps := r.snapshot()
	// Then check built-in models
	for _, p := range ps {
		for _, m := range p.Models() {
			if m.ID == modelID {
				return p
			}
		}
	}
	// Fallback to first provider
	for _, p := range ps {
		return p
	}
	return nil
}

// NewProviderWithConfig creates a Provider with explicit credentials, used when
// credentials come from the DB rather than environment variables.
// providerID must be "anthropic", "openai", "openrouter", or "ollama".
// Env-var values are used as the base; apiKey and baseURL override them when non-empty.
func NewProviderWithConfig(providerID, apiKey, baseURL string) (Provider, error) {
	switch providerID {
	case "anthropic":
		p := NewAnthropicProvider()
		if apiKey != "" {
			p.apiKey = apiKey
		}
		if baseURL != "" {
			p.baseURL = baseURL
		}
		return p, nil
	case "openai":
		p := NewOpenAIProvider()
		if apiKey != "" {
			p.apiKey = apiKey
		}
		if baseURL != "" {
			p.baseURL = baseURL
			p.collection = collectionFromBaseURL(baseURL)
		}
		return p, nil
	case "openrouter":
		p := NewOpenRouterProvider()
		if apiKey != "" {
			p.apiKey = apiKey
		}
		return p, nil
	case "ollama":
		p := NewOllamaProvider()
		if apiKey != "" {
			p.apiKey = apiKey
		}
		if baseURL != "" {
			p.baseURL = baseURL
		}
		return p, nil
	default:
		return nil, fmt.Errorf("unknown provider %q; must be anthropic, openai, openrouter, or ollama", providerID)
	}
}

// LocalEmbedderProvider is the provider ID for the inbuilt, no-dependency
// embedder that runs a sentence-embedding model in-process. It needs no API
// key and no network access. It is the only embedder ogcode supports —
// agentic memory embeddings are always produced locally.
const LocalEmbedderProvider = "local"

// NewEmbedder returns the inbuilt local embedder. ogcode no longer supports
// third-party embedders (OpenAI, OpenRouter, Ollama) for agentic memory — the
// pure-Go all-MiniLM-L6-v2 model runs in-process with zero configuration.
// The returned provider also satisfies Embedder.
func NewEmbedder() Provider {
	return NewLocalEmbedder("")
}

// RefreshModels clears cached model lists for all providers that support it,
// forcing re-fetch on next Models() call.
func (r *Registry) RefreshModels() {
	for _, p := range r.snapshot() {
		if refresher, ok := p.(ModelRefresher); ok {
			refresher.RefreshModels()
		}
	}
}

// ProviderPriority is the stable order used to choose a default provider when a
// session does not specify a model.
//
// User-configured first-party providers always win. Free-tier providers
// (keyed "ogcode-<collection>") are appended so the app works out-of-the-box
// with the community key pool, but never override a user's own credentials.
var ProviderPriority = []string{
	"anthropic", "openai", "openrouter", "ollama",
	"ogcode-openrouter", "ogcode-groq", "ogcode-cerebras", "ogcode-sambanova",
	"ogcode-github_models", "ogcode-nvidia",
}

// Default returns the highest-priority registered provider, or nil if the
// registry has no providers.
func (r *Registry) Default() Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, id := range ProviderPriority {
		if p, ok := r.providers[id]; ok {
			return p
		}
	}
	for _, p := range r.providers {
		return p
	}
	return nil
}

// ReplaceProviders atomically swaps the set of registered providers. Custom
// model routing (RegisterCustomModel) is preserved. Used to apply provider
// credential changes from the settings/onboarding UI without a server restart.
func (r *Registry) ReplaceProviders(providers map[string]Provider) {
	r.mu.Lock()
	r.providers = providers
	r.mu.Unlock()
}