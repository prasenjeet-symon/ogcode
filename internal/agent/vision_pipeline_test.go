package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

type visionTestProvider struct {
	models  []provider.ModelInfo
	events  []provider.StreamEvent
	lastReq *provider.StreamRequest
}

func (p visionTestProvider) ID() string {
	return "visiontest"
}

func (p visionTestProvider) Models() []provider.ModelInfo {
	return p.models
}

func (p visionTestProvider) StreamChat(_ context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	if p.lastReq != nil {
		*p.lastReq = req
	}
	ch := make(chan provider.StreamEvent, len(p.events))
	for _, evt := range p.events {
		ch <- evt
	}
	close(ch)
	return ch, nil
}

// EffortSpec reports the levels a scripted model lists, the way a host's own
// metadata answers (Ollama's /api/show, OpenRouter's reasoning object). Models
// that list none take no effort through this provider.
func (p visionTestProvider) EffortSpec(model string) provider.EffortSpec {
	for _, m := range p.models {
		if m.ID == model && len(m.Efforts) > 0 {
			return provider.EffortSpec{Levels: m.Efforts, Default: m.DefaultEffort}
		}
	}
	return provider.EffortSpec{}
}

// visionRunner wires a LoopRunner against a scripted provider, mirroring the
// search-pipeline tests' harness. The provider MUST list every model a test
// routes to: the pipeline resolves providers and checks image support through
// the registry.
func visionRunner(t *testing.T, models []provider.ModelInfo, events []provider.StreamEvent) (*LoopRunner, *provider.StreamRequest) {
	t.Helper()
	p := visionTestProvider{
		models:  models,
		events:  events,
		lastReq: &provider.StreamRequest{},
	}
	reg := provider.NewRegistry()
	reg.Register(p)
	return &LoopRunner{Registry: reg, DefaultProvider: p}, p.lastReq
}

func TestVisionPromptForSelectsPerMode(t *testing.T) {
	tests := []struct {
		mode      string
		systemHas string
		userHas   string
		maxTokens int
	}{
		{visionModeTranscribe, "Transcribe ALL text", "reading order", visionTranscribeMaxTokens},
		{visionModeDescribe, "You describe images", "cannot see", visionDescribeMaxTokens},
		{visionModeAnswer, "You answer questions", "what is the total", visionAnswerMaxTokens},
	}
	for _, tt := range tests {
		system, user, maxTokens := visionPromptFor(tt.mode, "what is the total")
		if !strings.Contains(system, tt.systemHas) {
			t.Errorf("mode %q: system %q missing %q", tt.mode, system, tt.systemHas)
		}
		if !strings.Contains(user, tt.userHas) {
			t.Errorf("mode %q: user %q missing %q", tt.mode, user, tt.userHas)
		}
		if maxTokens != tt.maxTokens {
			t.Errorf("mode %q: maxTokens = %d, want %d", tt.mode, maxTokens, tt.maxTokens)
		}
	}
}

// TestVisionPromptsDiagnoseFromVisibleEvidence pins the answer mode's contract:
// a debugging inquiry comes back as the visible evidence, the reading of it and
// the next step, kept apart — not a bare description — while describe mode may
// name a failure it sees but never its cause.
func TestVisionPromptsDiagnoseFromVisibleEvidence(t *testing.T) {
	for _, want := range []string{"what that evidence indicates", "what to check or change next", "inference"} {
		if !strings.Contains(visionAnswerSystem, want) {
			t.Errorf("visionAnswerSystem is missing %q", want)
		}
	}
	for _, want := range []string{"visible failure state", "without interpreting its cause"} {
		if !strings.Contains(visionDescribeSystem, want) {
			t.Errorf("visionDescribeSystem is missing %q", want)
		}
	}
}

