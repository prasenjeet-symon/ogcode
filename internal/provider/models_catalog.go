package provider

// =============================================================================
// MODEL CATALOG
// =============================================================================
//
// This file is the single place to add, remove, or update catalogued models:
// Anthropic and OpenAI (whose providers list exactly these) and the major
// open-weight families (OpenModels), which any provider may serve — OpenRouter,
// Ollama, Groq, OGX or a custom OpenAI-compatible endpoint. No other file needs
// to change.
//
// The catalogue knows a model's context window, pricing and image support
// whichever provider serves it: LookupCatalogModel matches every spelling of an
// id (see catalog_lookup.go), so a model a user adds by hand still gets its real
// window instead of an unknown. A host's own smaller window, its own price and a
// local runtime's limits still win where they apply — see Registry.ContextWindow,
// Registry.PriceOn and Registry.CatalogModel.
//
// HOW TO ADD A MODEL
//   1. Find the right provider section below.
//   2. Append a CatalogModel entry, with the host ids normalisation cannot
//      reach as Aliases. An alias that means different models on different
//      hosts is left out: an unknown window beats a wrong one.
//   3. Set ActiveByDefault: true only for broadly-useful, stable models.
//      Keep the active-by-default list small — users can enable others in settings.
//   4. The catalog tests fail on an id two entries claim.
//
// HOW TO RETIRE A MODEL
//   Move it to LegacyModels while other hosts still serve it, else remove it.
//   Existing user preferences referencing the old ID are ignored gracefully (the
//   model simply won't appear in the list).
//
// FIELDS
//   ID              — exact model ID sent to the API (first-party id where one
//                     exists, else the Hugging Face repo name)
//   Aliases         — other ids for the same model on other hosts (dated
//                     snapshots, OpenRouter slugs, Ollama tags, Groq ids) that
//                     normalisation alone does not reach
//   Name            — human-readable label shown in the UI
//   ActiveByDefault — whether the model is enabled without any user action
//   InputPricePerM  — USD per 1M input tokens (0 = unknown/free)
//   OutputPricePerM — USD per 1M output tokens (0 = unknown/free)
//   CacheReadPricePerM / CacheWritePricePerM — USD per 1M cached-input tokens
//                     read / written (0 = not published; cost estimates fall
//                     back to multiples of the input price)
//   ContextWindow   — the most a request can hold where the model is served
//                     (an input cap below the total window is recorded). NEVER
//                     guess upward: an overstated window lets a request
//                     overflow; an understated one only compacts a little early.
//   the rest        — output ceiling and request quirks; see CatalogModel.
//
// =============================================================================

// CatalogModel is what is known about one model, whichever provider serves it.
type CatalogModel struct {
	ID string
	// Aliases are other ids for this model on other hosts; see the header.
	Aliases         []string
	Name            string
	ActiveByDefault bool
	InputPricePerM  float64 // USD per 1M input tokens (0 = unknown)
	OutputPricePerM float64 // USD per 1M output tokens (0 = unknown)
	// CacheReadPricePerM and CacheWritePricePerM price cached input (0 = not
	// published). Anthropic bills both; OpenAI bills writes only on GPT-5.6 and
	// later (earlier models write at the plain input price, recorded as such),
	// and most open-model APIs bill only reads. A model with no cache discount
	// records its input price as the read price.
	CacheReadPricePerM  float64
	CacheWritePricePerM float64
	SupportsImages      bool // whether the model accepts image input
	// ContextWindow is the most a request can hold, in tokens (0 = unknown →
	// byte-size fallback). Where a vendor caps input below the total window —
	// OpenAI's 1.05M models take at most 922K in, its 400K models 272K — the
	// cap is recorded, since that is where a request starts to be rejected.
	ContextWindow int
	// MaxOutputTokens is the most output the model produces in one response.
	// 0 means unknown: no explicit limit is sent and the provider's own default
	// applies. NEVER guess this upward — a value above the model's real ceiling
	// makes every request fail, so understate it when a published figure is not
	// at hand. Only Anthropic entries carry a value that is sent: the OpenAI
	// entries stay at 0 (no cap is needed there, and the published figure is in
	// a comment), and no provider lists OpenModels, so their figure is only
	// informational.
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
	// RejectsSampling marks models that refuse temperature (and other sampling
	// parameters) outright, thinking or not — Claude Opus 4.7 and later, Kimi's
	// current models. Every provider drops the parameter for them rather than
	// fail the request.
	RejectsSampling bool

	// OpenAI reasoning, which shapes a Chat Completions request (see
	// shapeForOpenAIModel in openai.go). Unset for every other vendor.
	//
	// EffortFloor is the lowest reasoning_effort the model accepts — "none",
	// "minimal" or "low" — and empty for a model that does not reason. A call
	// that does not ask for thinking (every utility call) sends it, so a small
	// output budget is not spent on reasoning. Any effort but "none" means the
	// model is reasoning, and a reasoning model rejects temperature.
	EffortFloor string
	// ToolsNeedNoReasoning marks a model that takes tools on Chat Completions
	// only with reasoning_effort "none" — GPT-5.4 and later. Tool calls with
	// reasoning need the Responses API, which ogcode does not speak.
	ToolsNeedNoReasoning bool
	// ResponsesOnly marks a model whose tool calling needs the Responses API
	// (GPT-6 Astra, the pro and Codex models). The agent always sends tools
	// over Chat Completions, so the OpenAI provider does not offer it; the
	// catalogue still knows its window and price for a host that bridges the
	// two (OpenRouter).
	ResponsesOnly bool
}

