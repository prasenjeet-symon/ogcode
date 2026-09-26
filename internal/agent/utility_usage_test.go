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