func TestVisionPipelineCallsModelPerMode(t *testing.T) {
	tests := []struct {
		name       string
		mode       string
		question   string
		wantSystem string
		wantTokens int
	}{
		{"transcribe", visionModeTranscribe, "", visionTranscribeSystem, visionTranscribeMaxTokens},
		{"describe", visionModeDescribe, "", visionDescribeSystem, visionDescribeMaxTokens},
		{"answer", visionModeAnswer, "what is the total", visionAnswerSystem, visionAnswerMaxTokens},
		// An answer with nothing to answer degrades to a description — the
		// deterministic fallback rather than an error.
		{"answer without a question", visionModeAnswer, "", visionDescribeSystem, visionDescribeMaxTokens},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lr, last := visionRunner(t,
				[]provider.ModelInfo{{ID: "eye-model", SupportsImages: true, ActiveByDefault: true}},
				[]provider.StreamEvent{{Type: provider.EventTextDelta, Text: "extracted text"}})

			out, err := lr.RunVisionSession(context.Background(), []byte("imagedata"), "image/png", tt.mode, tt.question, "eye-model", "visiontest", "")
			if err != nil {
				t.Fatalf("RunVisionSession(%q): %v", tt.mode, err)
			}
			if !strings.HasPrefix(out, "extracted text") {
				t.Errorf("out = %q, want the model text prefix", out)
			}
			if last.Model != "eye-model" {
				t.Errorf("req.Model = %q, want the inherited model", last.Model)
			}
			if len(last.System) != 1 || last.System[0] != tt.wantSystem {
				t.Errorf("req.System = %v, want the %q prompt", last.System, tt.name)
			}
			if want := tt.wantTokens + visionThinkingHeadroom; last.MaxTokens != want {
				t.Errorf("req.MaxTokens = %d, want %d (mode budget plus thinking headroom)", last.MaxTokens, want)
			}
		})
	}
}

func TestVisionPipelineAttachesImage(t *testing.T) {
	imageBytes := []byte("fake image payload")
	lr, last := visionRunner(t,
		[]provider.ModelInfo{{ID: "eye-model", SupportsImages: true, ActiveByDefault: true}},
		[]provider.StreamEvent{{Type: provider.EventTextDelta, Text: "ok"}})

	if _, err := lr.RunVisionSession(context.Background(), imageBytes, "image/jpeg", "describe", "", "eye-model", "visiontest", ""); err != nil {
		t.Fatalf("RunVisionSession: %v", err)
	}
	if last.Task != provider.TaskVision {
		t.Errorf("req.Task = %q, want %q", last.Task, provider.TaskVision)
	}
	if len(last.Messages) != 1 || last.Messages[0].Role != "user" {
		t.Fatalf("req.Messages = %+v, want a single user message", last.Messages)
	}
	msg := last.Messages[0]
	var userText string
	if err := json.Unmarshal(msg.Content, &userText); err != nil {
		t.Fatalf("user content is not JSON text: %v", err)
	}
	if !strings.Contains(userText, "Describe the visual content") {
		t.Errorf("user content = %q, want the describe directive", userText)
	}
	if len(msg.Images) != 1 {
		t.Fatalf("message has %d images, want 1", len(msg.Images))
	}
	if msg.Images[0].MediaType != "image/jpeg" {
		t.Errorf("image MediaType = %q, want image/jpeg", msg.Images[0].MediaType)
	}
	decoded, err := base64.StdEncoding.DecodeString(msg.Images[0].Data)
	if err != nil {
		t.Fatalf("image Data is not valid base64: %v", err)
	}
	if string(decoded) != string(imageBytes) {
		t.Errorf("decoded image = %q, want the original bytes", decoded)
	}
}

func TestVisionPipelineImageModelOverrideWins(t *testing.T) {
	lr, last := visionRunner(t,
		[]provider.ModelInfo{{ID: "blind-model"}, {ID: "eye-model", SupportsImages: true}},
		[]provider.StreamEvent{{Type: provider.EventTextDelta, Text: "ok"}})
	lr.ImageModel = func() (string, string) { return "eye-model", "visiontest" }

	out, err := lr.RunVisionSession(context.Background(), []byte("imagedata"), "image/png", "transcribe", "", "blind-model", "visiontest", "")
	if err != nil {
		t.Fatalf("RunVisionSession: %v", err)
	}
	if last.Model != "eye-model" {
		t.Errorf("req.Model = %q, want the settings-screen image model (the session model cannot see)", last.Model)
	}
	if !strings.Contains(out, "eye-model") {
		t.Errorf("out = %q, want the resolved model named in the footer", out)
	}
}

func TestVisionPipelineInheritsModelWhenPrefEmpty(t *testing.T) {
	lr, last := visionRunner(t,
		[]provider.ModelInfo{{ID: "eye-model", SupportsImages: true, ActiveByDefault: true}},
		[]provider.StreamEvent{{Type: provider.EventTextDelta, Text: "ok"}})
	lr.ImageModel = func() (string, string) { return "", "" }

	if _, err := lr.RunVisionSession(context.Background(), []byte("imagedata"), "image/png", "describe", "", "eye-model", "visiontest", ""); err != nil {
		t.Fatalf("RunVisionSession: %v", err)
	}
	if last.Model != "eye-model" {
		t.Errorf("req.Model = %q, want the inherited session model", last.Model)
	}
}

