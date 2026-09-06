package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// streamIdleTimeout bounds how long a streaming response may go with NO data
// before it is aborted. Streaming requests carry no whole-request deadline (see
// streamHTTPClient), so this idle watchdog — reset on every chunk read off the
// wire — is what surfaces a dead connection. It is deliberately generous so a
// slow time-to-first-token doesn't trip it.
const streamIdleTimeout = 120 * time.Second

// streamIdleTimeoutBuffered is the idle budget for endpoints that do NOT stream
// tool-call arguments. Anthropic and OpenAI emit a tool call as a run of small
// deltas, so a working stream is never quiet for long and a tight budget is
// safe. Ollama composes the whole call and emits it in ONE frame when the model
// finishes — measured against a local endpoint: 13.4 seconds of complete
// silence for a 7 KB call, i.e. the wire stays silent for as long as the model
// spends writing the file. Under the tight budget that aborts healthy work on
// exactly the long-file turns that need it most, so these endpoints get a
// budget sized to outlast a large generation rather than a network blip.
const streamIdleTimeoutBuffered = 10 * time.Minute

// isLocalEndpoint reports whether a base URL points at this machine or the
// local network. Local model servers (Ollama, llama.cpp, LM Studio, vLLM and
// the relays people put in front of them) commonly batch tool calls the way
// Ollama does, and a loopback connection cannot suffer the network failures the
// tight idle budget exists to catch.
func isLocalEndpoint(baseURL string) bool {
	// isCloudURL already recognises loopback and the RFC1918 LAN ranges — reuse
	// it rather than restating that list in a second place.
	if !isCloudURL(baseURL) {
		return true
	}
	// It matches on substrings, so these host forms slip past it.
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	switch host := strings.ToLower(u.Hostname()); host {
	case "::1", "host.docker.internal":
		return true
	default:
		return strings.HasSuffix(host, ".local")
	}
}

// streamMaxLineBytes caps a single SSE line. Providers that do not chunk
// tool-call arguments send an entire call — a whole file's contents, for a write
// — in one `data:` line, and JSON escaping inflates it further. Too small a cap
// makes bufio fail with ErrTooLong part-way through a large response, which the
// agent loop can only report as a stream that ended without finishing.
const streamMaxLineBytes = 16 * 1024 * 1024

// streamResponseHeaderTimeout bounds the wait for response headers, the one
// phase the idle watchdog cannot cover (it only starts once the body exists).
// It is deliberately long: free and shared endpoints queue a request for
// minutes before answering, and that is a slow provider, not a dead connection.
const streamResponseHeaderTimeout = 300 * time.Second

