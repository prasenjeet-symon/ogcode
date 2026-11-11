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

// taskWireProvider answers every request with a final text answer and records
// the task kind, complexity and workload each one carried. taskForAgent is
// pure, so a test of the mapping alone stays green if the loop stops putting
// its answer on the request — the wire value is what these tests assert.
type taskWireProvider struct {
	mu           sync.Mutex
	tasks        []provider.Task
	complexities []provider.Complexity
	workloads    []provider.Workload
}

func (p *taskWireProvider) ID() string { return "mock" }
func (p *taskWireProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", ProviderID: "mock"}}
}

func (p *taskWireProvider) StreamChat(_ context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	p.mu.Lock()
	p.tasks = append(p.tasks, req.Task)
	p.complexities = append(p.complexities, req.Complexity)
	p.workloads = append(p.workloads, req.Workload)
	p.mu.Unlock()

	ch := make(chan provider.StreamEvent, 4)
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "final answer"}
		fr := "stop"
		ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
	}()
	return ch, nil
}

func (p *taskWireProvider) recorded() []provider.Task {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.Task{}, p.tasks...)
}

func (p *taskWireProvider) recordedComplexities() []provider.Complexity {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.Complexity{}, p.complexities...)
}

func (p *taskWireProvider) recordedWorkloads() []provider.Workload {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.Workload{}, p.workloads...)
}

func newTaskWireRunner(t *testing.T) (*LoopRunner, *taskWireProvider) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := session.SetModelCapability(database, &session.ModelCapability{ModelID: "mock-model", ProbedAt: session.Now()}); err != nil {
		t.Fatalf("set capability: %v", err)
	}

	p := &taskWireProvider{}
	reg := provider.NewRegistry()
	reg.Register(p)
	lr := &LoopRunner{
		Store: session.NewStore(database), Bus: bus.New(64), Registry: reg,
		Tools: tool.NewRegistry(), Dir: t.TempDir(), MaxSteps: 5,
	}
	return lr, p
}

// TestRunLoopSendsTheTurnTask pins that an agent turn carries its task kind on
// the request the provider receives, not only in taskForAgent's mapping.
func TestRunLoopSendsTheTurnTask(t *testing.T) {
	lr, p := newTaskWireRunner(t)
	sess := &session.Session{
		ID: session.NewSessionID(), ProjectID: "p", Directory: t.TempDir(), Title: "t",
		Model: "mock-model", Provider: "mock", SessionType: "build",
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

	tasks := p.recorded()
	if len(tasks) == 0 {
		t.Fatal("no request reached the provider")
	}
	for i, task := range tasks {
		if task != provider.TaskTurn {
			t.Errorf("request %d task = %q, want %q", i, task, provider.TaskTurn)
		}
	}
}

// TestRunTaskSessionSendsTheSubagentTask pins that a delegated sub-agent's
// requests are routed as subagent work rather than as a user turn, and that the
// spawner's difficulty and workload predictions reach the wire alongside it.
func TestRunTaskSessionSendsTheSubagentTask(t *testing.T) {
	lr, p := newTaskWireRunner(t)
	dir := t.TempDir()

	type res struct {
		answer string
		err    error
	}
	done := make(chan res, 1)
	go func() {
		a, e := lr.RunTaskSession(context.Background(), "investigate X", "find where X is used and report back", dir, "mock-model", "", provider.ComplexityHard, provider.WorkloadReadHeavy)
		done <- res{a, e}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("RunTaskSession error: %v", r.err)
		}
		if r.answer != "final answer" {
			t.Errorf("RunTaskSession answer = %q, want %q", r.answer, "final answer")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunTaskSession did not complete in time")
	}

	tasks := p.recorded()
	if len(tasks) == 0 {
		t.Fatal("no request reached the provider")
	}
	for i, task := range tasks {
		if task != provider.TaskSubagent {
			t.Errorf("request %d task = %q, want %q", i, task, provider.TaskSubagent)
		}
	}
	complexities := p.recordedComplexities()
	if len(complexities) != len(tasks) {
		t.Fatalf("recorded %d complexities for %d requests", len(complexities), len(tasks))
	}
	for i, c := range complexities {
		if c != provider.ComplexityHard {
			t.Errorf("request %d complexity = %q, want %q", i, c, provider.ComplexityHard)
		}
	}
	workloads := p.recordedWorkloads()
	if len(workloads) != len(tasks) {
		t.Fatalf("recorded %d workloads for %d requests", len(workloads), len(tasks))
	}
	for i, w := range workloads {
		if w != provider.WorkloadReadHeavy {
			t.Errorf("request %d workload = %q, want %q", i, w, provider.WorkloadReadHeavy)
		}
	}
}
