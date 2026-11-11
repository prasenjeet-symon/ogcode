package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/agent"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// complexityUpstream is a stub OpenAI-compatible endpoint that records the
// headers of every request it receives and answers with one final text, so the
// child's loop ends on its first step.
type complexityUpstream struct {
	mu      sync.Mutex
	headers []http.Header
	srv     *httptest.Server
}

func newComplexityUpstream(t *testing.T) *complexityUpstream {
	t.Helper()
	u := &complexityUpstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.headers = append(u.headers, r.Header.Clone())
		u.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(u.srv.Close)
	return u
}

// headerValues returns the value of name on every recorded request, in arrival
// order.
func (u *complexityUpstream) headerValues(name string) []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]string, 0, len(u.headers))
	for _, h := range u.headers {
		out = append(out, h.Get(name))
	}
	return out
}

// TestSpawnedTeammateSendsTheSpawnersComplexityPrediction is the end-to-end
// check for the spawn-time classification: a teammate spawned with a class is
// created with it, and its own model requests reach the provider carrying
// X-Client-Complexity beside the task kind the loop normally sends.
func TestSpawnedTeammateSendsTheSpawnersComplexityPrediction(t *testing.T) {
	srv := boardTestServer(t)
	srv.store = session.NewStore(srv.db)
	srv.mu.Lock()
	srv.running = map[session.SessionID]context.CancelFunc{}
	srv.runningToken = map[session.SessionID]uint64{}
	srv.runningDone = map[session.SessionID]chan struct{}{}
	srv.loopControls = map[session.SessionID]*agent.LoopControl{}
	srv.mu.Unlock()

	upstream := newComplexityUpstream(t)
	p, err := provider.NewOpenAICompatibleProvider("mock", "sk-test", upstream.srv.URL, "mock-model", "")
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	srv.registry.Register(p)
	// Recorded capability keeps the loop from probing the endpoint first, so the
	// only requests the stub sees are real turns.
	if err := session.SetModelCapability(srv.db, &session.ModelCapability{ModelID: "mock-model", ProbedAt: session.Now()}); err != nil {
		t.Fatalf("set capability: %v", err)
	}
	srv.loopRunner = &agent.LoopRunner{
		Store: srv.store, Bus: srv.bus, Registry: srv.registry,
		Tools: tool.NewRegistry(), Dir: srv.dir,
	}

	ctx := context.Background()
	parent := &session.Session{
		ID: session.NewSessionID(), ProjectID: srv.dir, Directory: srv.dir, Title: "parent",
		Model: "mock-model", Provider: "mock", SessionType: "build",
		CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}
	if err := srv.store.Create(parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	parentHandle, err := srv.joinBoard(ctx, parent.ID, "build")
	if err != nil {
		t.Fatalf("join parent: %v", err)
	}

	res, err := srv.spawnTeammate(ctx, parent, parentHandle, tool.TeammateRequest{
		Op: "spawn", Name: "easy-worker", Prompt: "rename one symbol in one file",
		Complexity: provider.ComplexityEasy,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if res.Handle == "" {
		t.Fatalf("spawn refused: %s", res.Notice)
	}

	childID := session.SessionID(res.SessionID)
	child, err := srv.store.Get(childID)
	if err != nil || child == nil {
		t.Fatalf("get child: %v", err)
	}
	if child.Complexity != string(provider.ComplexityEasy) {
		t.Errorf("child session complexity = %q, want %q", child.Complexity, provider.ComplexityEasy)
	}

	// Let the child's turn run to completion before reading the wire log.
	deadline := time.Now().Add(15 * time.Second)
	for srv.sessionRunning(childID) {
		if time.Now().After(deadline) {
			t.Fatal("the teammate's loop did not exit")
		}
		time.Sleep(10 * time.Millisecond)
	}

	values := upstream.headerValues("X-Client-Complexity")
	if len(values) == 0 {
		t.Fatal("no request reached the provider")
	}
	for i, v := range values {
		if v != string(provider.ComplexityEasy) {
			t.Errorf("request %d carried X-Client-Complexity %q, want %q", i, v, provider.ComplexityEasy)
		}
	}
}

// TestSpawnedTeammateWithoutAPredictionSendsNoComplexityHeader is the counterpart
// decision: an unclassified spawn is not refused, and its requests carry no
// invented class.
func TestSpawnedTeammateWithoutAPredictionSendsNoComplexityHeader(t *testing.T) {
	srv := boardTestServer(t)
	srv.store = session.NewStore(srv.db)
	srv.mu.Lock()
	srv.running = map[session.SessionID]context.CancelFunc{}
	srv.runningToken = map[session.SessionID]uint64{}
	srv.runningDone = map[session.SessionID]chan struct{}{}
	srv.loopControls = map[session.SessionID]*agent.LoopControl{}
	srv.mu.Unlock()

	upstream := newComplexityUpstream(t)
	p, err := provider.NewOpenAICompatibleProvider("mock", "sk-test", upstream.srv.URL, "mock-model", "")
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	srv.registry.Register(p)
	if err := session.SetModelCapability(srv.db, &session.ModelCapability{ModelID: "mock-model", ProbedAt: session.Now()}); err != nil {
		t.Fatalf("set capability: %v", err)
	}
	srv.loopRunner = &agent.LoopRunner{
		Store: srv.store, Bus: srv.bus, Registry: srv.registry,
		Tools: tool.NewRegistry(), Dir: srv.dir,
	}

	ctx := context.Background()
	parent := &session.Session{
		ID: session.NewSessionID(), ProjectID: srv.dir, Directory: srv.dir, Title: "parent",
		Model: "mock-model", Provider: "mock", SessionType: "build",
		CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}
	if err := srv.store.Create(parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	parentHandle, err := srv.joinBoard(ctx, parent.ID, "build")
	if err != nil {
		t.Fatalf("join parent: %v", err)
	}

	res, err := srv.spawnTeammate(ctx, parent, parentHandle, tool.TeammateRequest{
		Op: "spawn", Name: "unclassified-worker", Prompt: "look around",
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if res.Handle == "" {
		t.Fatalf("spawn refused: %s", res.Notice)
	}

	childID := session.SessionID(res.SessionID)
	deadline := time.Now().Add(15 * time.Second)
	for srv.sessionRunning(childID) {
		if time.Now().After(deadline) {
			t.Fatal("the teammate's loop did not exit")
		}
		time.Sleep(10 * time.Millisecond)
	}

	values := upstream.headerValues("X-Client-Complexity")
	if len(values) == 0 {
		t.Fatal("no request reached the provider")
	}
	for i, v := range values {
		if v != "" {
			t.Errorf("request %d carried an invented X-Client-Complexity %q", i, v)
		}
	}
}
