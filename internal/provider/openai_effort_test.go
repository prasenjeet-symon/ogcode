package provider

import (
	"encoding/json"
	"slices"
	"testing"
)

func effortProvider(id, baseURL string, listed ...ModelInfo) *OpenAIProvider {
	return &OpenAIProvider{id: id, apiKey: "k", baseURL: baseURL, cachedModels: listed}
}

func effortRequest(model, effort string, thinking bool) StreamRequest {
	return StreamRequest{
		Model:    model,
		Messages: []ModelMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
		Tools:    shapeTool,
		Thinking: thinking,
		Effort:   effort,
	}
}

func TestEffortHostDetection(t *testing.T) {
	cases := []struct {
		id, base string
		want     effortHost
	}{
		{"openai", "https://api.openai.com/v1", hostOpenAI},
		{"openai", "https://generativelanguage.googleapis.com/v1beta/openai/", hostGemini},
		{"openrouter", "https://openrouter.ai/api/v1", hostOpenRouter},
		{"openai", "https://openrouter.ai/api/v1", hostOpenRouter},
		{"ollama", "http://localhost:11434/v1", hostOllama},
		{"ollama", "http://10.0.0.5:8090/v1", hostOllama},
		{OGXProviderID, "https://ogx.ogcode.in/v1", hostOllama},
		{"openai", "https://ollama.com/v1", hostOllama},
		{"openai", "https://api.deepseek.com/v1", hostDeepSeek},
		{"openai", "https://api.z.ai/api/paas/v4", hostZAI},
		{"openai", "https://api.moonshot.ai/v1", hostMoonshot},
		{"openai", "https://api.minimax.io/v1", hostMiniMax},
		{"openai", "https://dashscope-intl.aliyuncs.com/compatible-mode/v1", hostQwen},
		{"openai", "https://api.groq.com/openai/v1", hostGroq},
		{"openai", "https://api.fireworks.ai/inference/v1", hostFireworks},
		// Mistral turns content into chunk arrays with reasoning on; left out.
		{"openai", "https://api.mistral.ai/v1", hostUnknown},
		{"openai", "http://localhost:1234/v1", hostUnknown},
	}
	for _, c := range cases {
		if got := effortProvider(c.id, c.base).effortHost(); got != c.want {
			t.Errorf("%s %s: host %v, want %v", c.id, c.base, got, c.want)
		}
	}
}

