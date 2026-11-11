package agent

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/permission"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
)

// riskScriptProvider replays one script of stream events per StreamChat call,
// recording each request so a test can inspect the budget it was sent with. The
// last script repeats if the loop asks more than the provider has scripts.
type riskScriptProvider struct {
	mu      sync.Mutex
	scripts [][]provider.StreamEvent
	calls   int
	reqs    []provider.StreamRequest
}

func (m *riskScriptProvider) ID() string { return "mock" }
func (m *riskScriptProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", ProviderID: "mock"}}
}

func (m *riskScriptProvider) StreamChat(_ context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	m.mu.Lock()
	script := m.scripts[min(m.calls, len(m.scripts)-1)]
	m.calls++
	m.reqs = append(m.reqs, req)
	m.mu.Unlock()

	ch := make(chan provider.StreamEvent, len(script))
	for _, evt := range script {
		ch <- evt
	}
	close(ch)
	return ch, nil
}

func (m *riskScriptProvider) requests() []provider.StreamRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]provider.StreamRequest{}, m.reqs...)
}

// textVerdict is a plain answer, the shape a non-reasoning model produces.
func textVerdict(text string) []provider.StreamEvent {
	fr := "stop"
	return []provider.StreamEvent{
		{Type: provider.EventTextDelta, Text: text},
		{Type: provider.EventFinish, FinishReason: &fr},
	}
}

// textVerdictWithUsage is a verdict that also reports what the call spent — the
// shape every real provider produces, and the usage that was being dropped
// before utility accounting existed.
func textVerdictWithUsage(text string, u *provider.TokenUsage) []provider.StreamEvent {
	fr := "stop"
	return []provider.StreamEvent{
		{Type: provider.EventTextDelta, Text: text},
		{Type: provider.EventUsage, Usage: u},
		{Type: provider.EventFinish, FinishReason: &fr},
	}
}

// reasoningOnly is the shape a reasoning model produces on a budget too small
// for its chain of thought: the reasoning lands in EventReasoning, the answer
// never arrives, and the stream ends on finish=length.
func reasoningOnly(thought string) []provider.StreamEvent {
	fr := "length"
	return []provider.StreamEvent{
		{Type: provider.EventReasoningStart},
		{Type: provider.EventReasoning, Text: thought},
		{Type: provider.EventFinish, FinishReason: &fr},
	}
}

func newRiskLoopRunner(t *testing.T, p provider.Provider) (*LoopRunner, *permission.Manager) {
	t.Helper()
	perm := permission.NewManager(nil)
	reg := provider.NewRegistry()
	reg.Register(p)
	lr := &LoopRunner{Registry: reg, Permissions: perm}
	return lr, perm
}

// Regression: the gate was sent MaxTokens: 8. A reasoning model spends its
// budget on the chain of thought before the answer, so 8 tokens bought no
// verdict at all — the response came back empty and every unclear command fell
// through to the user. Measured against the models Auto sessions run over
// ollama: at 8 tokens 3 of 16 unclear commands reached a verdict, at 1024 all
// 16 did. The budget has to leave room for thinking, not just for the word.
func TestAssessCommandRiskLLMBudgetLeavesRoomForReasoning(t *testing.T) {
	p := &riskScriptProvider{scripts: [][]provider.StreamEvent{textVerdict("SAFE")}}
	lr, _ := newRiskLoopRunner(t, p)

	if got := lr.assessCommandRiskLLM(context.Background(), "", "mock-model", "mock", "mkdir -p internal/foo"); got != permission.RiskSafe {
		t.Fatalf("verdict = %v, want RiskSafe", got)
	}

	reqs := p.requests()
	if len(reqs) != 1 {
		t.Fatalf("provider was called %d times, want 1", len(reqs))
	}
	// A chain of thought runs a few hundred characters (roughly 100 tokens) and
	// the answer follows it, so anything near the answer's own length truncates
	// the reply before it is written. 512 is the floor below which the observed
	// models stop answering.
	if got := reqs[0].MaxTokens; got < 512 {
		t.Errorf("risk gate budget = %d, too small for a reasoning model's thinking plus its one-word answer", got)
	}
	// And the system prompt must still ask for the one word the parser reads.
	if len(reqs[0].System) != 1 || !strings.Contains(reqs[0].System[0], "SAFE or ASK") {
		t.Errorf("risk gate prompt was not sent as the sole system entry: %v", reqs[0].System)
	}
}