// streamHTTPClient issues streaming requests. It deliberately has no
// Client.Timeout: that deadline covers the whole request including the body
// read, so a generation that legitimately runs long has its connection killed
// mid-stream. Liveness is bounded where it belongs instead — at connect, TLS and
// response-header time here, and by the per-stream idle watchdog once bytes are
// flowing.
var streamHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy:             http.ProxyFromEnvironment,
		DialContext:       (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2: true,
		MaxIdleConns:      100,
		// Below the ~60s idle-close observed on local relay endpoints (measured:
		// connections survive 50s idle, are closed by 70s). Pooling a connection
		// for longer than the peer keeps it alive hands dead sockets to new
		// requests; the transport usually retries those, but not reliably enough
		// to be worth the race. The cost is a fresh handshake for requests spaced
		// more than 30s apart.
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: streamResponseHeaderTimeout,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

// idleWatchdog aborts a stream that stops producing data. It wraps the response
// body so the timer resets on bytes actually read off the wire, rather than once
// per parsed SSE line: the reader goroutine also spends time blocked handing
// events to the agent loop, and counting that as idle cancels healthy streams
// whenever the consumer is slow — which is exactly what happens on the large
// responses this is meant to protect.
type idleWatchdog struct {
	r       io.Reader
	timer   *time.Timer
	timeout time.Duration
	fired   atomic.Bool
}

// newIdleWatchdog arms the watchdog with the caller's idle budget, which varies
// by endpoint — see streamIdleTimeoutBuffered.
func newIdleWatchdog(body io.Reader, cancel context.CancelFunc, timeout time.Duration) *idleWatchdog {
	if timeout <= 0 {
		timeout = streamIdleTimeout
	}
	w := &idleWatchdog{r: body, timeout: timeout}
	w.timer = time.AfterFunc(timeout, func() {
		w.fired.Store(true)
		cancel()
	})
	return w
}

func (w *idleWatchdog) Read(p []byte) (int, error) {
	n, err := w.r.Read(p)
	if n > 0 {
		w.timer.Reset(w.timeout)
	}
	return n, err
}

// Timeout is the idle budget this watchdog was armed with, so a report of the
// abort names the budget that actually applied rather than the default.
func (w *idleWatchdog) Timeout() time.Duration { return w.timeout }

// Fired reports whether the watchdog cancelled the request. It distinguishes "the
// connection went quiet" from "the caller aborted" — both of which surface as
// context.Canceled on the read.
func (w *idleWatchdog) Fired() bool { return w.fired.Load() }

func (w *idleWatchdog) Stop() { w.timer.Stop() }

// describeStreamReadError explains why a stream stopped part-way. Without it a
// failed scan is indistinguishable from a clean end of stream: the reader
// goroutine returns, the event channel closes, and the agent loop can only say
// the connection closed without a finish signal.
func describeStreamReadError(err error, idleFired bool, idleTimeout time.Duration) string {
	switch {
	case errors.Is(err, bufio.ErrTooLong):
		return fmt.Sprintf("stream read failed: provider sent a single SSE line larger than %d MB", streamMaxLineBytes/(1024*1024))
	case idleFired:
		return fmt.Sprintf("stream read failed: no data received for %s, connection appears stalled", idleTimeout)
	case errors.Is(err, context.Canceled):
		return "stream read failed: request cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "stream read failed: request deadline exceeded"
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return "stream read failed: provider closed the connection mid-response"
	default:
		return "stream read failed: " + err.Error()
	}
}

type StreamEventType string

const (
	EventTextDelta     StreamEventType = "text-delta"
	EventToolCallStart StreamEventType = "tool-call-start"
	EventToolCallDelta StreamEventType = "tool-call-delta"
	EventToolCallEnd   StreamEventType = "tool-call-end"
	// EventReasoningStart opens a thinking block. It carries no content: it
	// marks the boundary between one block and the next, so blocks are stored
	// and replayed separately rather than concatenated.
	EventReasoningStart     StreamEventType = "reasoning-start"
	EventReasoning          StreamEventType = "reasoning"
	EventReasoningSignature StreamEventType = "reasoning-signature"
	// EventReasoningRedacted carries a safety-redacted thinking block. It has
	// no readable text — only an opaque payload that must be round-tripped
	// verbatim as a redacted_thinking block, so it is its own event rather
	// than a signature on an empty reasoning block.
	EventReasoningRedacted StreamEventType = "reasoning-redacted"
	EventFinish            StreamEventType = "finish"
	EventUsage             StreamEventType = "usage"
	EventError             StreamEventType = "error"
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
	Type         StreamEventType `json:"type"`
	Text         string          `json:"text,omitempty"`
	Signature    string          `json:"signature,omitempty"`
	RedactedData string          `json:"redactedData,omitempty"`
	ToolCallID   string          `json:"toolCallId,omitempty"`
	ToolName     string          `json:"toolName,omitempty"`
	ToolInput    json.RawMessage `json:"toolInput,omitempty"`
	FinishReason *string         `json:"finishReason,omitempty"`
	Usage        *TokenUsage     `json:"usage,omitempty"`
	Error        string          `json:"error,omitempty"`
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
	// ReasoningParts carries thinking/reasoning blocks from a previous assistant
	// turn. Anthropic requires these to be forwarded back as "thinking" content
	// blocks with their signatures intact; OpenAI-family providers handle
	// reasoning tokens server-side and should ignore this field.
	ReasoningParts []ReasoningPart `json:"reasoningParts,omitempty"`
}

// ReasoningPart represents a thinking/reasoning block from a model's response.
// Anthropic models return these with a cryptographic signature that must be
// forwarded back unchanged on subsequent turns.
type ReasoningPart struct {
	Text      string `json:"text"`
	Signature string `json:"signature,omitempty"`
	// RedactedData is the opaque payload of a redacted_thinking block. When
	// set, the block carries no readable text and must be re-sent as a
	// redacted_thinking block rather than a thinking block.
	RedactedData string `json:"redactedData,omitempty"`
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type StreamRequest struct {
	Model       string           `json:"model"`
	System      []string         `json:"system"`
	Messages    []ModelMessage   `json:"messages"`
	Tools       []ToolDefinition `json:"tools"`
	Temperature float64          `json:"temperature,omitempty"`
	MaxTokens   int              `json:"maxTokens,omitempty"`
	// Thinking asks for the model's reasoning mode, where the provider and the
	// model support one. Only the agent loop sets it. The short utility calls —
	// titles, the auto-mode risk gate, compaction — run on tight max_tokens
	// budgets that thinking would spend before reaching an answer, and none of
	// them is the kind of work reasoning improves.
	Thinking bool `json:"thinking,omitempty"`
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
	// ContextWindow is the model's total context length in tokens (0 = unknown).
	// Used to size the compaction trigger; when 0 the loop falls back to a fixed
	// byte-size heuristic.
	ContextWindow int `json:"contextWindow,omitempty"`
	// MaxOutputTokens is the most output the model will produce in one response
	// (0 = unknown, leave the request's limit to the provider's own default).
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
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

// ContextWindow returns the model's total context length in tokens, or 0 when
// unknown (dynamically-fetched models without catalog metadata). Callers treat
// 0 as "fall back to a size heuristic".
func (r *Registry) ContextWindow(modelID string) int {
	if modelID == "" {
		return 0
	}
	for _, p := range r.snapshot() {
		for _, m := range p.Models() {
			if m.ID == modelID {
				return m.ContextWindow
			}
		}
	}
	return 0
}

// MaxOutputTokens returns the model's output ceiling in tokens, or 0 when
// unknown. Callers treat 0 as "send no explicit limit and let the provider
// apply its own default" — overstating a ceiling makes every request fail, so
// unknown must never be guessed upward.
func (r *Registry) MaxOutputTokens(modelID string) int {
	if modelID == "" {
		return 0
	}
	for _, p := range r.snapshot() {
		for _, m := range p.Models() {
			if m.ID == modelID {
				return m.MaxOutputTokens
			}
		}
	}
	return 0
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

// IsCustomModel reports whether modelID was registered as a user-added custom
// model (rather than a built-in catalog or dynamically-fetched model). Custom
// models are not present in any provider's curated catalog, so capability
// lookups that trust the catalog must treat them as unknown and probe instead.
func (r *Registry) IsCustomModel(modelID string) bool {
	r.customMu.RLock()
	defer r.customMu.RUnlock()
	_, ok := r.customModels[modelID]
	return ok
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
// pure-Go gte-small model runs in-process with zero configuration.
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
	"ogcode-openrouter", "ogcode-cerebras", "ogcode-sambanova",
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
