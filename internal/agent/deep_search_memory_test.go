package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/filechange"
	"github.com/prasenjeet-symon/ogcode/internal/memfile"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// deepSearchTurnProvider runs one deep_search round and then answers with text,
// which is the shape of a turn that researched something.
type deepSearchTurnProvider struct {
	mu    sync.Mutex
	calls int
}

func (m *deepSearchTurnProvider) ID() string { return "mock" }
func (m *deepSearchTurnProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", ProviderID: "mock"}}
}

func (m *deepSearchTurnProvider) StreamChat(_ context.Context, _ provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	m.mu.Lock()
	m.calls++
	call := m.calls
	m.mu.Unlock()

	ch := make(chan provider.StreamEvent, 8)
	go func() {
		defer close(ch)
		fr := "stop"
		if call == 1 {
			ch <- provider.StreamEvent{Type: provider.EventToolCallStart, ToolCallID: "call_deep", ToolName: "deep_search"}
			ch <- provider.StreamEvent{Type: provider.EventToolCallDelta, ToolCallID: "call_deep", ToolInput: []byte(`{"query":"what is ogcode"}`)}
			ch <- provider.StreamEvent{Type: provider.EventToolCallEnd, ToolCallID: "call_deep"}
			fr = "tool_use"
		} else {
			ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "final answer"}
		}
		ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
	}()
	return ch, nil
}

// TestRunLoopRecordsDeepSearchInSessionMemory is the end-to-end check for the
// session-memory note: a turn that ran deep_search must leave one line about
// the question in the conversation's session memory file, so the next turn
// knows that research already happened. Only that file — project MEMORY.md is
// never touched by the loop.
func TestRunLoopRecordsDeepSearchInSessionMemory(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := session.SetModelCapability(database, &session.ModelCapability{ModelID: "mock-model", ProbedAt: session.Now()}); err != nil {
		t.Fatalf("set capability: %v", err)
	}

	reg := provider.NewRegistry()
	reg.Register(&deepSearchTurnProvider{})
	tools := tool.NewRegistry()
	tools.Register(tool.DeepSearchTool{Run: func(context.Context, string, string, string, string) (string, error) {
		return "ogcode is an agent", nil
	}})

	dir := t.TempDir()
	lr := &LoopRunner{
		Store: session.NewStore(database), Bus: bus.New(64), Registry: reg, Tools: tools,
		Dir: dir, MaxSteps: 5,
	}
	sess := &session.Session{
		ID: session.NewSessionID(), ProjectID: dir, Directory: dir, Title: "t",
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
	text, _ := json.Marshal(session.TextPartData{Text: "research ogcode"})
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

	raw, err := os.ReadFile(memfile.SessionMemoryPath(dir, string(sess.ID)))
	if err != nil {
		t.Fatalf("the turn's deep search must be noted in session memory: %v", err)
	}
	body := string(raw)
	for _, want := range []string{memfile.DeepSearchHeading, `"what is ogcode"`, "live research"} {
		if !strings.Contains(body, want) {
			t.Errorf("session memory is missing %q:\n%s", want, body)
		}
	}
	if project, err := os.ReadFile(filepath.Join(dir, "MEMORY.md")); err == nil {
		if strings.Contains(string(project), "what is ogcode") {
			t.Error("the loop wrote research into project MEMORY.md; only session memory may carry it")
		}
	}
}

// TestRecordDeepSearchesWritesTheConversationFile pins the two things a
// teammate's research depends on: it lands in the ROOT session's file — the
// one the conversation's prompt loads next turn — and it is recorded in the
// file change gate, so the session's next shell command does not report the
// loop's own write as an unattributed change.
func TestRecordDeepSearchesWritesTheConversationFile(t *testing.T) {
	dir := t.TempDir()
	root := session.NewSessionID()
	child := session.NewSessionID()
	log := filechange.NewLog(context.Background(), nil)
	lr := &LoopRunner{
		Dir:     dir,
		FileLog: log,
		ParentSessionOf: func(id session.SessionID) (session.SessionID, string) {
			if id == child {
				return root, "build"
			}
			return "", ""
		},
	}

	// A turn that searched nothing must leave no file behind at all.
	lr.recordDeepSearches(context.Background(), child, nil, dir, "build")
	if _, err := os.Stat(memfile.SessionMemoryPath(dir, string(root))); !os.IsNotExist(err) {
		t.Fatalf("no notes must write no file, got err %v", err)
	}

	lr.recordDeepSearches(context.Background(), child, []memfile.DeepSearchNote{
		{Query: "incus worker isolation", At: time.Now()},
	}, dir, "build")

	rootPath := memfile.SessionMemoryPath(dir, string(root))
	raw, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatalf("a teammate's research belongs in the conversation's file: %v", err)
	}
	if !strings.Contains(string(raw), "incus worker isolation") {
		t.Errorf("root session memory is missing the query:\n%s", raw)
	}
	if _, err := os.Stat(memfile.SessionMemoryPath(dir, string(child))); !os.IsNotExist(err) {
		t.Errorf("the teammate's own file must stay untouched, got err %v", err)
	}
	if rev := log.Rev(tool.FileKey(dir, rootPath)); rev == 0 {
		t.Error("the loop's write must be recorded in the file change gate")
	}
}