func TestOpenAICompatibleEffortSpecs(t *testing.T) {
	cases := []struct {
		name   string
		p      *OpenAIProvider
		model  string
		levels []string
		deflt  string
		note   bool
	}{
		{"GPT-5.2 reasons with tools on Chat Completions", effortProvider("openai", "https://api.openai.com/v1"), "gpt-5.2", noneToXHighEfforts, EffortNone, false},
		{"GPT-5 always reasons", effortProvider("openai", "https://api.openai.com/v1"), "gpt-5-mini", minimalToHighEfforts, EffortMedium, false},
		{"o-series", effortProvider("openai", "https://api.openai.com/v1"), "o3", lowToHighEfforts, EffortMedium, false},
		// GPT-5.4 and later: no levels over Chat Completions, and a note saying why.
		{"GPT-6 Sol", effortProvider("openai", "https://api.openai.com/v1"), "gpt-6-sol", nil, "", true},
		{"a model that does not reason", effortProvider("openai", "https://api.openai.com/v1"), "gpt-4.1", nil, "", false},
		{"Gemini 3.8 Flash", effortProvider("openai", "https://generativelanguage.googleapis.com/v1beta/openai/"), "models/gemini-3.8-flash", lowToHighEfforts, EffortMedium, false},
		{"Gemini 2.5 Flash can turn thinking off", effortProvider("openai", "https://generativelanguage.googleapis.com/v1beta/openai/"), "gemini-2.5-flash", noneToHighEfforts, "", false},
		{"Gemma on the Gemini API is not Gemini", effortProvider("openai", "https://generativelanguage.googleapis.com/v1beta/openai/"), "gemma-4-31b-it", nil, "", false},
		{"DeepSeek's own API", effortProvider("openai", "https://api.deepseek.com/v1"), "deepseek-flash", noneLowHighMaxEfforts, EffortHigh, false},
		{"another vendor's model on DeepSeek's API", effortProvider("openai", "https://api.deepseek.com/v1"), "kimi-k3", nil, "", false},
		{"GLM-5.1 switches on Z.ai", effortProvider("openai", "https://api.z.ai/api/paas/v4"), "glm-5.1", onOffEfforts, EffortOn, false},
		{"Qwen Cloud's own 3.8 name", effortProvider("openai", "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"), "qwen3.8-max", qwen38Efforts, EffortXHigh, false},
		{"Qwen Cloud's 3.6", effortProvider("openai", "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"), "qwen3.6-plus", onOffEfforts, EffortOn, false},
		{"gpt-oss on Groq", effortProvider("openai", "https://api.groq.com/openai/v1"), "openai/gpt-oss-120b", lowToHighEfforts, EffortMedium, false},
		{"Llama on Groq takes no effort", effortProvider("openai", "https://api.groq.com/openai/v1"), "llama-3.3-70b-versatile", nil, "", false},
		{"Fireworks spells V4.1 its own way", effortProvider("openai", "https://api.fireworks.ai/inference/v1"), "accounts/fireworks/models/deepseek-v4p1-flash", noneLowHighMaxEfforts, EffortHigh, false},
		{"Fireworks V4 defaults low", effortProvider("openai", "https://api.fireworks.ai/inference/v1"), "accounts/fireworks/models/deepseek-v4-pro", noneLowHighMaxEfforts, EffortLow, false},
		{"an unknown host is never sent effort", effortProvider("openai", "http://localhost:1234/v1"), "gpt-oss-20b", nil, "", false},
		// OGX: no live metadata, so the catalogue answers, with Ollama's cloud
		// default where it differs from the vendor's.
		{"OGX falls back to the catalogue", effortProvider(OGXProviderID, "https://ogx.ogcode.in/v1"), "gpt-oss:120b", lowToHighEfforts, EffortMedium, false},
		{"OGX shows the default Ollama's cloud applies", effortProvider(OGXProviderID, "https://ogx.ogcode.in/v1"), "deepseek-v4-pro", noneLowHighMaxEfforts, EffortLow, false},
		{"an Ollama model with no ladder anywhere", effortProvider("ollama", "http://localhost:11434/v1"), "llama3.2:3b", nil, "", false},
	}
	for _, c := range cases {
		spec := c.p.EffortSpec(c.model)
		if !slices.Equal(spec.Levels, c.levels) {
			t.Errorf("%s: levels %v, want %v", c.name, spec.Levels, c.levels)
		}
		if spec.Default != c.deflt {
			t.Errorf("%s: default %q, want %q", c.name, spec.Default, c.deflt)
		}
		if (spec.Note != "") != c.note {
			t.Errorf("%s: note %q", c.name, spec.Note)
		}
	}
}

// A host that reports a model's levels is believed over the catalogue — it is
// the one that will reject a value it does not list.
func TestListedLevelsWinOverTheCatalogue(t *testing.T) {
	t.Run("Ollama metadata replaces the catalogue's ladder", func(t *testing.T) {
		p := effortProvider("ollama", "http://localhost:11434/v1",
			ModelInfo{ID: "glm-5.2:cloud", Efforts: []string{EffortNone, EffortHigh, EffortMax}, DefaultEffort: EffortHigh})
		spec := p.EffortSpec("glm-5.2:cloud")
		if !slices.Equal(spec.Levels, noneHighMaxEfforts) || spec.Default != EffortHigh {
			t.Errorf("got %+v", spec)
		}
	})
	t.Run("one reported value means no choice, even for a catalogued model", func(t *testing.T) {
		p := effortProvider("ollama", "http://localhost:11434/v1",
			ModelInfo{ID: "gpt-oss:20b", Efforts: []string{EffortNone}, DefaultEffort: EffortNone})
		if spec := p.EffortSpec("gpt-oss:20b"); len(spec.Levels) != 0 {
			t.Errorf("a model the host says never thinks got levels %v", spec.Levels)
		}
	})
	t.Run("OpenRouter's listing, with the vendor's real default", func(t *testing.T) {
		p := effortProvider("openrouter", "https://openrouter.ai/api/v1",
			ModelInfo{ID: "anthropic/claude-opus-5.5", Efforts: claudeEfforts, DefaultEffort: EffortHigh})
		spec := p.EffortSpec("anthropic/claude-opus-5.5")
		if !slices.Equal(spec.Levels, claudeEfforts) {
			t.Errorf("levels %v", spec.Levels)
		}
		// OpenRouter says high but sends nothing, so Anthropic's medium runs.
		if spec.Default != EffortMedium {
			t.Errorf("default %q, want the vendor's medium", spec.Default)
		}
	})
	t.Run("OpenRouter without a listing offers nothing", func(t *testing.T) {
		p := effortProvider("openrouter", "https://openrouter.ai/api/v1")
		if spec := p.EffortSpec("anthropic/claude-opus-5.5"); len(spec.Levels) != 0 {
			t.Errorf("got %v from an empty listing", spec.Levels)
		}
	})
}