func TestVisionPipelineRejectsNonVisionModel(t *testing.T) {
	lr, _ := visionRunner(t,
		[]provider.ModelInfo{{ID: "blind-model"}},
		[]provider.StreamEvent{{Type: provider.EventTextDelta, Text: "ok"}})

	_, err := lr.RunVisionSession(context.Background(), []byte("imagedata"), "image/png", "describe", "", "blind-model", "visiontest", "")
	if err == nil {
		t.Fatal("RunVisionSession succeeded with a non-vision model, want an error")
	}
	if !strings.Contains(err.Error(), "cannot accept images") {
		t.Errorf("err = %v, want the vision-capability message", err)
	}
}

func TestVisionPipelineRejectsUnavailableModel(t *testing.T) {
	lr, _ := visionRunner(t,
		[]provider.ModelInfo{{ID: "eye-model", SupportsImages: true, ActiveByDefault: true}},
		[]provider.StreamEvent{{Type: provider.EventTextDelta, Text: "ok"}})

	if _, err := lr.RunVisionSession(context.Background(), []byte("imagedata"), "image/png", "describe", "", "nope-model", "visiontest", ""); err == nil {
		t.Error("RunVisionSession succeeded with a model no provider serves, want an availability error")
	} else if !strings.Contains(err.Error(), "not available on any configured provider") {
		t.Errorf("err = %v, want the unavailable-model message", err)
	}
}

func TestVisionPipelineEmptyResponseErrors(t *testing.T) {
	lr, _ := visionRunner(t,
		[]provider.ModelInfo{{ID: "eye-model", SupportsImages: true, ActiveByDefault: true}},
		[]provider.StreamEvent{{Type: provider.EventFinish}})

	_, err := lr.RunVisionSession(context.Background(), []byte("imagedata"), "image/png", "describe", "", "eye-model", "visiontest", "")
	if err == nil {
		t.Fatal("RunVisionSession succeeded with an empty response, want an error")
	}
	if !strings.Contains(err.Error(), "returned no text") {
		t.Errorf("err = %v, want the empty-response message", err)
	}
}

func TestVisionPipelineStreamErrorIsFailure(t *testing.T) {
	lr, _ := visionRunner(t,
		[]provider.ModelInfo{{ID: "eye-model", SupportsImages: true, ActiveByDefault: true}},
		[]provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "partial"},
			{Type: provider.EventError, Error: "stream disconnected"},
		})

	out, err := lr.RunVisionSession(context.Background(), []byte("imagedata"), "image/png", "describe", "", "eye-model", "visiontest", "")
	if err == nil {
		t.Fatal("RunVisionSession succeeded after a stream error, want a failure")
	}
	if !strings.Contains(err.Error(), "stream disconnected") {
		t.Errorf("err = %v, want the transport error", err)
	}
	if out != "" {
		t.Errorf("out = %q, want nothing on a failed call", out)
	}
}

func TestVisionPipelineInputValidation(t *testing.T) {
	tests := []struct {
		name    string
		image   []byte
		mode    string
		wantErr string
	}{
		{"no image bytes", nil, visionModeDescribe, "no image bytes"},
		{"unknown mode", []byte("imagedata"), "read aloud", "unknown vision mode"},
	}
	for _, tt := range tests {
		lr := &LoopRunner{}
		if _, err := lr.RunVisionSession(context.Background(), tt.image, "image/png", tt.mode, "", "eye-model", "visiontest", ""); err == nil {
			t.Errorf("%s: RunVisionSession succeeded, want %q", tt.name, tt.wantErr)
		} else if !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("%s: err = %v, want it to contain %q", tt.name, err, tt.wantErr)
		}
	}
}

func TestVisionPipelineUsesProbedModelCapability(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := session.SetModelCapability(database, &session.ModelCapability{
		ModelID:        "probed-vl",
		SupportsImages: true,
		ProbedAt:       session.Now(),
	}); err != nil {
		t.Fatalf("set capability: %v", err)
	}

	// The provider listing does NOT set SupportsImages (simulating a dynamic
	// Ollama/OpenRouter model whose vision support was discovered by probing).
	lr, last := visionRunner(t,
		[]provider.ModelInfo{{ID: "probed-vl", SupportsImages: false}},
		[]provider.StreamEvent{{Type: provider.EventTextDelta, Text: "transcribed"}})
	lr.Store = session.NewStore(database)
	lr.ImageModel = func() (string, string) { return "probed-vl", "visiontest" }

	if m, p := lr.ResolveVisionModel("blind-session", "visiontest"); m != "probed-vl" || p != "visiontest" {
		t.Fatalf("ResolveVisionModel = (%q, %q), want (probed-vl, visiontest)", m, p)
	}
	out, err := lr.RunVisionSession(context.Background(), []byte("imagedata"), "image/png", "transcribe", "", "blind-session", "visiontest", "")
	if err != nil {
		t.Fatalf("RunVisionSession failed for probed vision model: %v", err)
	}
	if last.Model != "probed-vl" || !strings.HasPrefix(out, "transcribed") {
		t.Fatalf("got model=%q out=%q", last.Model, out)
	}
}

