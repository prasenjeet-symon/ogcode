package provider

import (
	"slices"
	"strings"
	"sync"
)

// Reasoning effort over the OpenAI-compatible protocol (see effort.go for the
// vocabulary). One OpenAIProvider serves OpenAI, OpenRouter, Ollama, OGX and
// every vendor a user points the OpenAI slot at, and they agree on almost
// nothing here: OpenAI and Gemini take reasoning_effort but each model its own
// set; OpenRouter wants a reasoning object; DeepSeek, Kimi and GLM take
// reasoning_effort for their graded models and a thinking switch for the rest;
// Qwen Cloud takes enable_thinking. Most of them answer a value they do not
// know with a 400, which fails the turn — so a level is only ever sent to a
// host this file knows, for a model whose levels it knows, in that host's own
// field. Everything else gets no effort picker at all.

// effortHost is the endpoint family a provider talks to, as far as effort goes
// — and the reasoning it takes back (see openai_reasoning.go).
type effortHost int

const (
	hostUnknown effortHost = iota
	hostOpenAI
	hostGemini
	hostOpenRouter
	// hostOllama is Ollama's OpenAI compatibility layer, wherever it runs: a
	// local daemon, ollama.com, a relay — and OGX, whose gateway forwards the
	// body to Ollama's cloud verbatim.
	hostOllama
	// The model vendors' own APIs.
	hostDeepSeek
	hostZAI
	hostMoonshot
	hostMiniMax
	hostQwen
	// Inference hosts serving open models.
	hostGroq
	hostCerebras
	hostTogether
	hostFireworks
	hostSambaNova
	hostNVIDIA
)

// effortHost names the host from the provider id, then the base URL.
func (p *OpenAIProvider) effortHost() effortHost {
	switch p.id {
	case "openrouter":
		return hostOpenRouter
	case "ollama", OGXProviderID:
		return hostOllama
	}
	u := strings.ToLower(p.baseURL)
	switch {
	case strings.Contains(u, "openrouter.ai"):
		return hostOpenRouter
	case strings.Contains(u, "api.openai.com"):
		return hostOpenAI
	case strings.Contains(u, "generativelanguage.googleapis.com"):
		return hostGemini
	case strings.Contains(u, "ollama.com"), strings.Contains(u, ":11434"):
		return hostOllama
	case strings.Contains(u, "deepseek.com"):
		return hostDeepSeek
	case strings.Contains(u, "api.z.ai"), strings.Contains(u, "bigmodel.cn"):
		return hostZAI
	case strings.Contains(u, "moonshot.ai"), strings.Contains(u, "moonshot.cn"):
		return hostMoonshot
	case strings.Contains(u, "minimax.io"), strings.Contains(u, "minimaxi.com"):
		return hostMiniMax
	case strings.Contains(u, "dashscope"), strings.Contains(u, "qwencloud"):
		return hostQwen
	case strings.Contains(u, "groq.com"):
		return hostGroq
	case strings.Contains(u, "cerebras.ai"):
		return hostCerebras
	case strings.Contains(u, "together.xyz"), strings.Contains(u, "together.ai"):
		return hostTogether
	case strings.Contains(u, "fireworks.ai"):
		return hostFireworks
	case strings.Contains(u, "sambanova"):
		return hostSambaNova
	case strings.Contains(u, "integrate.api.nvidia.com"):
		return hostNVIDIA
	}
	// Mistral is left out on purpose: with reasoning on, its streamed content
	// turns from a string into an array of thinking and text chunks, which this
	// client does not read — a level there would break the reply it asked for.
	return hostUnknown
}

// openAIToolsNote explains the picker's absence on GPT-5.4 and later, which a
// user would otherwise read as a bug: those models reason with tools only
// through the Responses API, and every agent step carries tools.
const openAIToolsNote = "OpenAI lets this model reason while it uses tools only through its Responses API. ogcode talks to OpenAI over Chat Completions, so agent turns run with reasoning off."

// EffortSpec reports the effort levels a model takes through this endpoint.
func (p *OpenAIProvider) EffortSpec(model string) EffortSpec {
	spec, _ := p.effortPlan(model)
	return spec
}

// effortWriter puts a level the spec allows into a request body.
type effortWriter func(body *oaiRequest, level string)