// The wire body is what each host sees: its own field, only on agent steps,
// only at a level it takes.
func TestOpenAICompatibleEffortOnTheWire(t *testing.T) {
	wire := func(p *OpenAIProvider, req StreamRequest) map[string]any {
		t.Helper()
		body, _ := captureWireRequest(t, p, req)
		return body
	}

	t.Run("OpenAI takes the level in reasoning_effort", func(t *testing.T) {
		body := wire(effortProvider("openai", "https://api.openai.com/v1"), effortRequest("gpt-5.2", EffortXHigh, true))
		if body["reasoning_effort"] != EffortXHigh {
			t.Errorf("reasoning_effort = %v", body["reasoning_effort"])
		}
	})
	t.Run("a tool step on GPT-5.4+ stays at none whatever was chosen", func(t *testing.T) {
		body := wire(effortProvider("openai", "https://api.openai.com/v1"), effortRequest("gpt-6-sol", EffortHigh, true))
		if body["reasoning_effort"] != EffortNone {
			t.Errorf("reasoning_effort = %v, want none: anything else is a 400 with tools", body["reasoning_effort"])
		}
	})
	t.Run("utility calls carry no effort", func(t *testing.T) {
		p := effortProvider("ollama", "http://localhost:11434/v1")
		body := wire(p, effortRequest("gpt-oss:20b", EffortHigh, false))
		if _, ok := body["reasoning_effort"]; ok {
			t.Errorf("reasoning_effort sent on a utility call: %v", body["reasoning_effort"])
		}
	})
	t.Run("a level the model does not take is dropped", func(t *testing.T) {
		body := wire(effortProvider("openai", "https://api.openai.com/v1"), effortRequest("gpt-5.1", EffortXHigh, true))
		if got, ok := body["reasoning_effort"]; ok {
			t.Errorf("GPT-5.1 has no xhigh; sent %v", got)
		}
	})
	t.Run("Ollama turns an on/off model on with high", func(t *testing.T) {
		p := effortProvider("ollama", "http://localhost:11434/v1",
			ModelInfo{ID: "kimi-k2.6:cloud", Efforts: onOffEfforts, DefaultEffort: EffortOn})
		if body := wire(p, effortRequest("kimi-k2.6:cloud", EffortOn, true)); body["reasoning_effort"] != EffortHigh {
			t.Errorf("on: reasoning_effort = %v, want high", body["reasoning_effort"])
		}
		if body := wire(p, effortRequest("kimi-k2.6:cloud", EffortNone, true)); body["reasoning_effort"] != EffortNone {
			t.Errorf("off: reasoning_effort = %v, want none", body["reasoning_effort"])
		}
	})
	t.Run("OpenRouter takes a reasoning object, never both forms", func(t *testing.T) {
		p := effortProvider("openai", "https://openrouter.ai/api/v1",
			ModelInfo{ID: "openai/gpt-5.2", Efforts: noneToXHighEfforts},
			ModelInfo{ID: "z-ai/glm-4.6", Efforts: onOffEfforts})
		body := wire(p, effortRequest("openai/gpt-5.2", EffortHigh, true))
		r, _ := body["reasoning"].(map[string]any)
		if r["effort"] != EffortHigh {
			t.Errorf("reasoning = %v, want effort high", body["reasoning"])
		}
		if _, ok := body["reasoning_effort"]; ok {
			t.Errorf("reasoning_effort beside reasoning is a 400 on OpenRouter: %v", body["reasoning_effort"])
		}
		body = wire(p, effortRequest("z-ai/glm-4.6", EffortNone, true))
		r, _ = body["reasoning"].(map[string]any)
		if r["enabled"] != false {
			t.Errorf("an on/off model is switched with enabled; got %v", body["reasoning"])
		}
	})
	t.Run("Z.ai switches GLM-5.1 with thinking.type", func(t *testing.T) {
		body := wire(effortProvider("openai", "https://api.z.ai/api/paas/v4"), effortRequest("glm-5.1", EffortNone, true))
		th, _ := body["thinking"].(map[string]any)
		if th["type"] != "disabled" {
			t.Errorf("thinking = %v, want type disabled", body["thinking"])
		}
	})
	t.Run("MiniMax M3 turns on as adaptive", func(t *testing.T) {
		body := wire(effortProvider("openai", "https://api.minimax.io/v1"), effortRequest("MiniMax-M3", EffortOn, true))
		th, _ := body["thinking"].(map[string]any)
		if th["type"] != "adaptive" {
			t.Errorf("thinking = %v, want type adaptive", body["thinking"])
		}
	})
	t.Run("Qwen Cloud: off is the switch, levels go with it on", func(t *testing.T) {
		p := effortProvider("openai", "https://dashscope-intl.aliyuncs.com/compatible-mode/v1")
		body := wire(p, effortRequest("qwen3.8-max", EffortNone, true))
		if body["enable_thinking"] != false {
			t.Errorf("enable_thinking = %v, want false", body["enable_thinking"])
		}
		if _, ok := body["reasoning_effort"]; ok {
			t.Errorf("no level goes with thinking off: %v", body["reasoning_effort"])
		}
		body = wire(p, effortRequest("qwen3.8-max", EffortXHigh, true))
		if body["enable_thinking"] != true || body["reasoning_effort"] != EffortXHigh {
			t.Errorf("got enable_thinking=%v reasoning_effort=%v", body["enable_thinking"], body["reasoning_effort"])
		}
	})
	t.Run("Groq names Qwen3.8's top level high", func(t *testing.T) {
		body := wire(effortProvider("openai", "https://api.groq.com/openai/v1"), effortRequest("qwen/qwen3.8-27b", EffortXHigh, true))
		if body["reasoning_effort"] != EffortHigh {
			t.Errorf("reasoning_effort = %v, want high", body["reasoning_effort"])
		}
	})
	t.Run("an unknown host is sent nothing", func(t *testing.T) {
		body := wire(effortProvider("openai", "http://localhost:1234/v1"), effortRequest("gpt-oss-20b", EffortHigh, true))
		for _, k := range []string{"reasoning_effort", "reasoning", "thinking", "enable_thinking"} {
			if _, ok := body[k]; ok {
				t.Errorf("%s sent to an endpoint that may reject it: %v", k, body[k])
			}
		}
	})
}