// A reasoning-only response carries no verdict, so it must resolve to RiskAsk —
// the fail-safe direction — even when the chain of thought contains the word
// SAFE. Reasoning is counted for the log and never parsed: it can hold both
// "I'd say SAFE" and a later "but strictly… ASK", and reading the verdict out of
// it could auto-approve a deliberation that concluded ASK.
func TestAssessCommandRiskLLMReasoningOnlyIsAsk(t *testing.T) {
	p := &riskScriptProvider{scripts: [][]provider.StreamEvent{
		reasoningOnly("This looks routine, I'd say SAFE. But strictly it writes to the tree, so ASK."),
	}}
	lr, perm := newRiskLoopRunner(t, p)

	if got := lr.assessCommandRiskLLM(context.Background(), "", "mock-model", "mock", "mv a.go b.go"); got != permission.RiskAsk {
		t.Fatalf("verdict = %v, want RiskAsk when the model returned no answer", got)
	}

	// No answer is not a judgement, so it must not be cached as one: caching
	// RiskAsk would keep re-asking the user without ever consulting the model
	// again, which is the bug this whole path exists to avoid.
	if _, ok := perm.CachedRisk("mv a.go b.go", "mock-model"); ok {
		t.Error("an unanswered risk check was cached; a later call must be able to retry the model")
	}
	if got := p.calls; got != 1 {
		t.Fatalf("provider calls after one check = %d, want 1", got)
	}
	// The retry really does reach the model again.
	lr.assessCommandRiskLLM(context.Background(), "", "mock-model", "mock", "mv a.go b.go")
	if got := p.calls; got != 2 {
		t.Errorf("provider calls after a retry = %d, want 2", got)
	}
}

// A model that answers is a judgement, and judgements are cached so a repeated
// command is decided once.
func TestAssessCommandRiskLLMCachesAnAnsweredVerdict(t *testing.T) {
	p := &riskScriptProvider{scripts: [][]provider.StreamEvent{textVerdict("ASK")}}
	lr, perm := newRiskLoopRunner(t, p)

	if got := lr.assessCommandRiskLLM(context.Background(), "", "mock-model", "mock", "npm install"); got != permission.RiskAsk {
		t.Fatalf("verdict = %v, want RiskAsk", got)
	}
	if _, ok := perm.CachedRisk("npm install", "mock-model"); !ok {
		t.Fatal("an answered verdict was not cached")
	}

	lr.assessCommandRiskLLM(context.Background(), "", "mock-model", "mock", "npm install")
	if got := p.calls; got != 1 {
		t.Errorf("provider calls = %d, want 1 (the second check should hit the cache)", got)
	}
}

// An unanswered check must be distinguishable from a judgement in the log —
// otherwise the truncation that caused the "Auto mode keeps asking" bug reads
// as a verdict of ASK, which is exactly how it went unnoticed.
func TestAssessCommandRiskLLMUnansweredIsLoggedAsNoVerdict(t *testing.T) {
	var buf strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	p := &riskScriptProvider{scripts: [][]provider.StreamEvent{reasoningOnly("thinking it through")}}
	lr, _ := newRiskLoopRunner(t, p)
	lr.assessCommandRiskLLM(context.Background(), "", "mock-model", "mock", "cp a.go b.go")

	logged := buf.String()
	if !strings.Contains(logged, "got no verdict") {
		t.Errorf("an unanswered risk check was not logged as such:\n%s", logged)
	}
	if !strings.Contains(logged, "finish=length") {
		t.Errorf("the truncation reason is missing from the log:\n%s", logged)
	}
	if strings.Contains(logged, "auto-mode risk verdict") {
		t.Errorf("an unanswered check was logged as a verdict:\n%s", logged)
	}
}

// verdictFromEngine is a verdict stream that also reports which engine served
// it, the shape the router produces once its X-Ogx-Engine header is surfaced.
func verdictFromEngine(engine, text string) []provider.StreamEvent {
	fr := "stop"
	return []provider.StreamEvent{
		{Type: provider.EventEngine, Engine: engine},
		{Type: provider.EventTextDelta, Text: text},
		{Type: provider.EventFinish, FinishReason: &fr},
	}
}

// A verdict served by the router's risk profile must not enter the process-wide
// cache: the profile is rolled back or reconfigured out of band, and a cached
// SAFE would outlive the configuration that justified it. The retry below is the
// proof — it reaches the model again instead of the cache.
func TestAssessCommandRiskLLMSkipsCacheForRiskProfileVerdict(t *testing.T) {
	p := &riskScriptProvider{scripts: [][]provider.StreamEvent{verdictFromEngine("clef", "SAFE")}}
	lr, perm := newRiskLoopRunner(t, p)

	if got := lr.assessCommandRiskLLM(context.Background(), "", "mock-model", "mock", "mkdir -p internal/foo"); got != permission.RiskSafe {
		t.Fatalf("verdict = %v, want RiskSafe", got)
	}
	if _, ok := perm.CachedRisk("mkdir -p internal/foo", "mock-model"); ok {
		t.Fatal("a verdict served by the risk profile was cached; it must expire with the profile")
	}

	lr.assessCommandRiskLLM(context.Background(), "", "mock-model", "mock", "mkdir -p internal/foo")
	if got := p.calls; got != 2 {
		t.Errorf("provider calls = %d, want 2 (the second check must reach the model, not a cache)", got)
	}
}

