package provider

import (
	"encoding/json"
	"testing"
)

// catalogued finds the first OpenAI catalogue entry matching pred, so these
// tests follow the catalogue instead of pinning model ids that change with it.
func openAIModelWhere(t *testing.T, what string, pred func(CatalogModel) bool) CatalogModel {
	t.Helper()
	for _, m := range OpenAIModels {
		if pred(m) {
			return m
		}
	}
	t.Fatalf("no OpenAI catalogue entry %s", what)
	return CatalogModel{}
}

var shapeTool = []ToolDefinition{{Name: "read", Description: "Read a file", Parameters: json.RawMessage(`{"type":"object"}`)}}

func shapeRequest(model string, tools []ToolDefinition, thinking bool) StreamRequest {
	return StreamRequest{
		Model:       model,
		Messages:    []ModelMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
		Tools:       tools,
		Temperature: 0.3,
		MaxTokens:   500,
		Thinking:    thinking,
	}
}

func openAIWire(t *testing.T, req StreamRequest) map[string]any {
	t.Helper()
	p := &OpenAIProvider{id: "openai", apiKey: "k", baseURL: "https://api.openai.com/v1"}
	body, _ := captureWireRequest(t, p, req)
	return body
}

// GPT-5.4 and later take tools on Chat Completions only with reasoning off; an
// agent step that left the effort at the model's default would be a 400.
func TestOpenAIShape_ToolsTurnReasoningOffWhereRequired(t *testing.T) {
	m := openAIModelWhere(t, "that needs reasoning off for tools", func(m CatalogModel) bool { return m.ToolsNeedNoReasoning })
	body := openAIWire(t, shapeRequest(m.ID, shapeTool, true))
	if body["reasoning_effort"] != "none" {
		t.Errorf("reasoning_effort = %v, want none", body["reasoning_effort"])
	}
	if body["temperature"] != 0.3 {
		t.Errorf("temperature = %v, want it kept: a model not reasoning takes it", body["temperature"])
	}
	if _, ok := body["max_tokens"]; ok {
		t.Errorf("max_tokens sent: %v", body["max_tokens"])
	}
	if body["max_completion_tokens"] != float64(500) {
		t.Errorf("max_completion_tokens = %v, want 500", body["max_completion_tokens"])
	}
}

// A model that reasons with tools on Chat Completions keeps its own effort for
// an agent step, and so rejects temperature.
func TestOpenAIShape_AgentStepKeepsTheModelsEffort(t *testing.T) {
	m := openAIModelWhere(t, "that always reasons and takes tools with it", func(m CatalogModel) bool {
		return m.EffortFloor != "" && m.EffortFloor != "none" && !m.ToolsNeedNoReasoning && !m.ResponsesOnly
	})
	body := openAIWire(t, shapeRequest(m.ID, shapeTool, true))
	if got, ok := body["reasoning_effort"]; ok {
		t.Errorf("reasoning_effort = %v, want the model's default (unset)", got)
	}
	if got, ok := body["temperature"]; ok {
		t.Errorf("temperature = %v sent to a reasoning model", got)
	}
}

// A utility call does not ask for thinking, so it gets the model's lowest
// effort; above "none" that is still reasoning, and temperature goes.
func TestOpenAIShape_UtilityCallsGetTheLowestEffort(t *testing.T) {
	for _, m := range []CatalogModel{
		openAIModelWhere(t, "whose floor is none", func(m CatalogModel) bool { return m.EffortFloor == "none" && !m.ResponsesOnly }),
		openAIModelWhere(t, "whose floor is above none", func(m CatalogModel) bool {
			return m.EffortFloor != "" && m.EffortFloor != "none" && !m.ResponsesOnly
		}),
	} {
		body := openAIWire(t, shapeRequest(m.ID, nil, false))
		if body["reasoning_effort"] != m.EffortFloor {
			t.Errorf("%s: reasoning_effort = %v, want %s", m.ID, body["reasoning_effort"], m.EffortFloor)
		}
		_, hasTemp := body["temperature"]
		if wantTemp := m.EffortFloor == "none"; hasTemp != wantTemp {
			t.Errorf("%s: temperature sent = %v, want %v at effort %s", m.ID, hasTemp, wantTemp, m.EffortFloor)
		}
	}
}

