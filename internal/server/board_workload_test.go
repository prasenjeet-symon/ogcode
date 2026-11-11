package server

import (
	"context"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/agent"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// TestSpawnedTeammateSendsTheSpawnersWorkloadPrediction is the end-to-end check
// for the spawn-time workload class: a teammate spawned with one is created with
// it, and its own model requests reach the provider carrying X-Client-Workload
// beside the task kind the loop normally sends.
func TestSpawnedTeammateSendsTheSpawnersWorkloadPrediction(t *testing.T) {
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
		Op: "spawn", Name: "reader", Prompt: "read the parser and report how it works",
		Workload: provider.WorkloadReadHeavy,
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
	if child.Workload != string(provider.WorkloadReadHeavy) {
		t.Errorf("child session workload = %q, want %q", child.Workload, provider.WorkloadReadHeavy)
	}

	// Let the child's turn run to completion before reading the wire log.
	deadline := time.Now().Add(15 * time.Second)
	for srv.sessionRunning(childID) {
		if time.Now().After(deadline) {
			t.Fatal("the teammate's loop did not exit")
		}
		time.Sleep(10 * time.Millisecond)
	}

	values := upstream.headerValues("X-Client-Workload")
	if len(values) == 0 {
		t.Fatal("no request reached the provider")
	}
	for i, v := range values {
		if v != string(provider.WorkloadReadHeavy) {
			t.Errorf("request %d carried X-Client-Workload %q, want %q", i, v, provider.WorkloadReadHeavy)
		}
	}
}

// TestSpawnedTeammateWithoutAWorkloadPredictionSendsNoWorkloadHeader is the
// counterpart decision: an unclassified spawn is not refused, and its requests
// carry no invented workload.
func TestSpawnedTeammateWithoutAWorkloadPredictionSendsNoWorkloadHeader(t *testing.T) {
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

	values := upstream.headerValues("X-Client-Workload")
	if len(values) == 0 {
		t.Fatal("no request reached the provider")
	}
	for i, v := range values {
		if v != "" {
			t.Errorf("request %d carried an invented X-Client-Workload %q", i, v)
		}
	}
}