// effortVisionModels is the model list the effort pins route to: the host
// reports none/low/medium/high for it, so every level the pins name is one it
// takes and a level above high is one it does not.
func effortVisionModels() []provider.ModelInfo {
	return []provider.ModelInfo{{
		ID:              "eye-model",
		SupportsImages:  true,
		ActiveByDefault: true,
		Efforts:         []string{provider.EffortNone, provider.EffortLow, provider.EffortMedium, provider.EffortHigh},
	}}
}

// A level the caller names rides on the vision request with Thinking set — the
// providers carry effort only on a thinking request, including "none", which
// turns reasoning off — and the mode's budget gains room for the reasoning so a
// 2048-token answer budget is not spent before any text comes out.
func TestVisionPipelineSendsChosenEffort(t *testing.T) {
	for _, tc := range []struct {
		level string
	}{
		{provider.EffortHigh},
		{provider.EffortNone},
	} {
		t.Run(tc.level, func(t *testing.T) {
			lr, last := visionRunner(t, effortVisionModels(),
				[]provider.StreamEvent{{Type: provider.EventTextDelta, Text: "ok"}})

			if _, err := lr.RunVisionSession(context.Background(), []byte("imagedata"), "image/png",
				visionModeAnswer, "what is the total?", "eye-model", "visiontest", tc.level); err != nil {
				t.Fatalf("RunVisionSession: %v", err)
			}
			if !last.Thinking {
				t.Error("req.Thinking = false, want true: effort rides only on a thinking request")
			}
			if last.Effort != tc.level {
				t.Errorf("req.Effort = %q, want %q", last.Effort, tc.level)
			}
			if want := visionAnswerMaxTokens + visionThinkingHeadroom; last.MaxTokens != want {
				t.Errorf("req.MaxTokens = %d, want %d (answer budget plus thinking headroom)", last.MaxTokens, want)
			}
		})
	}
}

// The level the agent names is also the call's declared difficulty: the gateway
// routes an image call on the complexity class, and the three depths the tool
// offers map back onto themselves through the router's ladder. A level the
// ladder cannot name leaves the header absent on purpose — the router REPLACES
// the body's level with the class's, so an invented class would overrule the
// caller's choice.
func TestVisionPipelineNamesTheEffortAsComplexity(t *testing.T) {
	models := []provider.ModelInfo{{
		ID:              "eye-model",
		SupportsImages:  true,
		ActiveByDefault: true,
		Efforts:         []string{provider.EffortNone, provider.EffortLow, provider.EffortMedium, provider.EffortHigh, provider.EffortMax},
	}}
	for _, tc := range []struct {
		name  string
		level string
		want  provider.Complexity
	}{
		{"low", provider.EffortLow, provider.ComplexityEasy},
		{"medium", provider.EffortMedium, provider.ComplexityMedium},
		{"high", provider.EffortHigh, provider.ComplexityHard},
		{"none", provider.EffortNone, ""},
		{"max", provider.EffortMax, ""},
		{"unset", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lr, last := visionRunner(t, models,
				[]provider.StreamEvent{{Type: provider.EventTextDelta, Text: "ok"}})

			if _, err := lr.RunVisionSession(context.Background(), []byte("imagedata"), "image/png",
				visionModeDescribe, "", "eye-model", "visiontest", tc.level); err != nil {
				t.Fatalf("RunVisionSession: %v", err)
			}
			if last.Complexity != tc.want {
				t.Errorf("req.Complexity = %q (effort %q), want %q", last.Complexity, tc.level, tc.want)
			}
		})
	}
}