// A model that does not reason gets no effort and keeps its temperature, but
// still moves to max_completion_tokens; dated snapshots and host spellings are
// the same model.
func TestOpenAIShape_NonReasoningModels(t *testing.T) {
	m := openAIModelWhere(t, "that does not reason", func(m CatalogModel) bool { return m.EffortFloor == "" })
	for _, id := range []string{m.ID, m.ID + "-2025-04-14", "openai/" + m.ID} {
		body := openAIWire(t, shapeRequest(id, shapeTool, true))
		if _, ok := body["reasoning_effort"]; ok {
			t.Errorf("%s: reasoning_effort sent to a model that does not reason", id)
		}
		if body["temperature"] != 0.3 {
			t.Errorf("%s: temperature = %v, want 0.3", id, body["temperature"])
		}
		if body["max_completion_tokens"] != float64(500) {
			t.Errorf("%s: max_completion_tokens = %v, want 500", id, body["max_completion_tokens"])
		}
	}
}

// Anything the OpenAI catalogue does not know — another vendor's model behind a
// compatible endpoint — and every other provider's request go out untouched.
func TestOpenAIShape_LeavesOtherModelsAndProvidersAlone(t *testing.T) {
	for name, body := range map[string]map[string]any{
		"uncatalogued model": openAIWire(t, shapeRequest("deepseek-flash", shapeTool, true)),
		"openrouter": func() map[string]any {
			p := &OpenAIProvider{id: "openrouter", apiKey: "k", baseURL: "https://openrouter.ai/api/v1"}
			b, _ := captureWireRequest(t, p, shapeRequest("openai/gpt-6-sol", shapeTool, true))
			return b
		}(),
	} {
		if body["max_tokens"] != float64(500) || body["temperature"] != 0.3 {
			t.Errorf("%s: max_tokens/temperature = %v/%v, want the caller's 500/0.3", name, body["max_tokens"], body["temperature"])
		}
		for _, k := range []string{"max_completion_tokens", "reasoning_effort"} {
			if v, ok := body[k]; ok {
				t.Errorf("%s: %s = %v sent", name, k, v)
			}
		}
	}
}

// The provider never offers a model whose tool calling needs the Responses API.
func TestOpenAICatalogHidesResponsesOnlyModels(t *testing.T) {
	hidden := openAIModelWhere(t, "that is Responses-only", func(m CatalogModel) bool { return m.ResponsesOnly })
	for _, m := range openAICatalog() {
		if m.ID == hidden.ID {
			t.Fatalf("%s is offered but its tools need the Responses API", m.ID)
		}
	}
	if _, ok := LookupCatalogModel("openai/" + hidden.ID); !ok {
		t.Errorf("%s dropped from the catalogue; a host that bridges it still needs its window and price", hidden.ID)
	}
}

// A catalogued model that refuses sampling parameters gets none, on any host.
func TestOpenAICompatibleDropsTemperatureForModelsThatRefuseIt(t *testing.T) {
	var refuses CatalogModel
	for _, m := range OpenModels {
		if m.RejectsSampling {
			refuses = m
			break
		}
	}
	if refuses.ID == "" {
		t.Fatal("no open model refuses sampling; pick another fixture")
	}
	p := &OpenAIProvider{id: "openrouter", apiKey: "k", baseURL: "https://openrouter.ai/api/v1"}
	body, _ := captureWireRequest(t, p, shapeRequest("vendor/"+refuses.ID, nil, false))
	if got, ok := body["temperature"]; ok {
		t.Errorf("temperature = %v sent to %s, which refuses it", got, refuses.ID)
	}
}
