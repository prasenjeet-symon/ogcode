package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// captureAnthropicBody runs one StreamChat against a stub server and returns the
// request body it actually sent. Asserting the wire body — rather than
// re-deriving it in the test — is what makes this a check on StreamChat.
func captureAnthropicBody(t *testing.T, req StreamRequest) map[string]any {
	t.Helper()

	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		raw = body
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	p := &AnthropicProvider{apiKey: "test-key", model: "claude-sonnet-4-6", baseURL: srv.URL}
	ch, err := p.StreamChat(context.Background(), req)
	if err != nil {
		t.Fatalf("stream chat: %v", err)
	}
	for range ch {
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal sent body: %v (body %q)", err, raw)
	}
	return out
}

func baseThinkingRequest(model string) StreamRequest {
	return StreamRequest{
		Model:    model,
		System:   []string{"You are a coding agent."},
		Messages: []ModelMessage{{Role: "user", Content: json.RawMessage(`"Hello"`)}},
	}
}

// The thinking configuration is per-model and opt-in per request. Sending a mode
// a model does not accept fails the whole request, so anything the catalog does
// not vouch for gets no thinking field at all.
func TestAnthropicThinkingParameter(t *testing.T) {
	t.Run("adaptive model asks for readable adaptive thinking", func(t *testing.T) {
		req := baseThinkingRequest("claude-opus-4-7")
		req.Thinking = true
		body := captureAnthropicBody(t, req)

		thinking, ok := body["thinking"].(map[string]any)
		if !ok {
			t.Fatalf("expected a thinking object, got %v", body["thinking"])
		}
		if thinking["type"] != "adaptive" {
			t.Errorf("expected type 'adaptive', got %v", thinking["type"])
		}
		// Without this the model still thinks, but the blocks come back with
		// empty text and the reasoning drawer has nothing to show.
		if thinking["display"] != "summarized" {
			t.Errorf("expected display 'summarized', got %v", thinking["display"])
		}
	})

	t.Run("no thinking is sent unless the caller asks", func(t *testing.T) {
		body := captureAnthropicBody(t, baseThinkingRequest("claude-opus-4-7"))
		if _, ok := body["thinking"]; ok {
			t.Errorf("utility calls must not enable thinking, got %v", body["thinking"])
		}
	})

	t.Run("budget-only models are left alone", func(t *testing.T) {
		// Claude 4.5 and earlier take a fixed budget that must be sized against
		// max_tokens; sending them `adaptive` is a 400.
		req := baseThinkingRequest("claude-haiku-4-5-20251001")
		req.Thinking = true
		body := captureAnthropicBody(t, req)
		if _, ok := body["thinking"]; ok {
			t.Errorf("expected no thinking config for a budget-only model, got %v", body["thinking"])
		}
	})

	t.Run("unknown models are left alone", func(t *testing.T) {
		req := baseThinkingRequest("claude-something-unreleased")
		req.Thinking = true
		body := captureAnthropicBody(t, req)
		if _, ok := body["thinking"]; ok {
			t.Errorf("expected no thinking config for an uncatalogued model, got %v", body["thinking"])
		}
	})

	t.Run("thinking displaces temperature", func(t *testing.T) {
		req := baseThinkingRequest("claude-opus-4-7")
		req.Thinking = true
		req.Temperature = 0.3
		body := captureAnthropicBody(t, req)
		if _, ok := body["temperature"]; ok {
			t.Errorf("temperature must not accompany thinking, got %v", body["temperature"])
		}

		// Without thinking it still goes out as the caller set it.
		plain := baseThinkingRequest("claude-opus-4-7")
		plain.Temperature = 0.3
		if got := captureAnthropicBody(t, plain)["temperature"]; got != 0.3 {
			t.Errorf("expected temperature preserved without thinking, got %v", got)
		}
	})
}

// Thinking tokens share max_tokens with the answer, so a thinking request that
// kept the 4096 default would spend the answer's room on reasoning and truncate
// the turn.
func TestAnthropicThinkingRaisesOutputCeiling(t *testing.T) {
	t.Run("thinking gets the model's published ceiling", func(t *testing.T) {
		req := baseThinkingRequest("claude-opus-4-7")
		req.Thinking = true
		got := captureAnthropicBody(t, req)["max_tokens"]
		want, _ := anthropicCatalogModel("claude-opus-4-7")
		if got != float64(want.MaxOutputTokens) {
			t.Errorf("expected max_tokens %d, got %v", want.MaxOutputTokens, got)
		}
	})

	t.Run("without thinking the default stands", func(t *testing.T) {
		if got := captureAnthropicBody(t, baseThinkingRequest("claude-opus-4-7"))["max_tokens"]; got != float64(4096) {
			t.Errorf("expected the 4096 default to be untouched, got %v", got)
		}
	})

	t.Run("an explicit limit is never overridden", func(t *testing.T) {
		req := baseThinkingRequest("claude-opus-4-7")
		req.Thinking = true
		req.MaxTokens = 8000
		if got := captureAnthropicBody(t, req)["max_tokens"]; got != float64(8000) {
			t.Errorf("expected the caller's limit honoured, got %v", got)
		}
	})
}

// thinkingConfigFor reads the mode from the catalog, so a new model gets its
// behaviour by being described there rather than by editing the provider.
func TestThinkingConfigForMatchesCatalog(t *testing.T) {
	for _, m := range AnthropicModels {
		got := thinkingConfigFor(m.ID)
		if m.Thinking == "adaptive" {
			if got == nil || got.Type != "adaptive" {
				t.Errorf("%s is catalogued adaptive but resolved to %+v", m.ID, got)
			}
			continue
		}
		if got != nil {
			t.Errorf("%s has no catalogued thinking mode but resolved to %+v", m.ID, got)
		}
	}
}
