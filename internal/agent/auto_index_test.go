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

// autoIndexProvider answers every call with one plain text delta and a "stop"
// finish, so a turn completes without a tool call.
type autoIndexProvider struct {
	mu     sync.Mutex
	answer string
}

func (m *autoIndexProvider) ID() string { return "mock" }
func (m *autoIndexProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", ProviderID: "mock"}}
}

func (m *autoIndexProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent, 4)
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: m.answer}
		fr := "stop"
		ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
	}()
	return ch, nil
}

// autoIndexCase describes the session a turn runs against. directory and
// projectID are separate so a test can reproduce a task session, which stores
// the project in one field and its own worktree in the other.
type autoIndexCase struct {
	sessionType string
	directory   string
	projectID   string
	model       string
	provider    string
}

// autoIndexFixture builds a LoopRunner whose AutoIndex records the endpoint it
// was handed. Returns the runner, the session id, and the project directory the
// session points at.
func autoIndexFixture(t *testing.T, c autoIndexCase, autoIndex func(string, string, string)) (*LoopRunner, session.SessionID, string) {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if err := session.SetModelCapability(database, &session.ModelCapability{
		ModelID: "mock-model", SupportsImages: false, ProbedAt: session.Now(),
	}); err != nil {
		t.Fatalf("set capability: %v", err)
	}

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	reg.Register(&autoIndexProvider{answer: "done"})

	model := c.model
	if model == "" {
		model = "mock-model"
	}

	dir := t.TempDir()
	lr := &LoopRunner{
		Store: store, Bus: bus.New(64), Registry: reg, Tools: tool.NewRegistry(),
		Dir: dir, MaxSteps: 4,
		AutoIndex: autoIndex,
	}

	sessDir := c.directory
	if sessDir == "" {
		sessDir = dir
	}
	sessProject := c.projectID
	if sessProject == "" {
		sessProject = dir
	}

	sess := &session.Session{
		ID: session.NewSessionID(), ProjectID: sessProject, Directory: sessDir,
		Title: "t", Model: model, Provider: c.provider, SessionType: c.sessionType,
		CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	return lr, sess.ID, dir
}

// runOneTurn posts a plain user message and runs the loop as the named agent.
func runOneTurn(t *testing.T, lr *LoopRunner, sessionID session.SessionID, agentName string) {
	t.Helper()

	um := &session.MessageInfo{ID: session.NewMessageID(), SessionID: sessionID, Role: session.RoleUser, CreatedAt: session.Now()}
	if err := lr.Store.CreateMessage(um); err != nil {
		t.Fatalf("create user msg: %v", err)
	}
	td, _ := json.Marshal(session.TextPartData{Text: "hello"})
	if err := lr.Store.CreatePart(&session.Part{
		ID: session.NewPartID(), MessageID: um.ID, SessionID: sessionID,
		Type: session.PartText, Data: td, CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}); err != nil {
		t.Fatalf("create user part: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- lr.RunLoop(context.Background(), sessionID, agentName, 0, 0) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunLoop error: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunLoop did not complete in time")
	}
}

// A completed interactive build turn must refresh the project index, on the
// session's own directory — the automatic half of the feature.
func TestRunLoop_AutoIndexesAfterACompletedTurn(t *testing.T) {
	called := make(chan string, 4)
	lr, sessionID, dir := autoIndexFixture(t, autoIndexCase{sessionType: "build"}, func(d, _, _ string) { called <- d })

	runOneTurn(t, lr, sessionID, "build")

	select {
	case got := <-called:
		if got != dir {
			t.Errorf("AutoIndex got dir %q, want the project directory %q", got, dir)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AutoIndex was not called after a completed turn")
	}
}

// Only the interactive build agent may refresh the index. The indexer drives its
// own turns through this loop under the "index" agent, so indexing on those
// would recurse into an index of the index, without bound.
func TestRunLoop_AutoIndexSkipsNonBuildAgents(t *testing.T) {
	for _, agentName := range []string{"index", "plan", "note", "breakdown", "task", "search"} {
		t.Run(agentName, func(t *testing.T) {
			called := make(chan string, 4)
			lr, sessionID, _ := autoIndexFixture(t, autoIndexCase{sessionType: agentName}, func(d, _, _ string) { called <- d })

			runOneTurn(t, lr, sessionID, agentName)

			select {
			case d := <-called:
				t.Fatalf("AutoIndex fired for the %s agent (dir %q); it must not", agentName, d)
			case <-time.After(300 * time.Millisecond):
				// The turn ended and nothing fired: correct.
			}
		})
	}
}

// A session whose directory is not its project is working in a worktree. Its
// turn must not refresh the project index: every file in a fresh checkout is new
// to the index, so one turn would index a whole duplicate tree.
func TestRunLoop_AutoIndexSkipsWorktreeSessions(t *testing.T) {
	called := make(chan string, 4)
	worktree := t.TempDir()

	// A task session: stored as "build", but run as the "task" agent and
	// pointing at its own worktree rather than the project.
	lr, sessionID, _ := autoIndexFixture(t, autoIndexCase{
		sessionType: "build",
		directory:   worktree,
		projectID:   "/some/project",
	}, func(d, _, _ string) { called <- d })

	runOneTurn(t, lr, sessionID, "build")

	select {
	case d := <-called:
		t.Fatalf("AutoIndex fired for a worktree session (dir %q); the project index must not import a worktree", d)
	case <-time.After(300 * time.Millisecond):
		// Correct.
	}
}

// The refresh must inherit the model and provider the finished turn ran on.
// An index session created with neither resolves through the registry default,
// which can be a different provider than the session it is catching up with —
// the bug this pins: the automatic index spent its tokens on whatever provider
// happened to be the registry default rather than the one the user was using.
//
// The provider asserted is the one the turn actually resolved to (the fixture's
// provider id), which is what the loop hands down — not merely the value stored
// on the session.
func TestRunLoop_AutoIndexInheritsTheTurnEndpoint(t *testing.T) {
	type endpoint struct{ model, provider string }
	called := make(chan endpoint, 4)
	lr, sessionID, _ := autoIndexFixture(t, autoIndexCase{
		sessionType: "build",
		model:       "mock-model",
	}, func(_, model, provider string) { called <- endpoint{model, provider} })

	runOneTurn(t, lr, sessionID, "build")

	select {
	case got := <-called:
		if got.model != "mock-model" {
			t.Errorf("AutoIndex model = %q, want the session's model %q", got.model, "mock-model")
		}
		if got.provider != "mock" {
			t.Errorf("AutoIndex provider = %q, want the provider the turn resolved to %q", got.provider, "mock")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AutoIndex was not called after a completed turn")
	}
}

// A nil callback is the CLI and the test default, and must leave the loop
// exactly as it was.
func TestRunLoop_NoAutoIndexIsHarmless(t *testing.T) {
	called := make(chan string, 4)
	lr, sessionID, _ := autoIndexFixture(t, autoIndexCase{sessionType: "build"}, nil)
	runOneTurn(t, lr, sessionID, "build")

	select {
	case d := <-called:
		t.Fatalf("AutoIndex fired with a nil callback: %q", d)
	default:
	}
}