// effortPlan is the single source for both what the picker offers and how a
// chosen level is sent, so the two cannot disagree.
func (p *OpenAIProvider) effortPlan(model string) (EffortSpec, effortWriter) {
	host := p.effortHost()
	switch host {
	case hostOpenAI:
		m, ok := openAICatalogModel(model)
		if !ok || len(m.Efforts) == 0 {
			return EffortSpec{}, nil
		}
		if m.ToolsNeedNoReasoning {
			return EffortSpec{Note: openAIToolsNote}, nil
		}
		return catalogEffortSpec(m), writeReasoningEffort

	case hostGemini:
		m, ok := googleCatalogModel(model)
		if !ok {
			return EffortSpec{}, nil
		}
		return catalogEffortSpec(m), writeReasoningEffort

	case hostOpenRouter:
		// OpenRouter reports every model's levels in its own listing; the
		// catalogue cannot know which of them a given route takes.
		info, ok := p.listedModel(model)
		if !ok || len(info.Efforts) < 2 {
			return EffortSpec{}, nil
		}
		spec := EffortSpec{Levels: slices.Clone(info.Efforts), Default: info.DefaultEffort}
		// OpenRouter reports a default it does not apply: with no effort it
		// sends none, and the vendor's own default runs (medium on Claude Opus
		// 5.5, where OpenRouter says high). Where the catalogue knows the
		// vendor's default, that is the one to show.
		if cm, ok := LookupCatalogModel(model); ok && slices.Contains(spec.Levels, cm.DefaultEffort) {
			spec.Default = cm.DefaultEffort
		}
		return spec, writeOpenRouterReasoning(spec)

	case hostOllama:
		// Ollama publishes each model's thinking values in /api/show, and its
		// cloud rejects a value a model does not list, so the host's own list
		// wins. The catalogue covers what has no metadata: OGX (its gateway
		// serves no /api/show), an older daemon, a relay.
		if info, ok := p.listedModel(model); ok && len(info.Efforts) > 0 {
			// One value is the host saying there is no choice: the model never
			// thinks, or always thinks at one depth. That overrules the
			// catalogue, which describes the model, not this host.
			if len(info.Efforts) < 2 {
				return EffortSpec{}, nil
			}
			spec := EffortSpec{Levels: slices.Clone(info.Efforts), Default: info.DefaultEffort}
			return spec, writeOllamaEffort
		}
		m, ok := LookupCatalogModel(model)
		if !ok || len(m.Efforts) == 0 {
			return EffortSpec{}, nil
		}
		spec := catalogEffortSpec(m)
		if d, ok := ollamaCloudDefaults[normalizeModelID(m.ID)]; ok {
			spec.Default = d
		}
		return spec, writeOllamaEffort

	case hostDeepSeek, hostZAI, hostMoonshot, hostMiniMax, hostQwen:
		return vendorEffortPlan(host, model)

	case hostGroq, hostCerebras, hostTogether, hostFireworks, hostSambaNova, hostNVIDIA:
		return inferenceHostEffortPlan(host, model)
	}
	return EffortSpec{}, nil
}

// vendorEffortPlan covers the model vendors' own APIs. Each takes graded
// levels in reasoning_effort and switches on/off models with its own field, and
// each is trusted only with its own models: a vendor that also hosts someone
// else's (Qwen Cloud serves Kimi K3) maps their levels its own way.
func vendorEffortPlan(host effortHost, model string) (EffortSpec, effortWriter) {
	family := map[effortHost]string{
		hostDeepSeek: "deepseek",
		hostZAI:      "glm",
		hostMoonshot: "kimi",
		hostMiniMax:  "minimax",
		hostQwen:     "qwen",
	}[host]
	norm := normalizeModelID(model)
	if !strings.HasPrefix(norm, family) {
		return EffortSpec{}, nil
	}

	var spec EffortSpec
	if m, ok := LookupCatalogModel(model); ok {
		spec = catalogEffortSpec(m)
	} else if host == hostQwen {
		// Qwen Cloud's own names (qwen3.8-max, qwen3.6-plus) are not in the
		// catalogue, which lists the open weights; the generation decides.
		spec = qwenCloudEffortSpec(norm)
	}
	if len(spec.Levels) == 0 {
		return EffortSpec{}, nil
	}

	onOff := slices.Equal(spec.Levels, onOffEfforts)
	switch {
	case !onOff && host == hostQwen:
		return spec, writeQwenEffort
	case !onOff:
		return spec, writeReasoningEffort
	case host == hostQwen:
		return spec, writeEnableThinking
	case host == hostMiniMax:
		return spec, writeThinkingSwitch("adaptive")
	default: // DeepSeek, Z.ai, Moonshot
		return spec, writeThinkingSwitch("enabled")
	}
}

// hostEffortRule is what one inference host documents for one model family:
// the levels it takes in reasoning_effort, its default, and — where the host's
// names differ from ogcode's — the writer that translates them.
type hostEffortRule struct {
	// models are normalised ids (see normalizeModelID); an id matches a rule
	// when it equals one of them or starts with one followed by "-".
	models []string
	levels []string
	def    string
	write  effortWriter // nil: the level goes into reasoning_effort as is
}

