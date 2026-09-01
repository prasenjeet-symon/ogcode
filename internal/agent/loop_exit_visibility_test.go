package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// awaitLoopDone reads events until the loop.done arrives, and returns its
// properties. Other events (message.updated and friends) are skipped.
func awaitLoopDone(t *testing.T, ch <-chan bus.Event) map[string]string {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt := <-ch:
			if evt.Type != "loop.done" {
				continue
			}
			var props map[string]string
			if err := json.Unmarshal(evt.Properties, &props); err != nil {
				t.Fatalf("unmarshal loop.done properties: %v", err)
			}
			return props
		case <-deadline:
			t.Fatal("timed out waiting for loop.done")
			return nil
		}
	}
}

// A panic anywhere on the interactive loop's path used to take the whole server
// process down: tool execution recovers, and the task runner recovers around its
// own call, but nothing recovered for a panic in setup, prompt assembly or
// compaction. RunLoop must turn it into an ordinary error return instead.
func TestRunLoopRecoversPanicInsteadOfCrashingTheProcess(t *testing.T) {
	b := bus.New(64)
	events := b.SubscribeAll()
	// A nil Store panics on the first call RunLoop makes against it.
	lr := &LoopRunner{Bus: b}

	err := lr.RunLoop(context.Background(), session.SessionID("s-panic"), "build", 0, 0)

	if err == nil {
		t.Fatal("RunLoop returned nil after a panic; the panic escaped or was swallowed")
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Errorf("error = %q, want it to name the panic", err)
	}

	props := awaitLoopDone(t, events)
	if props["reason"] != "panic" {
		t.Errorf("loop.done reason = %q, want %q", props["reason"], "panic")
	}
	if props["error"] == "" {
		t.Error("loop.done carried no error text, so the client has nothing to show")
	}
}

// The failure that motivated this: an error raised before any assistant message
// exists has nothing in the transcript to attach itself to, and used to reach
// only a slog line in the server's stdout. The client saw the loop stop for no
// stated reason. loop.done has to carry the text.
func TestRunLoopDoneCarriesTheErrorText(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	b := bus.New(64)
	events := b.SubscribeAll()
	lr := &LoopRunner{Store: session.NewStore(database), Bus: b}
	// Closing the DB makes the first session read fail with a real error rather
	// than a panic, which is the ordinary early-return shape.
	if err := database.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	runErr := lr.RunLoop(context.Background(), session.SessionID("s-err"), "build", 0, 0)
	if runErr == nil {
		t.Fatal("RunLoop returned nil against a closed database")
	}

	props := awaitLoopDone(t, events)
	if props["error"] == "" {
		t.Fatal("loop.done carried no error text")
	}
	if !strings.Contains(props["error"], runErr.Error()) {
		t.Errorf("loop.done error = %q, want it to contain the returned error %q", props["error"], runErr)
	}
	if props["sessionId"] != "s-err" {
		t.Errorf("loop.done sessionId = %q, want %q", props["sessionId"], "s-err")
	}
}

// finishScriptProvider answers in one step with a little text and a fixed
// finish reason, which is all these tests need to drive a turn to its exit.
type finishScriptProvider struct{ finish string }

func (finishScriptProvider) ID() string { return "mock" }
func (finishScriptProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", ProviderID: "mock"}}
}

func (m finishScriptProvider) StreamChat(_ context.Context, _ provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent, 4)
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "a partial answer"}
		fr := m.finish
		ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
	}()
	return ch, nil
}

// runFinishScript drives one full turn whose single model call ends with the
// given finish reason, and returns the loop.done properties and the turn's
// messages.
func runFinishScript(t *testing.T, finish string) (map[string]string, []*session.MessageWithParts) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	// Pre-seed so no capability probe inserts an extra model call.
	if err := session.SetModelCapability(database, &session.ModelCapability{
		ModelID: "mock-model", SupportsImages: false, ProbedAt: session.Now(),
	}); err != nil {
		t.Fatalf("set capability: %v", err)
	}

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	reg.Register(finishScriptProvider{finish: finish})

	b := bus.New(64)
	events := b.SubscribeAll()
	lr := &LoopRunner{
		Store: store, Bus: b, Registry: reg, Tools: tool.NewRegistry(),
		Dir: t.TempDir(), MaxSteps: 5,
	}
	sess := &session.Session{
		ID: session.NewSessionID(), ProjectID: "p", Directory: t.TempDir(),
		Title: "t", Model: "mock-model", SessionType: "build",
		CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	userMsg := &session.MessageInfo{
		ID: session.NewMessageID(), SessionID: sess.ID,
		Role: session.RoleUser, CreatedAt: session.Now(),
	}
	if err := store.CreateMessage(userMsg); err != nil {
		t.Fatalf("create user msg: %v", err)
	}
	textData, _ := json.Marshal(session.TextPartData{Text: "write me something long"})
	if err := store.CreatePart(&session.Part{
		ID: session.NewPartID(), MessageID: userMsg.ID, SessionID: sess.ID,
		Type: session.PartText, Data: textData, CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}); err != nil {
		t.Fatalf("create user part: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- lr.RunLoop(context.Background(), sess.ID, "build", 0, 0) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunLoop returned error: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunLoop did not complete in time")
	}

	msgs, err := store.GetMessages(sess.ID, "", 100)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	return awaitLoopDone(t, events), msgs
}

// A clean finish must stay clean: no error key, so the client does not raise a
// failure banner for a turn that simply ended.
func TestRunLoopDoneOmitsErrorOnCleanExit(t *testing.T) {
	props, _ := runFinishScript(t, "stop")

	if props["reason"] != "stop" {
		t.Errorf("loop.done reason = %q, want %q", props["reason"], "stop")
	}
	if _, ok := props["error"]; ok {
		t.Errorf("a clean loop.done carried an error key: %q", props["error"])
	}
}

// The silent stop that started all this. The model hits its output ceiling, the
// loop treats "length" as terminal and exits with no error anywhere — so the
// only thing that can tell the user is the finish reason. Both the event and the
// message have to carry it, or the turn just appears to end mid-sentence.
func TestRunLoopDoneReportsLengthAsANonError(t *testing.T) {
	props, msgs := runFinishScript(t, "length")

	if props["reason"] != "length" {
		t.Errorf("loop.done reason = %q, want %q", props["reason"], "length")
	}
	if _, ok := props["error"]; ok {
		t.Errorf("a length-truncated turn is not an error, but loop.done carried %q", props["error"])
	}

	var finish string
	for _, m := range msgs {
		if m.Info.Role == session.RoleAssistant && m.Info.Finish != nil {
			finish = *m.Info.Finish
		}
	}
	if finish != "length" {
		t.Errorf("assistant message finish = %q, want %q — the UI renders its truncation notice from this", finish, "length")
	}
}