// AnthropicModels is the authoritative list of models the Claude API serves
// (current and legacy-but-active; retired models are in LegacyModels).
// Maintained by contributors — see file header for instructions.
//
// Source: platform.claude.com/docs models overview, per-model pages, pricing and
// model-deprecations pages, verified 2026-09-28. All are multimodal. From the
// 4.6 generation on, the dateless ID is the pinned snapshot, the default window
// is 1M tokens (generally available, no beta header, no long-context premium)
// and output is capped at 128K; the 4.5 models are 200K/64K. Cache prices are
// the 5-minute write and the read; Fable 5.1 and Opus 5.5 discount reads below
// the usual 0.1x.
var AnthropicModels = []CatalogModel{
	// ── Current ──────────────────────────────────────────────────────────────
	{ID: "claude-fable-5-1", Name: "Claude Fable 5.1", ActiveByDefault: true, InputPricePerM: 10, OutputPricePerM: 50, CacheWritePricePerM: 12.5, CacheReadPricePerM: 0.25, SupportsImages: true, ContextWindow: 1_000_000, MaxOutputTokens: 128_000, Thinking: "adaptive", RejectsSampling: true},
	{ID: "claude-opus-5-5", Name: "Claude Opus 5.5", ActiveByDefault: true, InputPricePerM: 4, OutputPricePerM: 20, CacheWritePricePerM: 5, CacheReadPricePerM: 0.2, SupportsImages: true, ContextWindow: 1_000_000, MaxOutputTokens: 128_000, Thinking: "adaptive", RejectsSampling: true},
	{ID: "claude-sonnet-5", Name: "Claude Sonnet 5", ActiveByDefault: true, InputPricePerM: 2, OutputPricePerM: 10, CacheWritePricePerM: 2.5, CacheReadPricePerM: 0.2, SupportsImages: true, ContextWindow: 1_000_000, MaxOutputTokens: 128_000, Thinking: "adaptive", RejectsSampling: true},
	{ID: "claude-haiku-4-5-20251001", Aliases: []string{"claude-haiku-4-5"}, Name: "Claude Haiku 4.5", ActiveByDefault: true, InputPricePerM: 1, OutputPricePerM: 5, CacheWritePricePerM: 1.25, CacheReadPricePerM: 0.1, SupportsImages: true, ContextWindow: 200_000, MaxOutputTokens: 64_000},
	// Invitation-only (Project Glasswing): listed so an invited account can pick it.
	{ID: "claude-mythos-5-1", Name: "Claude Mythos 5.1", InputPricePerM: 10, OutputPricePerM: 50, CacheWritePricePerM: 12.5, CacheReadPricePerM: 0.25, SupportsImages: true, ContextWindow: 1_000_000, MaxOutputTokens: 128_000, Thinking: "adaptive", RejectsSampling: true},

	// ── Legacy (still served) ────────────────────────────────────────────────
	{ID: "claude-fable-5", Name: "Claude Fable 5", InputPricePerM: 10, OutputPricePerM: 50, CacheWritePricePerM: 12.5, CacheReadPricePerM: 1, SupportsImages: true, ContextWindow: 1_000_000, MaxOutputTokens: 128_000, Thinking: "adaptive", RejectsSampling: true},
	{ID: "claude-mythos-5", Name: "Claude Mythos 5", InputPricePerM: 10, OutputPricePerM: 50, CacheWritePricePerM: 12.5, CacheReadPricePerM: 1, SupportsImages: true, ContextWindow: 1_000_000, MaxOutputTokens: 128_000, Thinking: "adaptive", RejectsSampling: true},
	{ID: "claude-opus-5", Name: "Claude Opus 5", InputPricePerM: 5, OutputPricePerM: 25, CacheWritePricePerM: 6.25, CacheReadPricePerM: 0.5, SupportsImages: true, ContextWindow: 1_000_000, MaxOutputTokens: 128_000, Thinking: "adaptive", RejectsSampling: true},
	{ID: "claude-opus-4-8", Name: "Claude Opus 4.8", InputPricePerM: 5, OutputPricePerM: 25, CacheWritePricePerM: 6.25, CacheReadPricePerM: 0.5, SupportsImages: true, ContextWindow: 1_000_000, MaxOutputTokens: 128_000, Thinking: "adaptive", RejectsSampling: true},
	{ID: "claude-opus-4-7", Name: "Claude Opus 4.7", InputPricePerM: 5, OutputPricePerM: 25, CacheWritePricePerM: 6.25, CacheReadPricePerM: 0.5, SupportsImages: true, ContextWindow: 1_000_000, MaxOutputTokens: 128_000, Thinking: "adaptive", RejectsSampling: true},
	{ID: "claude-opus-4-6", Name: "Claude Opus 4.6", InputPricePerM: 5, OutputPricePerM: 25, CacheWritePricePerM: 6.25, CacheReadPricePerM: 0.5, SupportsImages: true, ContextWindow: 1_000_000, MaxOutputTokens: 128_000, Thinking: "adaptive"},
	{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6", InputPricePerM: 3, OutputPricePerM: 15, CacheWritePricePerM: 3.75, CacheReadPricePerM: 0.3, SupportsImages: true, ContextWindow: 1_000_000, MaxOutputTokens: 128_000, Thinking: "adaptive"},
	// The 4.5 models take only a fixed thinking budget, which the loop does not
	// size, so no thinking mode is requested for them.
	{ID: "claude-opus-4-5-20251101", Aliases: []string{"claude-opus-4-5"}, Name: "Claude Opus 4.5", InputPricePerM: 5, OutputPricePerM: 25, CacheWritePricePerM: 6.25, CacheReadPricePerM: 0.5, SupportsImages: true, ContextWindow: 200_000, MaxOutputTokens: 64_000},
	{ID: "claude-sonnet-4-5-20250929", Aliases: []string{"claude-sonnet-4-5"}, Name: "Claude Sonnet 4.5", InputPricePerM: 3, OutputPricePerM: 15, CacheWritePricePerM: 3.75, CacheReadPricePerM: 0.3, SupportsImages: true, ContextWindow: 200_000, MaxOutputTokens: 64_000},
}

// OpenAIModels is the list of OpenAI models the OpenAI provider offers, minus
// the ResponsesOnly ones, which stay catalogued for hosts that bridge them.
// Maintained by contributors — see file header for instructions.
//
// Source: developers.openai.com model pages, pricing, deprecations, changelog
// and the GPT-6 and Responses migration guides, verified 2026-09-28. Prices are
// the Standard tier for prompts under 272K tokens. Already-retired models are
// left out (the retired Codex models OpenRouter still serves are in
// LegacyModels), as are gpt-oss (in OpenModels: OpenAI does not serve it) and
// the chat-latest aliases, which OpenAI does not recommend for API use.
var OpenAIModels = []CatalogModel{
	// ── GPT-6 (current flagships) ───────────────────────────────────────────
	// 1.05M-token window with at most 922K in and 128K out. A prompt over 272K
	// input tokens bills the whole request at 2x input and 1.5x output; the
	// estimates here use the standard rates. Cache writes cost 1.25x input.
	{ID: "gpt-6-sol", Aliases: []string{"gpt-6-sol-pro"}, Name: "GPT-6 Sol", ActiveByDefault: true, InputPricePerM: 2, OutputPricePerM: 10, CacheWritePricePerM: 2.5, CacheReadPricePerM: 0.2, SupportsImages: true, ContextWindow: 922_000, EffortFloor: "none", ToolsNeedNoReasoning: true},
	{ID: "gpt-6-luna", Aliases: []string{"gpt-6-luna-pro"}, Name: "GPT-6 Luna", ActiveByDefault: true, InputPricePerM: 0.1, OutputPricePerM: 0.5, CacheWritePricePerM: 0.125, CacheReadPricePerM: 0.01, SupportsImages: true, ContextWindow: 922_000, EffortFloor: "none", ToolsNeedNoReasoning: true},
	// Astra's tool calling needs the Responses API, and it has no "none" effort.
	{ID: "gpt-6-astra", Aliases: []string{"gpt-6-astra-pro"}, Name: "GPT-6 Astra", InputPricePerM: 10, OutputPricePerM: 50, CacheWritePricePerM: 12.5, CacheReadPricePerM: 1, SupportsImages: true, ContextWindow: 922_000, EffortFloor: "low", ResponsesOnly: true},

	// ── GPT-5.6 ─────────────────────────────────────────────────────────────
	// Same limits and long-prompt rule as GPT-6. Sol's $4/$20 is promotional,
	// "available at least through November 21, 2026"; "gpt-5.6" routes to Sol.
	{ID: "gpt-5.6-sol", Aliases: []string{"gpt-5.6", "gpt-5.6-sol-pro"}, Name: "GPT-5.6 Sol", ActiveByDefault: true, InputPricePerM: 4, OutputPricePerM: 20, CacheWritePricePerM: 5, CacheReadPricePerM: 0.4, SupportsImages: true, ContextWindow: 922_000, EffortFloor: "none", ToolsNeedNoReasoning: true},
	{ID: "gpt-5.6-terra", Aliases: []string{"gpt-5.6-terra-pro"}, Name: "GPT-5.6 Terra", ActiveByDefault: true, InputPricePerM: 2, OutputPricePerM: 12, CacheWritePricePerM: 2.5, CacheReadPricePerM: 0.2, SupportsImages: true, ContextWindow: 922_000, EffortFloor: "none", ToolsNeedNoReasoning: true},
	{ID: "gpt-5.6-luna", Aliases: []string{"gpt-5.6-luna-pro"}, Name: "GPT-5.6 Luna", InputPricePerM: 0.2, OutputPricePerM: 1.2, CacheWritePricePerM: 0.25, CacheReadPricePerM: 0.02, SupportsImages: true, ContextWindow: 922_000, EffortFloor: "none", ToolsNeedNoReasoning: true},

	// ── GPT-5.5 and 5.4 ─────────────────────────────────────────────────────
	// OpenAI states no input cap for the 1.05M models here; 922K is the cap it
	// states for the same window on GPT-5.6 and 6. Pro models have no cache
	// discount and need the Responses API.
	{ID: "gpt-5.5", Name: "GPT-5.5", InputPricePerM: 5, OutputPricePerM: 30, CacheWritePricePerM: 5, CacheReadPricePerM: 0.5, SupportsImages: true, ContextWindow: 922_000, EffortFloor: "none", ToolsNeedNoReasoning: true},
	{ID: "gpt-5.5-pro", Name: "GPT-5.5 Pro", InputPricePerM: 30, OutputPricePerM: 180, CacheWritePricePerM: 30, CacheReadPricePerM: 30, SupportsImages: true, ContextWindow: 922_000, EffortFloor: "medium", ResponsesOnly: true},
	{ID: "gpt-5.4", Name: "GPT-5.4", InputPricePerM: 2.5, OutputPricePerM: 15, CacheWritePricePerM: 2.5, CacheReadPricePerM: 0.25, SupportsImages: true, ContextWindow: 922_000, EffortFloor: "none", ToolsNeedNoReasoning: true},
	{ID: "gpt-5.4-mini", Name: "GPT-5.4 Mini", InputPricePerM: 0.75, OutputPricePerM: 4.5, CacheWritePricePerM: 0.75, CacheReadPricePerM: 0.075, SupportsImages: true, ContextWindow: 272_000, EffortFloor: "none", ToolsNeedNoReasoning: true},
	{ID: "gpt-5.4-nano", Name: "GPT-5.4 Nano", InputPricePerM: 0.2, OutputPricePerM: 1.25, CacheWritePricePerM: 0.2, CacheReadPricePerM: 0.02, SupportsImages: true, ContextWindow: 272_000, EffortFloor: "none", ToolsNeedNoReasoning: true},
	{ID: "gpt-5.4-pro", Name: "GPT-5.4 Pro", InputPricePerM: 30, OutputPricePerM: 180, CacheWritePricePerM: 30, CacheReadPricePerM: 30, SupportsImages: true, ContextWindow: 922_000, EffortFloor: "medium", ResponsesOnly: true},
	{ID: "gpt-5.3-codex", Name: "GPT-5.3 Codex", InputPricePerM: 1.75, OutputPricePerM: 14, CacheWritePricePerM: 1.75, CacheReadPricePerM: 0.175, SupportsImages: true, ContextWindow: 272_000, EffortFloor: "low", ResponsesOnly: true},

	// ── GPT-5.2 and 5.1 ─────────────────────────────────────────────────────
	// 400K window, 128K out; OpenAI states no input cap, so the 272K it states
	// for the same window on GPT-5 stands. Tools work with reasoning here.
	{ID: "gpt-5.2", Name: "GPT-5.2", InputPricePerM: 1.75, OutputPricePerM: 14, CacheWritePricePerM: 1.75, CacheReadPricePerM: 0.175, SupportsImages: true, ContextWindow: 272_000, EffortFloor: "none"},
	{ID: "gpt-5.2-pro", Name: "GPT-5.2 Pro", InputPricePerM: 21, OutputPricePerM: 168, CacheWritePricePerM: 21, CacheReadPricePerM: 21, SupportsImages: true, ContextWindow: 272_000, EffortFloor: "medium", ResponsesOnly: true},
	{ID: "gpt-5.1", Name: "GPT-5.1", InputPricePerM: 1.25, OutputPricePerM: 10, CacheWritePricePerM: 1.25, CacheReadPricePerM: 0.125, SupportsImages: true, ContextWindow: 272_000, EffortFloor: "none"},

	// ── GPT-5 (snapshots shut down 2026-12-11) ──────────────────────────────
	// Always reasons: "minimal" is the least it accepts.
	{ID: "gpt-5", Name: "GPT-5", InputPricePerM: 1.25, OutputPricePerM: 10, CacheWritePricePerM: 1.25, CacheReadPricePerM: 0.125, SupportsImages: true, ContextWindow: 272_000, EffortFloor: "minimal"},
	{ID: "gpt-5-mini", Name: "GPT-5 Mini", InputPricePerM: 0.25, OutputPricePerM: 2, CacheWritePricePerM: 0.25, CacheReadPricePerM: 0.025, SupportsImages: true, ContextWindow: 272_000, EffortFloor: "minimal"},
	{ID: "gpt-5-nano", Name: "GPT-5 Nano", InputPricePerM: 0.05, OutputPricePerM: 0.4, CacheWritePricePerM: 0.05, CacheReadPricePerM: 0.005, SupportsImages: true, ContextWindow: 272_000, EffortFloor: "minimal"},
	{ID: "gpt-5-pro", Name: "GPT-5 Pro", InputPricePerM: 15, OutputPricePerM: 120, CacheWritePricePerM: 15, CacheReadPricePerM: 15, SupportsImages: true, ContextWindow: 272_000, EffortFloor: "high", ResponsesOnly: true},

	// ── GPT-4.1 and GPT-4o (no reasoning) ───────────────────────────────────
	// 4.1: ~1M window, 32,768 out; 4o: 128K window, 16,384 out. GPT-4.1 Nano
	// shuts down 2026-10-23.
	{ID: "gpt-4.1", Name: "GPT-4.1", InputPricePerM: 2, OutputPricePerM: 8, CacheWritePricePerM: 2, CacheReadPricePerM: 0.5, SupportsImages: true, ContextWindow: 1_047_576},
	{ID: "gpt-4.1-mini", Name: "GPT-4.1 Mini", InputPricePerM: 0.4, OutputPricePerM: 1.6, CacheWritePricePerM: 0.4, CacheReadPricePerM: 0.1, SupportsImages: true, ContextWindow: 1_047_576},
	{ID: "gpt-4.1-nano", Name: "GPT-4.1 Nano", InputPricePerM: 0.1, OutputPricePerM: 0.4, CacheWritePricePerM: 0.1, CacheReadPricePerM: 0.025, SupportsImages: true, ContextWindow: 1_047_576},
	{ID: "gpt-4o", Name: "GPT-4o", InputPricePerM: 2.5, OutputPricePerM: 10, CacheWritePricePerM: 2.5, CacheReadPricePerM: 1.25, SupportsImages: true, ContextWindow: 128_000},
	{ID: "gpt-4o-mini", Name: "GPT-4o Mini", InputPricePerM: 0.15, OutputPricePerM: 0.6, CacheWritePricePerM: 0.15, CacheReadPricePerM: 0.075, SupportsImages: true, ContextWindow: 128_000},

	// ── o-series (always reason; take no temperature) ───────────────────────
	// 200K window, 100K out. o4-mini, o3-mini and o1 shut down 2026-10-23; o3
	// and the pro models 2026-12-11.
	{ID: "o3", Name: "o3", InputPricePerM: 2, OutputPricePerM: 8, CacheWritePricePerM: 2, CacheReadPricePerM: 0.5, SupportsImages: true, ContextWindow: 200_000, EffortFloor: "low"},
	{ID: "o4-mini", Aliases: []string{"o4-mini-high"}, Name: "o4 Mini", InputPricePerM: 1.1, OutputPricePerM: 4.4, CacheWritePricePerM: 1.1, CacheReadPricePerM: 0.275, SupportsImages: true, ContextWindow: 200_000, EffortFloor: "low"},
	{ID: "o3-mini", Aliases: []string{"o3-mini-high"}, Name: "o3 Mini", InputPricePerM: 1.1, OutputPricePerM: 4.4, CacheWritePricePerM: 1.1, CacheReadPricePerM: 0.55, ContextWindow: 200_000, EffortFloor: "low"},
	{ID: "o1", Name: "o1", InputPricePerM: 15, OutputPricePerM: 60, CacheWritePricePerM: 15, CacheReadPricePerM: 7.5, SupportsImages: true, ContextWindow: 200_000, EffortFloor: "low"},
	{ID: "o3-pro", Name: "o3 Pro", InputPricePerM: 20, OutputPricePerM: 80, CacheWritePricePerM: 20, CacheReadPricePerM: 20, SupportsImages: true, ContextWindow: 200_000, EffortFloor: "low", ResponsesOnly: true},
	{ID: "o1-pro", Name: "o1 Pro", InputPricePerM: 150, OutputPricePerM: 600, CacheWritePricePerM: 150, CacheReadPricePerM: 150, SupportsImages: true, ContextWindow: 200_000, EffortFloor: "low", ResponsesOnly: true},
}

// CatalogModelByID finds a model across every catalogue list under any of its
// names (see LookupCatalogModel). Callers must treat a false return as
// "unknown", not "free".
func CatalogModelByID(id string) (CatalogModel, bool) {
	return LookupCatalogModel(id)
}

// OpenModels is the catalogue of major open-weight model families. No provider
// lists these on its own; they are reached through whichever host serves them
// (OpenRouter, Ollama, Groq, OGX, Together, Fireworks, a custom endpoint), so
// each entry carries the ids those hosts use as Aliases — Hugging Face repos,
// OpenRouter slugs, Ollama tags (":latest" names the size Ollama defaults to),
// Groq and Fireworks ids. An alias that means different models on different
// hosts is left out rather than guessed.
//
// Sources: each vendor's API docs and pricing pages, Hugging Face model cards
// and configs, OpenRouter's /models and /endpoints, Ollama's library and
// ollama.com/api/tags, Groq, Together and Fireworks docs — verified 2026-09-28.
//
// Prices are the vendor's own API where it serves the model (DeepSeek, Alibaba
// Qwen Cloud, Moonshot, Z.ai, MiniMax, Mistral, Azure for Phi), else the
// model's OpenRouter price; 0 where neither prices it. A host that lists its
// own price (OpenRouter) is billed at that, not at these (see PriceOn).
//
// ContextWindow is the most a request can hold where the model is commonly
// served, biased low where hosts disagree: a larger host still works, just
// compacts early, and a host that serves less corrects it on its first
// overflow (see EffectiveContextWindow). MaxOutputTokens is the vendor's
// published ceiling, informational only — no provider lists these entries.
var OpenModels = []CatalogModel{
	// ── DeepSeek (api.deepseek.com) ─────────────────────────────────────────
	// Peak-hour prices; off-peak is half. The first-party API serves two ids;
	// its older names (deepseek-v4-flash, -vision-exp) now route to V4.1 Flash.
	{ID: "deepseek-flash", Aliases: []string{"deepseek-v4.1-flash", "deepseek-ai/DeepSeek-V4.1-Flash", "deepseek-v4p1-flash", "deepseek-v4-flash", "deepseek-v4-flash-vision-exp"}, Name: "DeepSeek V4.1 Flash", InputPricePerM: 0.3, OutputPricePerM: 1.2, CacheReadPricePerM: 0.006, SupportsImages: true, ContextWindow: 1_048_576, MaxOutputTokens: 393_216},
	{ID: "deepseek-v4-pro", Aliases: []string{"deepseek-v4-pro-0813", "deepseek-ai/DeepSeek-V4-Pro-0813", "deepseek-ai/DeepSeek-V4-Pro"}, Name: "DeepSeek V4 Pro", InputPricePerM: 1.32, OutputPricePerM: 3.96, CacheReadPricePerM: 0.044, ContextWindow: 1_048_576, MaxOutputTokens: 393_216},
	// Retired first-party; still served elsewhere (OpenRouter prices).
	{ID: "deepseek-ai/DeepSeek-V4-Flash-0731", Name: "DeepSeek V4 Flash 0731", InputPricePerM: 0.021, OutputPricePerM: 0.32, CacheReadPricePerM: 0.016, ContextWindow: 1_048_576},
	{ID: "deepseek-ai/DeepSeek-V3.2", Aliases: []string{"deepseek-v3.2-exp"}, Name: "DeepSeek V3.2", InputPricePerM: 0.28, OutputPricePerM: 0.42, CacheReadPricePerM: 0.028, ContextWindow: 163_840},
	{ID: "deepseek-ai/DeepSeek-V3.1-Terminus", Aliases: []string{"deepseek-v3.1:671b-terminus"}, Name: "DeepSeek V3.1 Terminus", InputPricePerM: 0.27, OutputPricePerM: 1, CacheReadPricePerM: 0.135, ContextWindow: 163_840},
	{ID: "deepseek-ai/DeepSeek-V3.1", Aliases: []string{"deepseek-chat-v3.1", "deepseek-v3.1:671b"}, Name: "DeepSeek V3.1", InputPricePerM: 0.25, OutputPricePerM: 0.95, CacheReadPricePerM: 0.13, ContextWindow: 163_840},
	// Not bare "deepseek-r1": that is the 64K original on OpenRouter and an 8B
	// distill as Ollama's default tag.
	{ID: "deepseek-ai/DeepSeek-R1-0528", Aliases: []string{"deepseek-r1:671b", "deepseek-r1:671b-0528"}, Name: "DeepSeek R1 0528", InputPricePerM: 0.5, OutputPricePerM: 2.15, CacheReadPricePerM: 0.35, ContextWindow: 163_840},

	// ── Qwen (Alibaba Qwen Cloud / Model Studio) ────────────────────────────
	// The 3.8 models serve 1M, of which 983K may be input while thinking; the
	// weights are 262K native, so a self-hosted copy may serve less. Coder
	// prices are the lowest (≤32K-token prompt) tier.
	{ID: "qwen3.8-2.4t-a95b", Aliases: []string{"Qwen/Qwen3.8-2.4T-A95B-FP8"}, Name: "Qwen3.8 2.4T-A95B", InputPricePerM: 2, OutputPricePerM: 6, CacheReadPricePerM: 0.25, ContextWindow: 983_000, MaxOutputTokens: 131_072},
	{ID: "qwen3.8-27b", Aliases: []string{"Qwen/Qwen3.8-27B-FP8", "qwen3.8"}, Name: "Qwen3.8 27B", InputPricePerM: 0.5, OutputPricePerM: 3, CacheReadPricePerM: 0.1, SupportsImages: true, ContextWindow: 983_000, MaxOutputTokens: 131_072},
	{ID: "Qwen/Qwen3.8-Flash-Next", Aliases: []string{"qwen3.8-flash", "qwen3.8-flash-next:125b-a6b"}, Name: "Qwen3.8 Flash", InputPricePerM: 0.15, OutputPricePerM: 0.47, CacheReadPricePerM: 0.016, SupportsImages: true, ContextWindow: 983_000, MaxOutputTokens: 131_072},
	{ID: "qwen3.6-35b-a3b", Aliases: []string{"qwen3.6:35b", "qwen3.6"}, Name: "Qwen3.6 35B-A3B", InputPricePerM: 0.375, OutputPricePerM: 2.25, SupportsImages: true, ContextWindow: 258_000, MaxOutputTokens: 65_536},
	{ID: "qwen3.6-27b", Name: "Qwen3.6 27B", InputPricePerM: 0.6, OutputPricePerM: 3.6, SupportsImages: true, ContextWindow: 258_000, MaxOutputTokens: 65_536},
	{ID: "qwen3.5-397b-a17b", Name: "Qwen3.5 397B-A17B", InputPricePerM: 0.6, OutputPricePerM: 3.6, SupportsImages: true, ContextWindow: 258_000, MaxOutputTokens: 65_536},
	{ID: "qwen3.5-122b-a10b", Aliases: []string{"qwen3.5:122b"}, Name: "Qwen3.5 122B-A10B", InputPricePerM: 0.4, OutputPricePerM: 3.2, SupportsImages: true, ContextWindow: 258_000, MaxOutputTokens: 65_536},
	{ID: "qwen3.5-35b-a3b", Aliases: []string{"qwen3.5:35b"}, Name: "Qwen3.5 35B-A3B", InputPricePerM: 0.25, OutputPricePerM: 2, SupportsImages: true, ContextWindow: 258_000, MaxOutputTokens: 65_536},
	{ID: "qwen3.5-27b", Name: "Qwen3.5 27B", InputPricePerM: 0.3, OutputPricePerM: 2.4, SupportsImages: true, ContextWindow: 258_000, MaxOutputTokens: 65_536},
	// Qwen Cloud takes at most 204K input on the coder models.
	{ID: "qwen3-coder-next", Name: "Qwen3 Coder Next", InputPricePerM: 0.3, OutputPricePerM: 1.5, ContextWindow: 204_800, MaxOutputTokens: 65_536},
	// "qwen3-coder" is this model on OpenRouter; Ollama's default tag is the 30B.
	{ID: "qwen3-coder-480b-a35b-instruct", Aliases: []string{"qwen3-coder", "qwen3-coder:480b"}, Name: "Qwen3 Coder 480B-A35B", InputPricePerM: 1.5, OutputPricePerM: 7.5, ContextWindow: 204_800, MaxOutputTokens: 65_536},
	{ID: "qwen3-coder-30b-a3b-instruct", Aliases: []string{"qwen3-coder:30b", "qwen3-coder:30b-a3b"}, Name: "Qwen3 Coder 30B-A3B", InputPricePerM: 0.45, OutputPricePerM: 2.25, ContextWindow: 204_800, MaxOutputTokens: 65_536},
	{ID: "qwen3-235b-a22b-instruct-2507", Aliases: []string{"qwen3-235b-a22b-2507", "qwen3:235b-instruct"}, Name: "Qwen3 235B-A22B Instruct 2507", InputPricePerM: 0.23, OutputPricePerM: 0.92, ContextWindow: 129_000, MaxOutputTokens: 32_768},
	{ID: "qwen3-235b-a22b-thinking-2507", Aliases: []string{"qwen3:235b-thinking"}, Name: "Qwen3 235B-A22B Thinking 2507", InputPricePerM: 0.23, OutputPricePerM: 2.3, ContextWindow: 126_000, MaxOutputTokens: 32_768},
	{ID: "qwen3-vl-235b-a22b-instruct", Aliases: []string{"qwen3-vl:235b"}, Name: "Qwen3 VL 235B-A22B", InputPricePerM: 0.4, OutputPricePerM: 1.6, SupportsImages: true, ContextWindow: 129_000, MaxOutputTokens: 32_768},

	// ── Kimi (Moonshot) ─────────────────────────────────────────────────────
	// Sampling is fixed on the current models; another temperature is refused.
	// K3's output defaults to 128K; K2.x may use whatever the window leaves.
	{ID: "kimi-k3", Name: "Kimi K3", InputPricePerM: 3, OutputPricePerM: 15, CacheWritePricePerM: 3, CacheReadPricePerM: 0.3, SupportsImages: true, ContextWindow: 1_048_576, MaxOutputTokens: 131_072, RejectsSampling: true},
	{ID: "kimi-k2.7-code", Aliases: []string{"kimi-k2p7-code"}, Name: "Kimi K2.7 Code", InputPricePerM: 0.95, OutputPricePerM: 4, CacheReadPricePerM: 0.19, SupportsImages: true, ContextWindow: 262_144, RejectsSampling: true},
	{ID: "kimi-k2.7-code-highspeed", Name: "Kimi K2.7 Code Highspeed", InputPricePerM: 1.9, OutputPricePerM: 8, CacheReadPricePerM: 0.38, SupportsImages: true, ContextWindow: 262_144, RejectsSampling: true},
	{ID: "kimi-k2.6", Aliases: []string{"kimi-k2p6"}, Name: "Kimi K2.6", InputPricePerM: 0.95, OutputPricePerM: 4, CacheReadPricePerM: 0.16, SupportsImages: true, ContextWindow: 262_144, RejectsSampling: true},
	// Retired first-party; still served elsewhere (OpenRouter prices).
	{ID: "moonshotai/Kimi-K2.5", Name: "Kimi K2.5", InputPricePerM: 0.45, OutputPricePerM: 2.25, CacheReadPricePerM: 0.07, SupportsImages: true, ContextWindow: 262_144},
	{ID: "moonshotai/Kimi-K2-Thinking", Name: "Kimi K2 Thinking", InputPricePerM: 0.6, OutputPricePerM: 2.5, CacheReadPricePerM: 0.15, ContextWindow: 262_144},
	{ID: "moonshotai/Kimi-K2-Instruct-0905", Aliases: []string{"kimi-k2-0905", "kimi-k2-0905-preview"}, Name: "Kimi K2 0905", InputPricePerM: 0.6, OutputPricePerM: 2.5, ContextWindow: 262_144},
	{ID: "moonshotai/Kimi-K2-Instruct", Aliases: []string{"kimi-k2"}, Name: "Kimi K2", InputPricePerM: 0.57, OutputPricePerM: 2.3, ContextWindow: 131_072},

	// ── GLM (Z.ai) ──────────────────────────────────────────────────────────
	// 5.2 and later: 1M, which Ollama's cloud serves as 1,000,000 tokens. GLM-5.3
	// and 5.3 Flash always think.
	{ID: "glm-5.3", Aliases: []string{"glm-5p3"}, Name: "GLM-5.3", InputPricePerM: 1.4, OutputPricePerM: 4.4, CacheReadPricePerM: 0.26, ContextWindow: 1_000_000, MaxOutputTokens: 131_072},
	{ID: "glm-5.3-flash", Aliases: []string{"glm-5p3-flash"}, Name: "GLM-5.3 Flash", InputPricePerM: 0.15, OutputPricePerM: 0.5, CacheReadPricePerM: 0.03, SupportsImages: true, ContextWindow: 1_000_000, MaxOutputTokens: 131_072},
	{ID: "glm-5.3-flashx", Name: "GLM-5.3 FlashX", InputPricePerM: 0.37, OutputPricePerM: 1.25, CacheReadPricePerM: 0.075, SupportsImages: true, ContextWindow: 1_000_000, MaxOutputTokens: 131_072},
	{ID: "glm-5.2", Aliases: []string{"glm-5p2"}, Name: "GLM-5.2", InputPricePerM: 1.4, OutputPricePerM: 4.4, CacheReadPricePerM: 0.26, ContextWindow: 1_000_000, MaxOutputTokens: 131_072},
	{ID: "glm-5.1", Name: "GLM-5.1", InputPricePerM: 1.4, OutputPricePerM: 4.4, CacheReadPricePerM: 0.26, ContextWindow: 202_752, MaxOutputTokens: 131_072},
	{ID: "glm-5", Name: "GLM-5", InputPricePerM: 1, OutputPricePerM: 3.2, CacheReadPricePerM: 0.2, ContextWindow: 202_752, MaxOutputTokens: 131_072},
	{ID: "glm-4.7", Name: "GLM-4.7", InputPricePerM: 0.6, OutputPricePerM: 2.2, CacheReadPricePerM: 0.11, ContextWindow: 202_752, MaxOutputTokens: 131_072},
	// Free on Z.ai's API.
	{ID: "glm-4.7-flash", Name: "GLM-4.7 Flash", ContextWindow: 200_000, MaxOutputTokens: 131_072},
	{ID: "glm-4.6", Name: "GLM-4.6", InputPricePerM: 0.6, OutputPricePerM: 2.2, CacheReadPricePerM: 0.11, ContextWindow: 202_752, MaxOutputTokens: 131_072},
	{ID: "glm-4.5", Name: "GLM-4.5", InputPricePerM: 0.6, OutputPricePerM: 2.2, CacheReadPricePerM: 0.11, ContextWindow: 131_072, MaxOutputTokens: 98_304},
	{ID: "glm-4.5-air", Name: "GLM-4.5 Air", InputPricePerM: 0.2, OutputPricePerM: 1.1, CacheReadPricePerM: 0.03, ContextWindow: 131_072, MaxOutputTokens: 98_304},

	// ── MiniMax ─────────────────────────────────────────────────────────────
	// M3 is 1M first-party (billed double past 512K input), but MiniMax's own
	// OpenRouter endpoint, Together and Ollama's cloud all serve 512K. The M2
	// line is 204,800 first-party, 196,608 in the weights' config and on Groq.
	{ID: "MiniMax-M3", Name: "MiniMax M3", InputPricePerM: 0.3, OutputPricePerM: 1.2, CacheReadPricePerM: 0.06, SupportsImages: true, ContextWindow: 524_288},
	{ID: "MiniMax-M2.7", Name: "MiniMax M2.7", InputPricePerM: 0.3, OutputPricePerM: 1.2, CacheWritePricePerM: 0.375, CacheReadPricePerM: 0.06, ContextWindow: 196_608},
	{ID: "MiniMax-M2.7-highspeed", Name: "MiniMax M2.7 Highspeed", InputPricePerM: 0.6, OutputPricePerM: 2.4, CacheWritePricePerM: 0.375, CacheReadPricePerM: 0.06, ContextWindow: 196_608},
	{ID: "MiniMax-M2.5", Name: "MiniMax M2.5", InputPricePerM: 0.3, OutputPricePerM: 1.2, CacheWritePricePerM: 0.375, CacheReadPricePerM: 0.03, ContextWindow: 196_608},
	{ID: "MiniMax-M2.1", Name: "MiniMax M2.1", InputPricePerM: 0.3, OutputPricePerM: 1.2, CacheWritePricePerM: 0.375, CacheReadPricePerM: 0.03, ContextWindow: 196_608},
	{ID: "MiniMax-M2", Name: "MiniMax M2", InputPricePerM: 0.3, OutputPricePerM: 1.2, CacheWritePricePerM: 0.375, CacheReadPricePerM: 0.03, ContextWindow: 196_608, MaxOutputTokens: 131_072},
	{ID: "MiniMaxAI/MiniMax-M1-80k", Aliases: []string{"minimax-m1", "MiniMaxAI/MiniMax-M1-40k"}, Name: "MiniMax M1", InputPricePerM: 0.4, OutputPricePerM: 2.2, ContextWindow: 1_000_000},

	// ── Meta ────────────────────────────────────────────────────────────────
	// Meta's API now serves only its closed Muse models; these are self-hosted
	// or third-party (OpenRouter prices). Llama 4 Scout's nominal 10M window is
	// served nowhere; hosts top out near 1M.
	{ID: "meta-models/Muse-Glimmer-30B", Aliases: []string{"muse-glimmer"}, Name: "Muse Glimmer 30B", InputPricePerM: 0.3, OutputPricePerM: 1.2, CacheReadPricePerM: 0.04, SupportsImages: true, ContextWindow: 131_072},
	{ID: "meta-llama/Llama-4-Maverick-17B-128E-Instruct", Aliases: []string{"llama-4-maverick", "llama4:maverick", "llama4:128x17b", "llama4-maverick-instruct-basic"}, Name: "Llama 4 Maverick", InputPricePerM: 0.1875, OutputPricePerM: 0.6525, SupportsImages: true, ContextWindow: 1_048_576},
	{ID: "meta-llama/Llama-4-Scout-17B-16E-Instruct", Aliases: []string{"llama-4-scout", "llama4:scout", "llama4:16x17b", "llama4", "llama4-scout-instruct-basic"}, Name: "Llama 4 Scout", InputPricePerM: 0.1, OutputPricePerM: 0.3, SupportsImages: true, ContextWindow: 1_048_576},
	{ID: "meta-llama/Llama-3.3-70B-Instruct", Aliases: []string{"llama3.3:70b", "llama3.3", "llama-3.3-70b-versatile", "meta-llama/Llama-3.3-70B-Instruct-Turbo", "llama-v3p3-70b-instruct"}, Name: "Llama 3.3 70B", InputPricePerM: 0.1, OutputPricePerM: 0.32, ContextWindow: 131_072},
	{ID: "meta-llama/Llama-3.1-405B-Instruct", Aliases: []string{"llama3.1:405b", "llama-v3p1-405b-instruct"}, Name: "Llama 3.1 405B", ContextWindow: 131_072},
	{ID: "meta-llama/Llama-3.1-70B-Instruct", Aliases: []string{"Meta-Llama-3.1-70B-Instruct", "llama3.1:70b", "llama-v3p1-70b-instruct"}, Name: "Llama 3.1 70B", InputPricePerM: 0.4, OutputPricePerM: 0.4, ContextWindow: 131_072},
	{ID: "meta-llama/Llama-3.1-8B-Instruct", Aliases: []string{"Meta-Llama-3.1-8B-Instruct", "llama3.1:8b", "llama3.1", "llama-3.1-8b-instant", "llama-v3p1-8b-instruct"}, Name: "Llama 3.1 8B", InputPricePerM: 0.05, OutputPricePerM: 0.08, CacheReadPricePerM: 0.025, ContextWindow: 131_072},
	{ID: "meta-llama/Llama-3.2-3B-Instruct", Aliases: []string{"llama3.2:3b", "llama3.2"}, Name: "Llama 3.2 3B", InputPricePerM: 0.05, OutputPricePerM: 0.33, ContextWindow: 131_072},
	{ID: "meta-llama/Llama-3.2-1B-Instruct", Aliases: []string{"llama3.2:1b"}, Name: "Llama 3.2 1B", InputPricePerM: 0.027, OutputPricePerM: 0.201, ContextWindow: 131_072},
	{ID: "meta-llama/Llama-3.2-11B-Vision-Instruct", Aliases: []string{"llama3.2-vision:11b", "llama3.2-vision"}, Name: "Llama 3.2 11B Vision", SupportsImages: true, ContextWindow: 131_072},
	{ID: "meta-llama/Llama-3.2-90B-Vision-Instruct", Aliases: []string{"llama3.2-vision:90b"}, Name: "Llama 3.2 90B Vision", SupportsImages: true, ContextWindow: 131_072},

	// ── Mistral (La Plateforme) ─────────────────────────────────────────────
	// Open-weight models only (Codestral is closed). Prompt and output share
	// the window. Devstral, Magistral and Small 3.2 are retired from Mistral's
	// API; their weights are still served elsewhere. Not "mistral-medium-3":
	// Mistral points it at 3.5, OpenRouter at the retired 128K Medium 3.
	{ID: "mistral-medium-3-5", Aliases: []string{"mistralai/Mistral-Medium-3.5-128B", "mistral-medium-3.5:128b"}, Name: "Mistral Medium 3.5", InputPricePerM: 1.5, OutputPricePerM: 7.5, CacheReadPricePerM: 0.15, SupportsImages: true, ContextWindow: 262_144},
	{ID: "mistral-small-2603", Aliases: []string{"mistralai/Mistral-Small-4-119B-2603"}, Name: "Mistral Small 4", InputPricePerM: 0.15, OutputPricePerM: 0.6, CacheReadPricePerM: 0.015, SupportsImages: true, ContextWindow: 262_144},
	{ID: "mistral-large-2512", Aliases: []string{"mistralai/Mistral-Large-3-675B-Instruct-2512", "mistral-large-3:675b"}, Name: "Mistral Large 3", InputPricePerM: 0.5, OutputPricePerM: 1.5, CacheReadPricePerM: 0.05, SupportsImages: true, ContextWindow: 262_144},
	{ID: "ministral-14b-2512", Aliases: []string{"mistralai/Ministral-3-14B-Instruct-2512", "ministral-3:14b"}, Name: "Ministral 3 14B", InputPricePerM: 0.2, OutputPricePerM: 0.2, CacheReadPricePerM: 0.02, SupportsImages: true, ContextWindow: 262_144},
	{ID: "ministral-8b-2512", Aliases: []string{"mistralai/Ministral-3-8B-Instruct-2512", "ministral-3:8b", "ministral-3"}, Name: "Ministral 3 8B", InputPricePerM: 0.15, OutputPricePerM: 0.15, CacheReadPricePerM: 0.015, SupportsImages: true, ContextWindow: 262_144},
	// Mistral's own endpoint serves the 3B at 128K, not the 256K of its card.
	{ID: "ministral-3b-2512", Aliases: []string{"mistralai/Ministral-3-3B-Instruct-2512", "ministral-3:3b"}, Name: "Ministral 3 3B", InputPricePerM: 0.1, OutputPricePerM: 0.1, CacheReadPricePerM: 0.01, SupportsImages: true, ContextWindow: 131_072},
	{ID: "devstral-2512", Aliases: []string{"mistralai/Devstral-2-123B-Instruct-2512", "devstral-2:123b", "devstral-2"}, Name: "Devstral 2", InputPricePerM: 0.4, OutputPricePerM: 2, CacheReadPricePerM: 0.04, ContextWindow: 262_144},
	{ID: "mistralai/Devstral-Small-2-24B-Instruct-2512", Aliases: []string{"labs-devstral-small-2512", "devstral-small-2:24b", "devstral-small-2"}, Name: "Devstral Small 2", SupportsImages: true, ContextWindow: 262_144},
	{ID: "mistralai/Devstral-Small-2507", Aliases: []string{"devstral:24b", "devstral"}, Name: "Devstral Small 1.1", ContextWindow: 131_072},
	// Not Ollama's "magistral": that is the 40K v1.0.
	{ID: "mistralai/Magistral-Small-2509", Name: "Magistral Small 1.2", SupportsImages: true, ContextWindow: 131_072},
	{ID: "mistralai/Mistral-Small-3.2-24B-Instruct-2506", Aliases: []string{"mistral-small-2506", "mistral-small-3.2-24b-instruct", "mistral-small3.2:24b", "mistral-small3.2"}, Name: "Mistral Small 3.2", InputPricePerM: 0.09375, OutputPricePerM: 0.25, SupportsImages: true, ContextWindow: 131_072},

	// ── Google Gemma ────────────────────────────────────────────────────────
	// The Gemini API serves Gemma 4 31B and 26B free only; prices are
	// OpenRouter's. Gemma 3 has no native tool calling.
	{ID: "gemma-4-31b-it", Aliases: []string{"gemma4:31b"}, Name: "Gemma 4 31B", InputPricePerM: 0.09, OutputPricePerM: 0.34, CacheReadPricePerM: 0.05, SupportsImages: true, ContextWindow: 262_144},
	{ID: "gemma-4-26b-a4b-it", Aliases: []string{"gemma4:26b"}, Name: "Gemma 4 26B-A4B", InputPricePerM: 0.09, OutputPricePerM: 0.3, CacheReadPricePerM: 0.05, SupportsImages: true, ContextWindow: 262_144},
	{ID: "google/gemma-4-12B-it", Aliases: []string{"gemma4:12b"}, Name: "Gemma 4 12B", SupportsImages: true, ContextWindow: 262_144},
	{ID: "google/gemma-4-E4B-it", Aliases: []string{"gemma4:e4b", "gemma4"}, Name: "Gemma 4 E4B", SupportsImages: true, ContextWindow: 131_072},
	{ID: "google/gemma-4-E2B-it", Aliases: []string{"gemma4:e2b"}, Name: "Gemma 4 E2B", SupportsImages: true, ContextWindow: 131_072},
	{ID: "google/gemma-3-27b-it", Aliases: []string{"gemma3:27b"}, Name: "Gemma 3 27B", InputPricePerM: 0.08, OutputPricePerM: 0.45, CacheReadPricePerM: 0.04, SupportsImages: true, ContextWindow: 131_072},
	{ID: "google/gemma-3-12b-it", Aliases: []string{"gemma3:12b"}, Name: "Gemma 3 12B", InputPricePerM: 0.05, OutputPricePerM: 0.15, SupportsImages: true, ContextWindow: 131_072},
	{ID: "google/gemma-3-4b-it", Aliases: []string{"gemma3:4b", "gemma3"}, Name: "Gemma 3 4B", InputPricePerM: 0.05, OutputPricePerM: 0.1, SupportsImages: true, ContextWindow: 131_072},
	{ID: "google/gemma-3-1b-it", Aliases: []string{"gemma3:1b"}, Name: "Gemma 3 1B", ContextWindow: 32_768},
	// Multimodal per the card, but Ollama serves it text-only.
	{ID: "google/gemma-3n-E4B-it", Aliases: []string{"gemma3n:e4b", "gemma3n"}, Name: "Gemma 3n E4B", ContextWindow: 32_768},
	{ID: "google/gemma-3n-E2B-it", Aliases: []string{"gemma3n:e2b"}, Name: "Gemma 3n E2B", ContextWindow: 32_768},

	// ── OpenAI gpt-oss ──────────────────────────────────────────────────────
	// OpenAI lists but does not serve them; prices are OpenRouter's. Groq and
	// most hosts cap output at 64K.
	{ID: "gpt-oss-120b", Aliases: []string{"gpt-oss:120b"}, Name: "gpt-oss-120b", InputPricePerM: 0.15, OutputPricePerM: 0.6, CacheReadPricePerM: 0.075, ContextWindow: 131_072, MaxOutputTokens: 131_072},
	{ID: "gpt-oss-20b", Aliases: []string{"gpt-oss:20b", "gpt-oss"}, Name: "gpt-oss-20b", InputPricePerM: 0.018, OutputPricePerM: 0.09, ContextWindow: 131_072, MaxOutputTokens: 131_072},

	// ── NVIDIA Nemotron ─────────────────────────────────────────────────────
	// Cards allow 1M, but default configs and most hosts serve 262,144; prices
	// are OpenRouter's (NVIDIA publishes none per token).
	{ID: "nvidia/nemotron-3-ultra-550b-a55b", Aliases: []string{"nvidia/NVIDIA-Nemotron-3-Ultra-550B-A55B-BF16", "nemotron-3-ultra"}, Name: "Nemotron 3 Ultra", InputPricePerM: 0.6, OutputPricePerM: 2.4, CacheReadPricePerM: 0.12, ContextWindow: 262_144},
	{ID: "nvidia/nemotron-3-super-120b-a12b", Aliases: []string{"nvidia/NVIDIA-Nemotron-3-Super-120B-A12B-BF16", "nemotron-3-super:120b", "nemotron-3-super"}, Name: "Nemotron 3 Super", InputPricePerM: 0.08, OutputPricePerM: 0.45, ContextWindow: 262_144},
	{ID: "nvidia/nemotron-3-nano-30b-a3b", Aliases: []string{"nvidia/nemotron-nano-3-30b-a3b", "nvidia/NVIDIA-Nemotron-3-Nano-30B-A3B-BF16", "nemotron-3-nano:30b", "nemotron-3-nano"}, Name: "Nemotron 3 Nano 30B", InputPricePerM: 0.05, OutputPricePerM: 0.2, CacheReadPricePerM: 0.03, ContextWindow: 262_144},
	{ID: "nvidia/NVIDIA-Nemotron-3-Nano-4B-BF16", Aliases: []string{"nemotron-3-nano:4b"}, Name: "Nemotron 3 Nano 4B", ContextWindow: 262_144},
	{ID: "nvidia/nemotron-3.5-lightning-30b-a3b", Aliases: []string{"nemotron-3.5-lightning", "nemotron-3.5-lightning:30b"}, Name: "Nemotron 3.5 Lightning", InputPricePerM: 0.08, OutputPricePerM: 0.2, CacheReadPricePerM: 0.04, ContextWindow: 262_144},
	{ID: "nvidia/nemotron-3-nano-omni-30b-a3b-reasoning", Aliases: []string{"nemotron3:33b"}, Name: "Nemotron 3 Nano Omni", SupportsImages: true, ContextWindow: 131_072},

	// ── Microsoft Phi ───────────────────────────────────────────────────────
	// Azure AI Foundry prices. Foundry lists no tool calling for any Phi-4.
	{ID: "microsoft/phi-4", Aliases: []string{"phi4:14b", "phi4"}, Name: "Phi-4", InputPricePerM: 0.125, OutputPricePerM: 0.5, ContextWindow: 16_384, MaxOutputTokens: 16_384},
	{ID: "microsoft/Phi-4-reasoning", Aliases: []string{"phi4-reasoning:14b", "phi4-reasoning"}, Name: "Phi-4 Reasoning", InputPricePerM: 0.125, OutputPricePerM: 0.5, ContextWindow: 32_768, MaxOutputTokens: 32_768},
	{ID: "microsoft/Phi-4-reasoning-plus", Aliases: []string{"phi4-reasoning:plus"}, Name: "Phi-4 Reasoning Plus", InputPricePerM: 0.125, OutputPricePerM: 0.5, ContextWindow: 32_768},
	{ID: "microsoft/Phi-4-mini-instruct", Aliases: []string{"phi4-mini:3.8b", "phi4-mini"}, Name: "Phi-4 Mini", InputPricePerM: 0.075, OutputPricePerM: 0.3, ContextWindow: 131_072},
	{ID: "microsoft/Phi-4-multimodal-instruct", Name: "Phi-4 Multimodal", InputPricePerM: 0.08, OutputPricePerM: 0.32, SupportsImages: true, ContextWindow: 131_072},
	{ID: "microsoft/Phi-4-mini-reasoning", Aliases: []string{"phi4-mini-reasoning:3.8b", "phi4-mini-reasoning"}, Name: "Phi-4 Mini Reasoning", InputPricePerM: 0.075, OutputPricePerM: 0.3, ContextWindow: 128_000, MaxOutputTokens: 128_000},
	{ID: "microsoft/Phi-4-reasoning-vision-15B", Name: "Phi-4 Reasoning Vision", SupportsImages: true, ContextWindow: 16_384},
}

// LegacyModels are models retired from their vendor's own API that other hosts
// still serve (OpenRouter, Bedrock, Vertex). They are catalogue-only: no
// provider lists them, so nobody picks a model the first-party API would
// reject, but a host that does serve one still gets its window and prices.
// Output ceilings are not restated on current official pages, so they are 0.
var LegacyModels = []CatalogModel{
	// Retired on the Claude API 2026-08-05 / 2026-06-15; prices from the Claude
	// pricing page, which still lists them for other platforms.
	{ID: "claude-opus-4-1-20250805", Aliases: []string{"claude-opus-4-1"}, Name: "Claude Opus 4.1", InputPricePerM: 15, OutputPricePerM: 75, CacheWritePricePerM: 18.75, CacheReadPricePerM: 1.5, SupportsImages: true, ContextWindow: 200_000},
	{ID: "claude-opus-4-20250514", Aliases: []string{"claude-opus-4"}, Name: "Claude Opus 4", InputPricePerM: 15, OutputPricePerM: 75, CacheWritePricePerM: 18.75, CacheReadPricePerM: 1.5, SupportsImages: true, ContextWindow: 200_000},
	{ID: "claude-sonnet-4-20250514", Aliases: []string{"claude-sonnet-4"}, Name: "Claude Sonnet 4", InputPricePerM: 3, OutputPricePerM: 15, CacheWritePricePerM: 3.75, CacheReadPricePerM: 0.3, SupportsImages: true, ContextWindow: 200_000},
	{ID: "claude-3-5-haiku-20241022", Aliases: []string{"claude-3-5-haiku"}, Name: "Claude Haiku 3.5", InputPricePerM: 0.8, OutputPricePerM: 4, CacheWritePricePerM: 1, CacheReadPricePerM: 0.08, SupportsImages: true, ContextWindow: 200_000},

	// Shut down on OpenAI's API 2026-07-23; OpenRouter still lists them. 400K
	// window, of which 272K may be input.
	{ID: "gpt-5.2-codex", Name: "GPT-5.2 Codex", InputPricePerM: 1.75, OutputPricePerM: 14, CacheWritePricePerM: 1.75, CacheReadPricePerM: 0.175, SupportsImages: true, ContextWindow: 272_000},
	{ID: "gpt-5.1-codex", Name: "GPT-5.1 Codex", InputPricePerM: 1.25, OutputPricePerM: 10, CacheWritePricePerM: 1.25, CacheReadPricePerM: 0.125, SupportsImages: true, ContextWindow: 272_000},
	{ID: "gpt-5.1-codex-max", Name: "GPT-5.1 Codex Max", InputPricePerM: 1.25, OutputPricePerM: 10, CacheWritePricePerM: 1.25, CacheReadPricePerM: 0.125, SupportsImages: true, ContextWindow: 272_000},
	{ID: "gpt-5.1-codex-mini", Name: "GPT-5.1 Codex Mini", InputPricePerM: 0.25, OutputPricePerM: 2, CacheWritePricePerM: 0.25, CacheReadPricePerM: 0.025, SupportsImages: true, ContextWindow: 272_000},
}