// inferenceHostRules holds each host's documented behaviour, checked against
// its own API docs on 2026-10-07. Only what a host documents is here: Groq and
// Fireworks reject a value outside a model's set with a 400, so a guess costs a
// failed turn, and a model a host does not document gets no picker.
var inferenceHostRules = map[effortHost][]hostEffortRule{
	hostGroq: {
		{models: []string{"gpt-oss"}, levels: lowToHighEfforts, def: EffortMedium},
		// Groq calls Qwen3.8's top level "high".
		{models: []string{"qwen3-8"}, levels: qwen38Efforts, write: writeXHighAsHigh},
	},
	hostCerebras: {
		{models: []string{"gpt-oss"}, levels: lowToHighEfforts, def: EffortMedium},
		{models: []string{"qwen-3-8", "qwen3-8"}, levels: qwen38Efforts, def: EffortXHigh, write: writeXHighAsHigh},
		// Every level but "none" turns Gemma 4's thinking on.
		{models: []string{"gemma-4"}, levels: onOffEfforts, def: EffortNone, write: writeOnOffAsEffort},
	},
	hostTogether: {
		{models: []string{"gpt-oss"}, levels: lowToHighEfforts, def: EffortMedium},
		{models: []string{"kimi-k3"}, levels: lowHighMaxEfforts, def: EffortMax},
		{models: []string{"glm-5-3"}, levels: lowHighMaxEfforts},
	},
	hostFireworks: {
		{models: []string{"gpt-oss"}, levels: lowToHighEfforts, def: EffortMedium},
		{models: []string{"qwen3p8", "qwen3-8"}, levels: qwen38Efforts, def: EffortXHigh},
		{models: []string{"deepseek-v4p1", "deepseek-v4-1"}, levels: noneLowHighMaxEfforts, def: EffortHigh},
		{models: []string{"deepseek-v4"}, levels: noneLowHighMaxEfforts, def: EffortLow},
		{models: []string{"glm-5p2", "glm-5-2"}, levels: noneHighMaxEfforts, def: EffortMax},
		{models: []string{"kimi-k3"}, levels: lowHighMaxEfforts, def: EffortMax},
	},
	hostSambaNova: {
		{models: []string{"gpt-oss"}, levels: lowToHighEfforts},
	},
	hostNVIDIA: {
		{models: []string{"gpt-oss"}, levels: lowToHighEfforts, def: EffortMedium},
		{models: []string{"kimi-k3"}, levels: lowHighMaxEfforts, def: EffortMax},
		{models: []string{"glm-5-3"}, levels: lowHighMaxEfforts, def: EffortMax},
	},
}

// inferenceHostEffortPlan looks the model up in its host's rules.
func inferenceHostEffortPlan(host effortHost, model string) (EffortSpec, effortWriter) {
	norm := normalizeModelID(model)
	for _, r := range inferenceHostRules[host] {
		if !slices.ContainsFunc(r.models, func(m string) bool { return norm == m || strings.HasPrefix(norm, m+"-") }) {
			continue
		}
		write := r.write
		if write == nil {
			write = writeReasoningEffort
		}
		return EffortSpec{Levels: slices.Clone(r.levels), Default: r.def}, write
	}
	return EffortSpec{}, nil
}

// qwenCloudEffortSpec is Qwen Cloud's documented behaviour by generation, for
// the ids it serves that the catalogue does not list.
func qwenCloudEffortSpec(norm string) EffortSpec {
	switch {
	case strings.HasPrefix(norm, "qwen3-8-2-4t"):
		return EffortSpec{Levels: slices.Clone(qwen38ThinkingEfforts), Default: EffortXHigh}
	case strings.HasPrefix(norm, "qwen3-8"):
		return EffortSpec{Levels: slices.Clone(qwen38Efforts), Default: EffortXHigh}
	case strings.HasPrefix(norm, "qwen3-6"), strings.HasPrefix(norm, "qwen3-5"):
		return EffortSpec{Levels: slices.Clone(onOffEfforts), Default: EffortOn}
	}
	return EffortSpec{}
}

// ollamaCloudDefaults are the defaults Ollama's cloud applies where they differ
// from the vendor's (ollama.com/api/show, 2026-10-07). They matter only where
// no live metadata is at hand — OGX — so the picker shows what actually runs.
var ollamaCloudDefaults = map[string]string{
	normalizeModelID("deepseek-v4-pro"): EffortLow,
	normalizeModelID("glm-5.2"):         EffortHigh,
}

