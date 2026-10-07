package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// effortProvider answers every request with text and records the effort each
// one asked for. It takes low and high only, so a stored level outside that
// set shows whether the loop checks before sending.
type effortProvider struct {
	mu      sync.Mutex
	efforts []string
	think   []bool
}

func (p *effortProvider) ID() string { return "mock" }
func (p *effortProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", ProviderID: "mock"}}
}
func (p *effortProvider) EffortSpec(string) provider.EffortSpec {
	return provider.EffortSpec{Levels: []string{provider.EffortLow, provider.EffortHigh}}
}
func (p *effortProvider) StreamChat(_ context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	p.mu.Lock()
	p.efforts = append(p.efforts, req.Effort)
	p.think = append(p.think, req.Thinking)
	p.mu.Unlock()
	ch := make(chan provider.StreamEvent, 4)
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "done"}
		fr := "stop"
		ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
	}()
	return ch, nil
}

// agentEfforts returns the efforts of the requests that asked for thinking —
// the agent loop's own steps, not utility calls.
func (p *effortProvider) agentEfforts() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for i, e := range p.efforts {
		if p.think[i] {
			out = append(out, e)
		}
	}
	return out
}

func newEffortRunner(t *testing.T) (*LoopRunner, *effortProvider) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := session.SetModelCapability(database, &session.ModelCapability{ModelID: "mock-model", ProbedAt: session.Now()}); err != nil {
		t.Fatalf("set capability: %v", err)
	}
	mock := &effortProvider{}
	reg := provider.NewRegistry()
	reg.Register(mock)
	lr := &LoopRunner{
		Store: session.NewStore(database), Bus: bus.New(64), Registry: reg, Tools: tool.NewRegistry(),
		Dir: t.TempDir(), MaxSteps: 5,
	}
	return lr, mock
}

func runEffortTurn(t *testing.T, lr *LoopRunner, effort string) {
	t.Helper()
	sess := &session.Session{
		ID: session.NewSessionID(), ProjectID: "p", Directory: t.TempDir(), Title: "t",
		Model: "mock-model", Provider: "mock", Effort: effort, SessionType: "build",
		CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}
	if err := lr.Store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	user := &session.MessageInfo{ID: session.NewMessageID(), SessionID: sess.ID, Role: session.RoleUser, CreatedAt: session.Now()}
	if err := lr.Store.CreateMessage(user); err != nil {
		t.Fatalf("create user msg: %v", err)
	}
	text, _ := json.Marshal(session.TextPartData{Text: "go"})
	if err := lr.Store.CreatePart(&session.Part{
		ID: session.NewPartID(), MessageID: user.ID, SessionID: sess.ID, Type: session.PartText,
		Data: text, CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}); err != nil {
		t.Fatalf("create user part: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- lr.RunLoop(context.Background(), sess.ID, "build", 0, 0) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunLoop: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunLoop did not complete in time")
	}
}

// The session's effort is what every agent step asks for — once checked
// against the model, since a level it does not take fails the request.
func TestRunLoopSendsTheSessionsEffort(t *testing.T) {
	t.Run("a level the model takes", func(t *testing.T) {
		lr, mock := newEffortRunner(t)
		runEffortTurn(t, lr, provider.EffortHigh)
		got := mock.agentEfforts()
		if len(got) == 0 {
			t.Fatal("no agent step reached the provider")
		}
		for _, e := range got {
			if e != provider.EffortHigh {
				t.Fatalf("agent step effort = %q, want high", e)
			}
		}
	})
	t.Run("a level it does not take runs at its default", func(t *testing.T) {
		lr, mock := newEffortRunner(t)
		runEffortTurn(t, lr, provider.EffortMax)
		for _, e := range mock.agentEfforts() {
			if e != "" {
				t.Fatalf("agent step effort = %q, want the default (empty)", e)
			}
		}
	})
}

// A task sub-agent does part of the user's own work, so it runs at the effort
// the delegating turn runs at.
func TestSubagentInheritsTheRunsEffort(t *testing.T) {
	lr, mock := newEffortRunner(t)
	ctx := withRunEffort(context.Background(), provider.EffortLow)
	if _, err := lr.RunTaskSession(ctx, "look", "look around", t.TempDir(), "mock-model", "mock"); err != nil {
		t.Fatalf("RunTaskSession: %v", err)
	}
	got := mock.agentEfforts()
	if len(got) == 0 || got[0] != provider.EffortLow {
		t.Fatalf("sub-agent step efforts = %v, want low", got)
	}
}
