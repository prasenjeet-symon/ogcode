package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// resetProvider opens a stream successfully and then fails it, which is what a
// TCP reset after the response headers actually looks like to the loop: not an
// error from StreamChat, but an EventError on an already-live channel.
type resetProvider struct {
	calls int
	// resetsBeforeSuccess is how many opened streams die before one completes.
	resetsBeforeSuccess int
	// emitBeforeReset sends a text delta before failing, so the reset arrives
	// with visible output already on screen.
	emitBeforeReset bool
	// silentClose ends the stream by closing the channel with no events at all —
	// what a clean FIN mid-response looks like, as opposed to an RST.
	silentClose bool
	// err is the failure text; defaults to the real-world reset.
	err string
}

func (m *resetProvider) ID() string { return "mock-reset" }
func (m *resetProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-reset-model", ProviderID: "mock-reset"}}
}

func (m *resetProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	m.calls++
	failing := m.calls <= m.resetsBeforeSuccess
	ch := make(chan provider.StreamEvent, 8)
	go func() {
		defer close(ch)
		send := func(evt provider.StreamEvent) bool {
			select {
			case ch <- evt:
				return true
			case <-ctx.Done():
				return false
			}
		}
		if failing {
			if m.emitBeforeReset {
				if !send(provider.StreamEvent{Type: provider.EventTextDelta, Text: "partial answer"}) {
					return
				}
			}
			if m.silentClose {
				// No error event, no finish reason: the channel just ends. With
				// emitBeforeReset that is a truncated response; without it, an
				// empty one.
				return
			}
			msg := m.err
			if msg == "" {
				msg = "stream read failed: read tcp [2409::1]:64593->[2606:4700::1]:443: read: connection reset by peer"
			}
			send(provider.StreamEvent{Type: provider.EventError, Error: msg})
			return
		}
		if !send(provider.StreamEvent{Type: provider.EventTextDelta, Text: "the answer"}) {
			return
		}
		fr := "stop"
		send(provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr})
	}()
	return ch, nil
}