// The writers. Each is handed only levels the plan's spec allows.

func writeReasoningEffort(body *oaiRequest, level string) {
	body.ReasoningEffort = level
}

// writeOllamaEffort sends reasoning_effort, which Ollama maps onto `think`:
// "none" is false, and any level turns an on/off model's thinking on — "high"
// is the value every Ollama version accepts for that.
func writeOllamaEffort(body *oaiRequest, level string) {
	if level == EffortOn {
		level = EffortHigh
	}
	body.ReasoningEffort = level
}

// writeOnOffAsEffort switches an on/off model through reasoning_effort, on a
// host where "none" turns thinking off and any level turns it on.
func writeOnOffAsEffort(body *oaiRequest, level string) {
	if level == EffortOn {
		level = EffortHigh
	}
	body.ReasoningEffort = level
}

// writeXHighAsHigh is for hosts that name Qwen3.8's top level "high".
func writeXHighAsHigh(body *oaiRequest, level string) {
	if level == EffortXHigh {
		level = EffortHigh
	}
	body.ReasoningEffort = level
}

// writeThinkingSwitch is the thinking: {type} switch of DeepSeek, Kimi and GLM
// ("enabled"/"disabled") and MiniMax ("adaptive"/"disabled").
func writeThinkingSwitch(on string) effortWriter {
	return func(body *oaiRequest, level string) {
		state := "disabled"
		if level != EffortNone {
			state = on
		}
		body.Thinking = &oaiThinkingSwitch{Type: state}
	}
}

// writeEnableThinking is Qwen Cloud's switch for the on/off models.
func writeEnableThinking(body *oaiRequest, level string) {
	on := level != EffortNone
	body.EnableThinking = &on
}

// writeQwenEffort covers Qwen3.8, which takes graded levels in
// reasoning_effort once thinking is on, and is turned off with the switch.
func writeQwenEffort(body *oaiRequest, level string) {
	on := level != EffortNone
	body.EnableThinking = &on
	if on {
		body.ReasoningEffort = level
	}
}

// writeOpenRouterReasoning sends OpenRouter's reasoning object: a level for a
// graded model, the enabled switch for one that only turns thinking on or off.
func writeOpenRouterReasoning(spec EffortSpec) effortWriter {
	onOff := slices.Equal(spec.Levels, onOffEfforts)
	return func(body *oaiRequest, level string) {
		switch {
		case onOff:
			on := level == EffortOn
			body.Reasoning = &oaiReasoning{Enabled: &on}
		default:
			body.Reasoning = &oaiReasoning{Effort: level}
		}
		// reasoning_effort is OpenRouter's shorthand for the same setting, and
		// the two together with different values are a 400 — the OpenAI shaping
		// may have set it for a model OpenRouter serves.
		body.ReasoningEffort = ""
	}
}

// applyEffort writes the requested level into body when the endpoint takes it
// for the model. A request that is not the agent loop's (no Thinking) never
// carries effort, and a level the model does not take is dropped rather than
// sent: it would fail the request on most hosts.
func (p *OpenAIProvider) applyEffort(body *oaiRequest, req StreamRequest) {
	if !req.Thinking || req.Effort == "" {
		return
	}
	spec, write := p.effortPlan(body.Model)
	if write == nil || !spec.Allows(req.Effort) {
		return
	}
	write(body, req.Effort)
}

// listedModel finds a model in the provider's fetched catalogue.
func (p *OpenAIProvider) listedModel(id string) (ModelInfo, bool) {
	p.modelsMu.Lock()
	defer p.modelsMu.Unlock()
	for _, m := range p.cachedModels {
		if m.ID == id {
			return m, true
		}
	}
	return ModelInfo{}, false
}

var (
	googleCatalogOnce sync.Once
	googleCatalogIdx  catalogIndex
)

// googleCatalogModel looks up a Gemini model. Only Google's own entries
// answer: Gemma on the Gemini API takes thinking differently, and its mapping
// through the compatible endpoint is undocumented.
func googleCatalogModel(id string) (CatalogModel, bool) {
	googleCatalogOnce.Do(func() {
		googleCatalogIdx = newCatalogIndex(GoogleModels)
	})
	return googleCatalogIdx.lookup(id)
}

// oaiReasoning is OpenRouter's reasoning control: a level, or the switch for
// models without levels.
type oaiReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Enabled *bool  `json:"enabled,omitempty"`
}

// oaiThinkingSwitch is the thinking: {type} object DeepSeek, Kimi, GLM and
// MiniMax take on their own APIs.
type oaiThinkingSwitch struct {
	Type string `json:"type"`
}
