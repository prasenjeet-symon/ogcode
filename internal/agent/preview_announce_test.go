package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/id"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// announceLoopRunner builds a LoopRunner over a real store holding one session
// that lives in its own project directory — the guard in recordAnnouncedPorts
// only announces for such a session.
func announceLoopRunner(t *testing.T) (*LoopRunner, *session.Session) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	store := session.NewStore(database)
	dir := t.TempDir()
	sess := &session.Session{
		ID: session.NewSessionID(), ProjectID: dir, Directory: dir,
		Title: "t", Model: "mock-model", SessionType: "build",
		CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return &LoopRunner{Store: store, Dir: sess.Directory}, sess
}

// seedAssistantText appends an assistant message carrying one text part.
func seedAssistantText(t *testing.T, store *session.Store, sess *session.Session, text string) {
	t.Helper()
	msg := &session.MessageInfo{ID: id.NewMessageID(), SessionID: sess.ID, Role: session.RoleAssistant}
	if err := store.CreateMessage(msg); err != nil {
		t.Fatalf("create message: %v", err)
	}
	data, err := json.Marshal(session.TextPartData{Text: text})
	if err != nil {
		t.Fatalf("marshal text part: %v", err)
	}
	part := &session.Part{ID: id.NewPartID(), MessageID: msg.ID, SessionID: sess.ID, Type: session.PartText, Data: data}
	if err := store.CreatePart(part); err != nil {
		t.Fatalf("create part: %v", err)
	}
}

func announcedIn(t *testing.T, lr *LoopRunner, dir string) []int {
	t.Helper()
	got, err := lr.Store.AnnouncedPorts(dir)
	if err != nil {
		t.Fatalf("announced ports: %v", err)
	}
	return got
}

// TestRecordAnnouncedPorts pins what a step's prose records: the ports of the
// live-preview URLs in THAT text, for the build agent's own project only. The
// transcript is never read, so a preview URL quoted in an earlier reply —
// debugging output, a test fixture, a discussion of this very bug — is not
// recorded again; under the old whole-history scan such quotes kept dead
// services on the Preview grid forever.
func TestRecordAnnouncedPorts(t *testing.T) {
	t.Run("the step's own URL is recorded, history is not re-read", func(t *testing.T) {
		lr, sess := announceLoopRunner(t)
		seedAssistantText(t, lr.Store, sess,
			"the grid wrongly showed http://9999."+PreviewDomain()+"/ for a dead service")

		lr.recordAnnouncedPorts("build", sess.ID,
			"your dev server is live at http://4321."+PreviewDomain()+":7799/")

		if got := announcedIn(t, lr, sess.Directory); len(got) != 1 || got[0] != 4321 {
			t.Fatalf("got %v, want [4321] — the old reply's 9999 quote must not be recorded", got)
		}
	})

	t.Run("prose without a URL records nothing", func(t *testing.T) {
		lr, sess := announceLoopRunner(t)
		lr.recordAnnouncedPorts("build", sess.ID, "all done, nothing running on 127.0.0.1:5432")
		if got := announcedIn(t, lr, sess.Directory); len(got) != 0 {
			t.Fatalf("got %v, want none", got)
		}
	})

	t.Run("non-build agents record nothing", func(t *testing.T) {
		lr, sess := announceLoopRunner(t)
		lr.recordAnnouncedPorts("task", sess.ID, "serving at http://4321."+PreviewDomain()+":7799/")
		if got := announcedIn(t, lr, sess.Directory); len(got) != 0 {
			t.Fatalf("got %v, want none", got)
		}
	})

	t.Run("a session outside its project records nothing", func(t *testing.T) {
		lr, proj := announceLoopRunner(t)
		// A task session: the project in one field, its own worktree in the other.
		task := &session.Session{
			ID: session.NewSessionID(), ProjectID: proj.Directory, Directory: t.TempDir(),
			Title: "t", Model: "mock-model", SessionType: "build",
			CreatedAt: session.Now(), UpdatedAt: session.Now(),
		}
		if err := lr.Store.Create(task); err != nil {
			t.Fatalf("create task session: %v", err)
		}
		lr.recordAnnouncedPorts("build", task.ID, "serving at http://4321."+PreviewDomain()+":7799/")
		for _, dir := range []string{task.Directory, task.ProjectID} {
			if got := announcedIn(t, lr, dir); len(got) != 0 {
				t.Fatalf("%s: got %v, want none", dir, got)
			}
		}
	})
}