func TestOpenRouterEffortsFromListing(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name   string
		r      *oaiModelReasoning
		levels []string
		deflt  string
	}{
		{"no reasoning object", nil, nil, ""},
		{"levels arrive highest first", &oaiModelReasoning{SupportedEfforts: json.RawMessage(`["xhigh","high","medium","low","none"]`), DefaultEffort: "medium"}, noneToXHighEfforts, EffortMedium},
		{"a mandatory model loses none", &oaiModelReasoning{SupportedEfforts: json.RawMessage(`["max","xhigh","high","medium","low","none"]`), DefaultEffort: "high", Mandatory: true}, claudeEfforts, EffortHigh},
		{"no levels: an on/off switch", &oaiModelReasoning{DefaultEnabled: &yes}, onOffEfforts, EffortOn},
		{"no levels and mandatory: nothing to choose", &oaiModelReasoning{Mandatory: true}, nil, ""},
		{"null: every level", &oaiModelReasoning{SupportedEfforts: json.RawMessage(`null`), DefaultEnabled: &no}, noneToHighEfforts, EffortNone},
		{"one level is no choice", &oaiModelReasoning{SupportedEfforts: json.RawMessage(`["high"]`), DefaultEffort: "high"}, nil, ""},
	}
	for _, c := range cases {
		levels, def := openRouterEfforts(c.r)
		if !slices.Equal(levels, c.levels) || def != c.deflt {
			t.Errorf("%s: got %v %q, want %v %q", c.name, levels, def, c.levels, c.deflt)
		}
	}
}

