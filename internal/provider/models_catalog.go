package provider

// =============================================================================
// MODEL CATALOG
// =============================================================================
//
// This file is the single place to add, remove, or update models for Anthropic
// and OpenAI. No other file needs to change.
//
// HOW TO ADD A MODEL
//   1. Find the right provider section below.
//   2. Append a CatalogModel entry.
//   3. Set ActiveByDefault: true only for broadly-useful, stable models.
//      Keep the active-by-default list small — users can enable others in settings.
//
// HOW TO RETIRE A MODEL
//   Remove or comment out the entry. Existing user preferences referencing the
//   old ID are ignored gracefully (the model simply won't appear in the list).
//
// FIELDS
//   ID              — exact model ID sent to the API
//   Name            — human-readable label shown in the UI
//   ActiveByDefault — whether the model is enabled without any user action
//   InputPricePerM  — USD per 1M input tokens (0 = unknown/free)
//   OutputPricePerM — USD per 1M output tokens (0 = unknown/free)
//
// =============================================================================

// CatalogModel is a statically-known model for a provider that does not expose
// a live /v1/models discovery endpoint.
type CatalogModel struct {
	ID              string
	Name            string
	ActiveByDefault bool
	InputPricePerM  float64 // USD per 1M input tokens (0 = unknown)
	OutputPricePerM float64 // USD per 1M output tokens (0 = unknown)
	SupportsImages  bool    // whether the model accepts image input
	ContextWindow   int     // total context length in tokens (0 = unknown → byte-size fallback)
	// MaxOutputTokens is the most output the model produces in one response.
	// 0 means unknown: no explicit limit is sent and the provider's own default
	// applies. NEVER guess this upward — a value above the model's real ceiling
	// makes every request fail, so understate it when a published figure is not
	// at hand. Only Anthropic entries carry a value today: the OpenAI-compatible
	// path (also used for OpenRouter and Ollama) would send it as `max_tokens`,
	// which the o-series and GPT-5 reasoning models reject in favour of
	// `max_completion_tokens`, so those deliberately stay at 0.
	MaxOutputTokens int
	// Thinking names the reasoning mode to request for this model, for providers
	// that must ask for it explicitly. "adaptive" is what Claude 4.6 and later
	// accept: the model decides when and how deeply to think, and reasons
	// between tool calls on its own with no beta header.
	//
	// Empty means no thinking configuration is sent. Claude 4.5 and earlier
	// accept only a fixed `budget_tokens` that has to fit inside `max_tokens` —
	// a tradeoff the agent loop does not currently make, since it leaves
	// `max_tokens` at the provider default — and Haiku 4.5 cannot reason
	// between tool calls at all, which is where an agent loop would spend it.
	Thinking string
}