// midTurnAnnounceProvider scripts a two-step turn: the first step names a
// live-preview URL and calls a tool, the second ends the turn without naming it
// again. At the second call it checks whether the port is already recorded —
// the proxy serves only recorded ports, and the user can click the URL while the
// turn is still running.
type midTurnAnnounceProvider struct {
	mu              sync.Mutex
	calls           int
	announce        string
	store           *session.Store
	dir             string
	port            int
	recordedMidTurn bool
	checkedAtSecond bool
}

func (m *midTurnAnnounceProvider) ID() string { return "mock" }
func (m *midTurnAnnounceProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", ProviderID: "mock"}}
}

func (m *midTurnAnnounceProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	m.mu.Lock()
	m.calls++
	call := m.calls
	if call == 2 {
		ports, _ := m.store.AnnouncedPorts(m.dir)
		for _, p := range ports {
			if p == m.port {
				m.recordedMidTurn = true
			}
		}
		m.checkedAtSecond = true
	}
	m.mu.Unlock()

	ch := make(chan provider.StreamEvent, 8)
	go func() {
		defer close(ch)
		if call == 1 {
			ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: m.announce}
			ch <- provider.StreamEvent{Type: provider.EventToolCallStart, ToolCallID: "call_1", ToolName: "grep"}
			ch <- provider.StreamEvent{Type: provider.EventToolCallDelta, ToolCallID: "call_1", ToolInput: []byte(`{}`)}
			ch <- provider.StreamEvent{Type: provider.EventToolCallEnd, ToolCallID: "call_1"}
			fr := "tool_use"
			ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
			return
		}
		ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "All tests pass."}
		fr := "stop"
		ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
	}()
	return ch, nil
}

// TestRunLoop_RecordsPreviewURLMidTurn pins the per-step record through the
// real loop: a URL written in a step that goes on to call a tool is recorded
// before the next step starts — not only if the turn's FINAL reply repeats it,
// which it usually does not ("All tests pass.").
func TestRunLoop_RecordsPreviewURLMidTurn(t *testing.T) {
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
	dir := t.TempDir()

	p := &midTurnAnnounceProvider{
		announce: "The dev server is live at http://4321." + PreviewDomain() + ":7799/ — now the tests.",
		store:    store, dir: dir, port: 4321,
	}
	reg := provider.NewRegistry()
	reg.Register(p)
	tools := tool.NewRegistry()
	tools.Register(noopGrepTool{})

	lr := &LoopRunner{Store: store, Bus: bus.New(64), Registry: reg, Tools: tools, Dir: dir, MaxSteps: 6}
	sess := &session.Session{
		ID: session.NewSessionID(), ProjectID: dir, Directory: dir,
		Title: "t", Model: "mock-model", SessionType: "build",
		CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	runOneTurn(t, lr, sess.ID, "build")

	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.checkedAtSecond {
		t.Fatalf("the scripted turn never reached its second step (calls = %d)", p.calls)
	}
	if !p.recordedMidTurn {
		t.Fatal("port 4321 was not recorded before the next step: a mid-turn preview URL must be served while the turn runs")
	}
	if got := announcedIn(t, lr, dir); len(got) != 1 || got[0] != 4321 {
		t.Fatalf("after the turn got %v, want [4321]", got)
	}
}