// A verdict that was relayed to the ordinary model is still a durable judgement:
// only the risk profile's own answer is excluded from the cache.
func TestAssessCommandRiskLLMCachesRelayedVerdict(t *testing.T) {
	p := &riskScriptProvider{scripts: [][]provider.StreamEvent{verdictFromEngine("llm", "ASK")}}
	lr, perm := newRiskLoopRunner(t, p)

	lr.assessCommandRiskLLM(context.Background(), "", "mock-model", "mock", "npm install")
	if _, ok := perm.CachedRisk("npm install", "mock-model"); !ok {
		t.Fatal("a relayed verdict was not cached")
	}
	lr.assessCommandRiskLLM(context.Background(), "", "mock-model", "mock", "npm install")
	if got := p.calls; got != 1 {
		t.Errorf("provider calls = %d, want 1 (the second check should hit the cache)", got)
	}
}

// An ASK served by the router's risk profile is cached deliberately: it can
// only make Auto mode ask for confirmation, so a stale one cannot hide intent —
// the worst it costs is a prompt. Only the profile's SAFE must expire with the
// profile, because that is the canary the gate is still being measured against.
func TestAssessCommandRiskLLMCachesRiskProfileAsk(t *testing.T) {
	p := &riskScriptProvider{scripts: [][]provider.StreamEvent{verdictFromEngine("clef", "ASK")}}
	lr, perm := newRiskLoopRunner(t, p)

	if got := lr.assessCommandRiskLLM(context.Background(), "", "mock-model", "mock", "npm install"); got != permission.RiskAsk {
		t.Fatalf("verdict = %v, want RiskAsk", got)
	}
	if _, ok := perm.CachedRisk("npm install", "mock-model"); !ok {
		t.Fatal("an ASK served by the risk profile was not cached; only its SAFE must expire with the profile")
	}
	if got := lr.assessCommandRiskLLM(context.Background(), "", "mock-model", "mock", "npm install"); got != permission.RiskAsk {
		t.Fatalf("cached verdict = %v, want RiskAsk", got)
	}
	if got := p.calls; got != 1 {
		t.Errorf("provider calls = %d, want 1 (the second check should hit the cache)", got)
	}
}

// The gate's request must be routed as risk work: a router maps on the
// X-Client-Task spelling, so a request that arrives as a turn would be sent to
// the wrong model and the gate would lose its dedicated judgement.
func TestRiskRequestCarriesRiskTask(t *testing.T) {
	p := &riskScriptProvider{scripts: [][]provider.StreamEvent{textVerdict("SAFE")}}
	lr, _ := newRiskLoopRunner(t, p)

	lr.assessCommandRiskLLM(context.Background(), "", "mock-model", "mock", "mkdir -p internal/foo")

	reqs := p.requests()
	if len(reqs) != 1 {
		t.Fatalf("provider was called %d times, want 1", len(reqs))
	}
	if got := reqs[0].Task; got != provider.TaskRisk {
		t.Errorf("risk request task = %q, want %q", got, provider.TaskRisk)
	}
}

// engineIsRiskProfile is the cache-only reading of the gateway's engine name.
// A qualified name must still count as the canary, while an unknown one must
// not: it falls through to the old caching behaviour, never to anything safer.
func TestEngineIsRiskProfile(t *testing.T) {
	for _, tc := range []struct {
		engine string
		want   bool
	}{
		{"clef", true},
		{"  CLEF ", true},
		{"clef-2026-11", true},
		{"llm", false},
		{"", false},
		{"mystery-gateway", false},
	} {
		if got := engineIsRiskProfile(tc.engine); got != tc.want {
			t.Errorf("engineIsRiskProfile(%q) = %v, want %v", tc.engine, got, tc.want)
		}
	}
}

// A gateway that starts reporting a different engine name must not change the
// cache decision silently. The warning is one-off, so a chatty router cannot
// flood the log; an expected engine — the risk profile, the relayed model, or
// none at all — stays quiet.
func TestWarnUnrecognisedRiskEngineOnce(t *testing.T) {
	var buf strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	riskEngineWarnOnce = sync.Once{}
	t.Cleanup(func() {
		slog.SetDefault(prev)
		riskEngineWarnOnce = sync.Once{}
	})

	for _, expected := range []string{"", "clef", "clef-2026-11", "  LLM "} {
		warnUnrecognisedRiskEngine(expected)
	}
	if strings.Contains(buf.String(), "unrecognised") {
		t.Fatalf("an expected engine was warned about:\n%s", buf.String())
	}

	warnUnrecognisedRiskEngine("mystery-gateway")
	warnUnrecognisedRiskEngine("another-engine")
	logged := buf.String()
	if got := strings.Count(logged, "unrecognised serving engine"); got != 1 {
		t.Errorf("unrecognised-engine warnings = %d, want exactly 1:\n%s", got, logged)
	}
	if !strings.Contains(logged, "engine=mystery-gateway") {
		t.Errorf("the warning does not name the engine:\n%s", logged)
	}
}