// The level is resolved against the RESOLVED vision model, never the session's:
// the settings-screen override can point at a model with another ladder. A level
// that model does not take is dropped rather than sent — most hosts reject it
// outright — and one it does take survives a blind session model.
func TestVisionPipelineResolvesEffortAgainstVisionModel(t *testing.T) {
	t.Run("level the vision model lacks is dropped", func(t *testing.T) {
		lr, last := visionRunner(t, effortVisionModels(),
			[]provider.StreamEvent{{Type: provider.EventTextDelta, Text: "ok"}})

		if _, err := lr.RunVisionSession(context.Background(), []byte("imagedata"), "image/png",
			visionModeDescribe, "", "eye-model", "visiontest", provider.EffortMax); err != nil {
			t.Fatalf("RunVisionSession: %v", err)
		}
		if last.Thinking || last.Effort != provider.EffortNone {
			t.Errorf("req.Thinking = %v, req.Effort = %q, want the unsupported level dropped and reasoning named off", last.Thinking, last.Effort)
		}
		if last.Complexity != "" {
			t.Errorf("req.Complexity = %q, want no class for a level the body does not carry", last.Complexity)
		}
		if want := visionDescribeMaxTokens + visionThinkingHeadroom; last.MaxTokens != want {
			t.Errorf("req.MaxTokens = %d, want %d", last.MaxTokens, want)
		}
	})

	t.Run("blind session model keeps the vision model's level", func(t *testing.T) {
		lr, last := visionRunner(t, []provider.ModelInfo{
			{ID: "blind-model"},
			{ID: "eye-model", SupportsImages: true, Efforts: []string{provider.EffortLow, provider.EffortHigh}},
		}, []provider.StreamEvent{{Type: provider.EventTextDelta, Text: "ok"}})
		lr.ImageModel = func() (string, string) { return "eye-model", "visiontest" }

		if _, err := lr.RunVisionSession(context.Background(), []byte("imagedata"), "image/png",
			visionModeDescribe, "", "blind-model", "visiontest", provider.EffortHigh); err != nil {
			t.Fatalf("RunVisionSession: %v", err)
		}
		if !last.Thinking || last.Effort != provider.EffortHigh {
			t.Errorf("req.Thinking = %v, req.Effort = %q, want high kept for the resolved vision model", last.Thinking, last.Effort)
		}
	})
}

// Asking for no effort keeps the flat shape — no Thinking, no complexity class
// — but names "none" in the body so the endpoint's default, or a gateway pin,
// cannot reason inside the answer budget. The headroom rides either way.
func TestVisionPipelineNoEffortNamesReasoningOff(t *testing.T) {
	lr, last := visionRunner(t, effortVisionModels(),
		[]provider.StreamEvent{{Type: provider.EventTextDelta, Text: "ok"}})

	if _, err := lr.RunVisionSession(context.Background(), []byte("imagedata"), "image/png",
		visionModeDescribe, "", "eye-model", "visiontest", ""); err != nil {
		t.Fatalf("RunVisionSession: %v", err)
	}
	if last.Thinking || last.Effort != provider.EffortNone {
		t.Errorf("req.Thinking = %v, req.Effort = %q, want no thinking and an explicit none", last.Thinking, last.Effort)
	}
	if last.Complexity != "" {
		t.Errorf("req.Complexity = %q, want no class without a chosen level", last.Complexity)
	}
	if want := visionDescribeMaxTokens + visionThinkingHeadroom; last.MaxTokens != want {
		t.Errorf("req.MaxTokens = %d, want %d (mode budget plus thinking headroom)", last.MaxTokens, want)
	}
}

// Reasoning that spent the whole ceiling leaves nothing for the answer, and the
// failure names the budget the call was given: the mode's budget plus the
// thinking headroom, not the bare answer budget that emptied it.
func TestVisionPipelineEmptyAnswerReportsHeadroomBudget(t *testing.T) {
	length := "length"
	lr, _ := visionRunner(t,
		[]provider.ModelInfo{{ID: "eye-model", SupportsImages: true, ActiveByDefault: true}},
		[]provider.StreamEvent{
			{Type: provider.EventReasoning, Text: strings.Repeat("weighing the evidence ", 50)},
			{Type: provider.EventFinish, FinishReason: &length},
		})

	_, err := lr.RunVisionSession(context.Background(), []byte("imagedata"), "image/png",
		visionModeAnswer, "is this page dark mode?", "eye-model", "visiontest", "")
	if err == nil {
		t.Fatal("RunVisionSession succeeded with no answer, want an error")
	}
	want := fmt.Sprintf("no answer within %d output tokens", visionAnswerMaxTokens+visionThinkingHeadroom)
	if !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v, want it to name %q", err, want)
	}
}
