package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
)

// wireVisionProvider wraps a real OpenAI-compatible provider so a test can put
// it behind an httptest server and inspect the headers it actually sent.
type wireVisionProvider struct {
	provider.Provider
	models []provider.ModelInfo
}

func (p wireVisionProvider) ID() string                   { return "visiontest" }
func (p wireVisionProvider) Models() []provider.ModelInfo { return p.models }
func (p wireVisionProvider) EffortSpec(string) provider.EffortSpec {
	return provider.EffortSpec{
		Levels:  []string{provider.EffortNone, provider.EffortLow, provider.EffortMedium, provider.EffortHigh, provider.EffortMax},
		Default: provider.EffortMedium,
	}
}

// TestVisionCallCarriesComplexityOntoTheWire pins the end-to-end shape of the
// vision request: the effort the agent chose reaches the provider both as the
// body's reasoning effort and as the X-Client-Complexity label the OGX gateway
// routes on. A level outside the gateway's three depths sends no label at all,
// because the gateway would otherwise replace the caller's explicit level with
// the one the class names.
func TestVisionCallCarriesComplexityOntoTheWire(t *testing.T) {
	tests := []struct {
		effort         string
		wantComplexity string
	}{
		{provider.EffortLow, "easy"},
		{provider.EffortMedium, "medium"},
		{provider.EffortHigh, "hard"},
		{provider.EffortNone, ""},
		{provider.EffortMax, ""},
		{"", ""},
	}
	for _, tc := range tests {
		name := tc.effort
		if name == "" {
			name = "unset"
		}
		t.Run(name, func(t *testing.T) {
			var seen http.Header
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = r.Header.Clone()
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
			}))
			defer srv.Close()

			inner, err := provider.NewOpenAICompatibleProvider("visiontest", "k", srv.URL, "eye-model", "")
			if err != nil {
				t.Fatal(err)
			}
			p := wireVisionProvider{Provider: inner, models: []provider.ModelInfo{{ID: "eye-model", SupportsImages: true, ActiveByDefault: true}}}
			reg := provider.NewRegistry()
			reg.Register(p)
			lr := &LoopRunner{Registry: reg, DefaultProvider: p}

			if _, err := lr.RunVisionSession(context.Background(), []byte("img"), "image/png", visionModeDescribe, "", "eye-model", "visiontest", tc.effort); err != nil {
				t.Fatalf("RunVisionSession: %v", err)
			}
			if got := seen.Get("X-Client-Complexity"); got != tc.wantComplexity {
				t.Errorf("X-Client-Complexity = %q, want %q", got, tc.wantComplexity)
			}
			if got := seen.Get("X-Client-Task"); got != string(provider.TaskVision) {
				t.Errorf("X-Client-Task = %q, want %q", got, provider.TaskVision)
			}
		})
	}
}