func TestOllamaEffortsFromShow(t *testing.T) {
	cases := []struct {
		name   string
		resp   ollamaShowResponse
		model  string
		local  bool
		levels []string
		deflt  string
		known  bool
	}{
		{"named levels with off", ollamaShowResponse{Thinking: &ollamaThinking{Values: []any{false, "low", "high", "max"}, Default: "low"}}, "deepseek-v4-pro:0813", false, noneLowHighMaxEfforts, EffortLow, true},
		{"an on/off model", ollamaShowResponse{Thinking: &ollamaThinking{Values: []any{false, true}, Default: true}}, "kimi-k2.6", false, onOffEfforts, EffortOn, true},
		{"always thinking: one value", ollamaShowResponse{Thinking: &ollamaThinking{Values: []any{true}, Default: true}}, "minimax-m2.7", false, []string{EffortOn}, EffortOn, true},
		{"never thinking, said by metadata", ollamaShowResponse{Thinking: &ollamaThinking{Values: []any{false}, Default: false}}, "llama3.2:3b", true, []string{EffortNone}, EffortNone, true},
		{"never thinking, said by capabilities", ollamaShowResponse{Capabilities: []string{"completion", "tools"}}, "llama3.2:3b", true, []string{EffortNone}, EffortNone, true},
		{"a local template model that thinks", ollamaShowResponse{Capabilities: []string{"completion", "thinking"}}, "qwen3:8b", true, onOffEfforts, "", true},
		{"a catalogued thinker without metadata is left to the catalogue", ollamaShowResponse{Capabilities: []string{"completion", "thinking"}}, "gpt-oss:20b", true, nil, "", false},
		{"a cloud thinker without metadata is left to the catalogue", ollamaShowResponse{Capabilities: []string{"completion", "thinking"}}, "minimax-m3", false, nil, "", false},
		{"nothing known", ollamaShowResponse{}, "x", true, nil, "", false},
	}
	for _, c := range cases {
		levels, def, known := ollamaEffortsFromShow(&c.resp, c.model, c.local)
		if !slices.Equal(levels, c.levels) || def != c.deflt || known != c.known {
			t.Errorf("%s: got %v %q %v, want %v %q %v", c.name, levels, def, known, c.levels, c.deflt, c.known)
		}
	}
}

// A cloud model listed by both the instance and the catalog keeps what the
// cloud's /api/show said about it.
func TestMergeOllamaModelsKeepsCatalogFacts(t *testing.T) {
	local := []ModelInfo{{ID: "glm-5.2:cloud"}, {ID: "qwen3:8b", Efforts: onOffEfforts}}
	catalog := []ModelInfo{{ID: "glm-5.2:cloud", ContextWindow: 1_000_000, Efforts: noneHighMaxEfforts, DefaultEffort: EffortHigh}}
	merged := mergeOllamaModels(local, catalog)
	if len(merged) != 2 {
		t.Fatalf("merged %d models, want 2", len(merged))
	}
	glm := merged[0]
	if glm.ContextWindow != 1_000_000 || !slices.Equal(glm.Efforts, noneHighMaxEfforts) || glm.DefaultEffort != EffortHigh {
		t.Errorf("glm-5.2:cloud lost the catalog's facts: %+v", glm)
	}
	if !slices.Equal(merged[1].Efforts, onOffEfforts) {
		t.Errorf("a local model's own levels must stay: %+v", merged[1])
	}
}