// AnthropicModels is the authoritative list of Anthropic models.
// Maintained by contributors — see file header for instructions.
// All listed Claude models are multimodal and accept image input.
// All current Claude models expose a 200k-token context window by default.
var AnthropicModels = []CatalogModel{
	// ── Claude 4 family ──────────────────────────────────────────────────────
	{ID: "claude-opus-4-7", Name: "Claude Opus 4.7", ActiveByDefault: true, InputPricePerM: 15, OutputPricePerM: 75, SupportsImages: true, ContextWindow: 200000, MaxOutputTokens: 32000, Thinking: "adaptive"},
	{ID: "claude-opus-4-6", Name: "Claude Opus 4.6", ActiveByDefault: true, InputPricePerM: 15, OutputPricePerM: 75, SupportsImages: true, ContextWindow: 200000, MaxOutputTokens: 32000, Thinking: "adaptive"},
	{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6", ActiveByDefault: true, InputPricePerM: 3, OutputPricePerM: 15, SupportsImages: true, ContextWindow: 200000, MaxOutputTokens: 32000, Thinking: "adaptive"},
	{ID: "claude-haiku-4-5-20251001", Name: "Claude Haiku 4.5", ActiveByDefault: true, InputPricePerM: 0.80, OutputPricePerM: 4, SupportsImages: true, ContextWindow: 200000, MaxOutputTokens: 64000},

	// ── Claude 4 intermediate releases ───────────────────────────────────────
	{ID: "claude-opus-4-5-20251101", Name: "Claude Opus 4.5", ActiveByDefault: false, InputPricePerM: 15, OutputPricePerM: 75, SupportsImages: true, ContextWindow: 200000, MaxOutputTokens: 64000},
	{ID: "claude-opus-4-1-20250805", Name: "Claude Opus 4.1", ActiveByDefault: false, InputPricePerM: 15, OutputPricePerM: 75, SupportsImages: true, ContextWindow: 200000, MaxOutputTokens: 32000},
	{ID: "claude-sonnet-4-5-20250929", Name: "Claude Sonnet 4.5", ActiveByDefault: false, InputPricePerM: 3, OutputPricePerM: 15, SupportsImages: true, ContextWindow: 200000, MaxOutputTokens: 64000},

	// ── Claude 4 (older releases) ────────────────────────────────────────────
	{ID: "claude-opus-4-20250514", Name: "Claude Opus 4", ActiveByDefault: false, InputPricePerM: 15, OutputPricePerM: 75, SupportsImages: true, ContextWindow: 200000, MaxOutputTokens: 32000},
	{ID: "claude-sonnet-4-20250514", Name: "Claude Sonnet 4", ActiveByDefault: false, InputPricePerM: 3, OutputPricePerM: 15, SupportsImages: true, ContextWindow: 200000, MaxOutputTokens: 64000},
}

// OpenAIModels is the authoritative list of OpenAI models.
// Maintained by contributors — see file header for instructions.
// SupportsImages marks multimodal models. The o*-mini reasoning models are
// text-only; the GPT-4o/4.1/5 families and o1/o3/o4-mini accept images.
// ContextWindow values are conservative (biased low where the published figure
// is uncertain): understating only makes compaction trigger slightly early,
// never overflow.
var OpenAIModels = []CatalogModel{
	// ── GPT-5 family ────────────────────────────────────────────────────────
	{ID: "gpt-5", Name: "GPT-5", ActiveByDefault: true, InputPricePerM: 10, OutputPricePerM: 30, SupportsImages: true, ContextWindow: 272000},
	{ID: "gpt-5-mini", Name: "GPT-5 Mini", ActiveByDefault: true, InputPricePerM: 1.50, OutputPricePerM: 6, SupportsImages: true, ContextWindow: 272000},
	{ID: "gpt-5-nano", Name: "GPT-5 Nano", ActiveByDefault: false, InputPricePerM: 0.10, OutputPricePerM: 0.40, SupportsImages: true, ContextWindow: 272000},

	// ── GPT-4.1 family ─────────────────────────────────────────────────────
	{ID: "gpt-4.1", Name: "GPT-4.1", ActiveByDefault: true, InputPricePerM: 2, OutputPricePerM: 8, SupportsImages: true, ContextWindow: 1000000},
	{ID: "gpt-4.1-mini", Name: "GPT-4.1 Mini", ActiveByDefault: true, InputPricePerM: 0.40, OutputPricePerM: 1.60, SupportsImages: true, ContextWindow: 1000000},
	{ID: "gpt-4.1-nano", Name: "GPT-4.1 Nano", ActiveByDefault: false, InputPricePerM: 0.10, OutputPricePerM: 0.40, SupportsImages: true, ContextWindow: 1000000},

	// ── GPT-4o family ───────────────────────────────────────────────────────
	{ID: "gpt-4o", Name: "GPT-4o", ActiveByDefault: false, InputPricePerM: 2.50, OutputPricePerM: 10, SupportsImages: true, ContextWindow: 128000},
	{ID: "gpt-4o-mini", Name: "GPT-4o Mini", ActiveByDefault: false, InputPricePerM: 0.15, OutputPricePerM: 0.60, SupportsImages: true, ContextWindow: 128000},

	// ── Reasoning ────────────────────────────────────────────────────────────
	{ID: "o4-mini", Name: "o4 Mini", ActiveByDefault: true, InputPricePerM: 1.10, OutputPricePerM: 4.40, SupportsImages: true, ContextWindow: 200000},
	{ID: "o3", Name: "o3", ActiveByDefault: true, InputPricePerM: 10, OutputPricePerM: 40, SupportsImages: true, ContextWindow: 200000},
	{ID: "o3-mini", Name: "o3 Mini", ActiveByDefault: false, InputPricePerM: 1.10, OutputPricePerM: 4.40, ContextWindow: 200000},
	{ID: "o1", Name: "o1", ActiveByDefault: false, InputPricePerM: 15, OutputPricePerM: 60, SupportsImages: true, ContextWindow: 200000},
	{ID: "o1-mini", Name: "o1 Mini", ActiveByDefault: false, InputPricePerM: 1.50, OutputPricePerM: 6, ContextWindow: 128000},
}

// CatalogModelByID finds a model across the static catalogs above.
//
// Only Anthropic and OpenAI are covered. OpenRouter and Ollama discover their
// model lists at runtime and carry no pricing here, so callers must treat a
// false return as "unknown", not "free".
func CatalogModelByID(id string) (CatalogModel, bool) {
	for _, list := range [][]CatalogModel{AnthropicModels, OpenAIModels} {
		for _, m := range list {
			if m.ID == id {
				return m, true
			}
		}
	}
	return CatalogModel{}, false
}