// runResetScript drives one turn and returns the loop's error plus every
// assistant message left in the store.
func runResetScript(t *testing.T, p *resetProvider) (error, []*session.MessageInfo, *session.Store) {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := session.SetModelCapability(database, &session.ModelCapability{
		ModelID: "mock-reset-model", SupportsImages: false, ProbedAt: session.Now(),
	}); err != nil {
		t.Fatalf("set capability: %v", err)
	}

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	reg.Register(p)
	lr := &LoopRunner{
		Store: store, Bus: bus.New(64), Registry: reg, Tools: tool.NewRegistry(),
		Dir: t.TempDir(), MaxSteps: 4,
	}
	sess := &session.Session{
		ID: session.NewSessionID(), ProjectID: "p", Directory: t.TempDir(),
		Title: "t", Model: "mock-reset-model", SessionType: "build",
		CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	prompt := &session.MessageInfo{
		ID: session.NewMessageID(), SessionID: sess.ID,
		Role: session.RoleUser, CreatedAt: session.Now(),
	}
	if err := store.CreateMessage(prompt); err != nil {
		t.Fatalf("create user msg: %v", err)
	}
	textData, _ := json.Marshal(session.TextPartData{Text: "do the task"})
	if err := store.CreatePart(&session.Part{
		ID: session.NewPartID(), MessageID: prompt.ID, SessionID: sess.ID,
		Type: session.PartText, Data: textData, CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}); err != nil {
		t.Fatalf("create user part: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- lr.RunLoop(context.Background(), sess.ID, "build", 0, 0) }()
	var runErr error
	select {
	case runErr = <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("run loop did not finish")
	}

	msgs, err := store.GetMessages(sess.ID, "", 100)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	var assistants []*session.MessageInfo
	for _, m := range msgs {
		if m.Info.Role == session.RoleAssistant {
			info := m.Info
			assistants = append(assistants, &info)
		}
	}
	return runErr, assistants, store
}

// The case this exists for: a connection reset that lands after the response
// headers, before the model has said anything. Nothing has been shown or
// stored, so re-dispatching cannot duplicate visible work — and the user should
// never learn it happened.
func TestMidStreamReset_RetriesWhenNothingWasProduced(t *testing.T) {
	p := &resetProvider{resetsBeforeSuccess: 1}
	err, assistants, _ := runResetScript(t, p)

	if err != nil {
		t.Fatalf("a reset before any output should be recovered, got: %v", err)
	}
	if p.calls != 2 {
		t.Errorf("expected the request to be re-dispatched once (2 calls), got %d", p.calls)
	}
	// The empty shell from the failed attempt must be gone: left in the store it
	// would sit between two user messages and break the strict alternation both
	// APIs require on the next request.
	if len(assistants) != 1 {
		t.Fatalf("expected exactly 1 assistant message, got %d — the abandoned attempt was not cleaned up", len(assistants))
	}
	if assistants[0].Error != nil {
		t.Errorf("the surviving message should carry no error, got %q", *assistants[0].Error)
	}
}

// Once output exists, replaying would duplicate it on screen. Those keep the
// resumable error the user can act on.
func TestMidStreamReset_DoesNotRetryAfterOutput(t *testing.T) {
	p := &resetProvider{resetsBeforeSuccess: 1, emitBeforeReset: true}
	err, assistants, _ := runResetScript(t, p)

	if err == nil {
		t.Fatal("a reset after visible output must surface, not silently replay")
	}
	if p.calls != 1 {
		t.Errorf("expected no re-dispatch after output, got %d calls", p.calls)
	}
	if len(assistants) != 1 || assistants[0].Error == nil {
		t.Fatal("the partial message should be kept, carrying the error")
	}
	if assistants[0].Interrupted == nil || !assistants[0].Interrupted.Resumable {
		t.Error("the turn should be marked resumable so the user can continue it")
	}
}

// A permanently broken connection must not spin. The budget is run-wide
// precisely because the retry re-enters the same step.
func TestMidStreamReset_BudgetIsBounded(t *testing.T) {
	p := &resetProvider{resetsBeforeSuccess: 100}
	err, _, _ := runResetScript(t, p)

	if err == nil {
		t.Fatal("an endlessly failing stream must eventually surface")
	}
	// maxMidStreamRetries re-dispatches, plus the final attempt that gives up.
	if p.calls > maxMidStreamRetries+1 {
		t.Errorf("retried %d times; the run-wide budget should cap it", p.calls)
	}
}

// A non-transient failure is not a connection problem and must not be replayed.
func TestMidStreamReset_OnlyRetriesTransientErrors(t *testing.T) {
	p := &resetProvider{resetsBeforeSuccess: 1, err: "invalid request: unknown model"}
	err, _, _ := runResetScript(t, p)

	if err == nil {
		t.Fatal("a non-transient mid-stream error must surface")
	}
	if p.calls != 1 {
		t.Errorf("expected no re-dispatch for a non-transient error, got %d calls", p.calls)
	}
}

// A peer that closes cleanly mid-response gives the reader EOF, not a scan
// error, so no EventError is ever emitted — the channel simply ends. That used
// to land in "no content and no finish_reason" and hand the user the same manual
// Resume this fix exists to remove. It is the identical failure and gets the
// identical treatment.
func TestStreamClosedSilently_RetriesWhenNothingWasProduced(t *testing.T) {
	p := &resetProvider{resetsBeforeSuccess: 1, silentClose: true}
	err, assistants, _ := runResetScript(t, p)

	if err != nil {
		t.Fatalf("a silent close before any output should be recovered, got: %v", err)
	}
	if p.calls != 2 {
		t.Errorf("expected the request to be re-dispatched once (2 calls), got %d", p.calls)
	}
	if len(assistants) != 1 {
		t.Fatalf("expected exactly 1 assistant message, got %d", len(assistants))
	}
	if assistants[0].Error != nil {
		t.Errorf("the surviving message should carry no error, got %q", *assistants[0].Error)
	}
}

// A stream that produced output and then closed without a finish signal is
// truncated, not empty — replaying it would duplicate what is already on screen,
// so it keeps the resumable error.
func TestStreamClosedSilently_DoesNotRetryAfterOutput(t *testing.T) {
	p := &resetProvider{resetsBeforeSuccess: 1, silentClose: true, emitBeforeReset: true}
	_, assistants, store := runResetScript(t, p)

	if p.calls != 1 {
		t.Errorf("expected no re-dispatch, got %d calls", p.calls)
	}
	// Note the asymmetry this pins, which predates the retry: an EventError after
	// output returns an error from RunLoop, while a silent close after output
	// returns nil and records the failure on the message. The user sees the same
	// thing either way — a message carrying an error, with Resume — so the
	// assertion is on the message, which is the part that is actually the
	// contract.
	if len(assistants) != 1 {
		t.Fatalf("expected the partial message to be kept, got %d assistant messages", len(assistants))
	}
	if assistants[0].Error == nil {
		t.Error("the truncated message should carry an error")
	}
	if assistants[0].Interrupted == nil || !assistants[0].Interrupted.Resumable {
		t.Error("a truncated turn should be resumable so the user can continue it")
	}
	// The visible output must survive — that is the whole reason this case is
	// not replayed.
	parts, err := store.GetParts(assistants[0].ID)
	if err != nil {
		t.Fatalf("get parts: %v", err)
	}
	if len(parts) == 0 {
		t.Error("the partial output was discarded; it should be kept")
	}
}
