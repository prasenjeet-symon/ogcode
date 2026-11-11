package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/permission"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// utilityLoopRunner builds a LoopRunner with a real store and bus holding one
// session, plus a provider scripted to answer the risk gate. It lets a test
// watch what a utility call does to the session row and the event stream.
func utilityLoopRunner(t *testing.T, p provider.Provider) (*LoopRunner, session.SessionID, *bus.Bus) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	store := session.NewStore(database)
	sess := &session.Session{
		ID: session.NewSessionID(), ProjectID: "p", Directory: t.TempDir(),
		Title: "t", Model: "mock-model", SessionType: "build",
		CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	reg := provider.NewRegistry()
	reg.Register(p)
	b := bus.New(16)
	lr := &LoopRunner{
		Store: store, Bus: b, Registry: reg, Dir: sess.Directory,
		Permissions: permission.NewManager(nil),
	}
	return lr, sess.ID, b
}

// TestAssessCommandRiskLLMRecordsUtilityUsage pins that the risk gate's tokens
// land on the session row. They arrive on the risk call's own stream, which the
// main-turn accounting never sees, so before utility accounting existed every
// Auto-mode risk check spent tokens that appeared in no total.
func TestAssessCommandRiskLLMRecordsUtilityUsage(t *testing.T) {
	p := &riskScriptProvider{scripts: [][]provider.StreamEvent{
		textVerdictWithUsage("SAFE", &provider.TokenUsage{InputTokens: 120, OutputTokens: 4, CacheReadTokens: 100}),
	}}
	lr, id, b := utilityLoopRunner(t, p)

	before, err := lr.Store.Get(id)
	if err != nil {
		t.Fatalf("get before: %v", err)
	}
	if before.UtilityTokens != nil {
		t.Fatalf("session started with utility usage: %+v", before.UtilityTokens)
	}

	watched := b.SubscribeAll()
	defer b.Unsubscribe(watched)
	if got := lr.assessCommandRiskLLM(context.Background(), id, "mock-model", "mock", "mkdir -p internal/foo"); got != permission.RiskSafe {
		t.Fatalf("verdict = %v, want RiskSafe", got)
	}

	after, err := lr.Store.Get(id)
	if err != nil {
		t.Fatalf("get after: %v", err)
	}
	if after.UtilityTokens == nil {
		t.Fatal("the risk call spent tokens but none were recorded")
	}
	u := after.UtilityTokens
	if u.Input != 120 || u.Output != 4 || u.CacheRead != 100 {
		t.Errorf("utility tokens = %+v, want input=120 output=4 cacheRead=100", u)
	}

	// The new figure must be announced so an open token view picks it up — it
	// lives on the session row, so no message.updated carries it.
	select {
	case evt := <-watched:
		if evt.Type != "session.updated" {
			t.Errorf("published %q, want session.updated", evt.Type)
		}
	default:
		t.Error("no session.updated was published after utility usage landed")
	}
}

// bgModelProvider is the scripted risk fake plus a second, smaller model. The
// background-model tests need a configured model the registry can actually
// serve, and riskScriptProvider lists only the session's.
type bgModelProvider struct{ *riskScriptProvider }

func (p *bgModelProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{
		{ID: "mock-model", ProviderID: "mock"},
		{ID: "bg-haiku-model", ProviderID: "mock"},
	}
}

func TestBackgroundModelOverridesUtilityCalls(t *testing.T) {
	p := &bgModelProvider{riskScriptProvider: &riskScriptProvider{scripts: [][]provider.StreamEvent{
		textVerdict("SAFE"),
		textVerdict("SAFE"),
	}}}
	lr, id, _ := utilityLoopRunner(t, p)

	// 1. When BackgroundModel returns a configured model on a reachable provider,
	// behind-the-scenes calls use that model instead of the session's model.
	lr.BackgroundModel = func() (string, string) {
		return "bg-haiku-model", "mock"
	}
	if got := lr.assessCommandRiskLLM(context.Background(), id, "session-main-model", "mock", "mkdir -p internal/foo"); got != permission.RiskSafe {
		t.Fatalf("verdict = %v, want RiskSafe", got)
	}
	reqs := p.requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	if reqs[0].Model != "bg-haiku-model" {
		t.Fatalf("risk call model = %q, want bg-haiku-model", reqs[0].Model)
	}

	// 2. When BackgroundModel returns empty ("inherit from session"), calls use
	// the session's model.
	lr.BackgroundModel = func() (string, string) {
		return "", ""
	}
	if got := lr.assessCommandRiskLLM(context.Background(), id, "session-main-model", "mock", "mkdir -p internal/bar"); got != permission.RiskSafe {
		t.Fatalf("verdict = %v, want RiskSafe", got)
	}
	reqs = p.requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	if reqs[1].Model != "session-main-model" {
		t.Fatalf("inherited risk call model = %q, want session-main-model", reqs[1].Model)
	}
}

// TestBackgroundModelRequiresAServedModel pins the fallback branch: a
// configured background model that nothing serves must leave the inherited
// (model, providerID) untouched, so the call still runs on the session's model
// instead of failing on a misspelled or removed one. An available model wins.
func TestBackgroundModelRequiresAServedModel(t *testing.T) {
	p := &bgModelProvider{riskScriptProvider: &riskScriptProvider{scripts: [][]provider.StreamEvent{
		textVerdict("SAFE"),
	}}}
	lr, id, _ := utilityLoopRunner(t, p)

	tests := []struct {
		name      string
		bgModel   string
		bgProv    string
		wantModel string
		wantProv  string
	}{
		{"served by the named provider", "bg-haiku-model", "mock", "bg-haiku-model", "mock"},
		{"served without naming a provider", "bg-haiku-model", "", "bg-haiku-model", ""},
		{"no provider serves it", "no-such-model", "mock", "session-main-model", "mock"},
		{"served, but not by the named provider", "bg-haiku-model", "other", "session-main-model", "mock"},
		{"empty model inherits", "", "", "session-main-model", "mock"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lr.BackgroundModel = func() (string, string) { return tt.bgModel, tt.bgProv }
			gotModel, gotProv := lr.resolveBackgroundModel("session-main-model", "mock")
			if gotModel != tt.wantModel || gotProv != tt.wantProv {
				t.Errorf("resolveBackgroundModel = (%q, %q), want (%q, %q)", gotModel, gotProv, tt.wantModel, tt.wantProv)
			}
		})
	}

	// End to end: the unavailable model never reaches the wire, so the gate runs
	// on the session's model rather than failing on it.
	lr.BackgroundModel = func() (string, string) { return "no-such-model", "mock" }
	if got := lr.assessCommandRiskLLM(context.Background(), id, "session-main-model", "mock", "mkdir -p internal/unavailable"); got != permission.RiskSafe {
		t.Fatalf("verdict = %v, want RiskSafe", got)
	}
	reqs := p.requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	if reqs[0].Model != "session-main-model" {
		t.Fatalf("risk call model = %q, want the inherited session-main-model", reqs[0].Model)
	}
}
