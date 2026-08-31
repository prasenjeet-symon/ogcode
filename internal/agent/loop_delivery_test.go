package agent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// deliveryScriptProvider plays a fixed script of one turn so a test can control
// exactly what the model says first and how long it takes to say it.
type deliveryScriptProvider struct {
	calls int
	// failFirst makes the first StreamChat return a transient error, so the loop
	// retries and sleeps its backoff before the stream that finally opens.
	failFirst bool
	// reasoningFirst emits a reasoning event, waits, and only then emits text.
	reasoningFirst bool
	// thinkFor is how long the reasoning phase lasts before any text appears.
	thinkFor time.Duration
}

func (m *deliveryScriptProvider) ID() string { return "mock-delivery" }
func (m *deliveryScriptProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-delivery-model", ProviderID: "mock-delivery"}}
}

func (m *deliveryScriptProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	m.calls++
	if m.failFirst && m.calls == 1 {
		// Classified transient, so the loop retries rather than failing the turn.
		return nil, errors.New("503 service unavailable")
	}

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
		if m.reasoningFirst {
			if !send(provider.StreamEvent{Type: provider.EventReasoning, Text: "weighing the options"}) {
				return
			}
			select {
			case <-time.After(m.thinkFor):
			case <-ctx.Done():
				return
			}
		}
		if !send(provider.StreamEvent{Type: provider.EventTextDelta, Text: "the answer"}) {
			return
		}
		fr := "stop"
		send(provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr})
	}()
	return ch, nil
}

// runDeliveryScript drives one full turn and returns the assistant message the
// loop produced, along with the prompt that caused it and the wall time the
// whole run took.
func runDeliveryScript(t *testing.T, p *deliveryScriptProvider) (assistant *session.MessageInfo, prompt *session.MessageInfo, elapsed time.Duration) {
	t.Helper()
	resetCacheVerdicts()
	t.Cleanup(resetCacheVerdicts)

	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	// Pre-seed both caches so no probe or observation call runs ahead of the
	// scripted one and shifts the attempt count the test is asserting on.
	if err := session.SetModelCapability(database, &session.ModelCapability{
		ModelID: "mock-delivery-model", SupportsImages: false, ProbedAt: session.Now(),
	}); err != nil {
		t.Fatalf("set capability: %v", err)
	}
	if err := session.SetModelCacheSupport(database, "mock-delivery-model", "mock-delivery",
		string(provider.CacheSupported), session.Now()); err != nil {
		t.Fatalf("seed cache verdict: %v", err)
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
		Title: "t", Model: "mock-delivery-model", SessionType: "build",
		CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	prompt = &session.MessageInfo{
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

	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- lr.RunLoop(context.Background(), sess.ID, "build", 0, 0) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run loop: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("run loop did not finish")
	}
	elapsed = time.Since(start)

	msgs, err := store.GetMessages(sess.ID, "", 100)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	for _, m := range msgs {
		if m.Info.Role == session.RoleAssistant {
			info := m.Info
			assistant = &info
			break
		}
	}
	if assistant == nil {
		t.Fatal("loop produced no assistant message")
	}
	return assistant, prompt, elapsed
}

// A thinking model says nothing in text until it has finished reasoning. If
// only text deltas started the clock, TTFT would report the whole thinking
// phase as latency — the number would be wrong for exactly the models whose
// latency people care most about.
func TestRunLoop_DeliveryFirstTokenCountsReasoning(t *testing.T) {
	const thinkFor = 600 * time.Millisecond
	assistant, _, _ := runDeliveryScript(t, &deliveryScriptProvider{
		reasoningFirst: true, thinkFor: thinkFor,
	})

	d := assistant.Delivery
	if d == nil {
		t.Fatal("assistant message carries no Delivery record")
	}
	if d.FirstTokenKind != "reasoning" {
		t.Errorf("FirstTokenKind = %q, want %q — the reasoning event did not start the clock", d.FirstTokenKind, "reasoning")
	}
	if d.TTFTMs >= thinkFor.Milliseconds() {
		t.Errorf("TTFTMs = %d, which is at least the %dms thinking phase — the whole reasoning phase was billed as latency",
			d.TTFTMs, thinkFor.Milliseconds())
	}
}

// Backoff is ogcode's own waiting, not the model's. A turn that retried must
// still report the latency of the attempt that actually served it.
func TestRunLoop_DeliveryExcludesRetryBackoffFromTTFT(t *testing.T) {
	assistant, _, elapsed := runDeliveryScript(t, &deliveryScriptProvider{failFirst: true})

	d := assistant.Delivery
	if d == nil {
		t.Fatal("assistant message carries no Delivery record")
	}
	if d.Attempts != 2 {
		t.Errorf("Attempts = %d, want 2 — the retry was not recorded", d.Attempts)
	}
	// The loop's first backoff is a full second, so a run that genuinely retried
	// takes longer than that. Without it the test would pass on a loop that
	// never retried at all.
	if elapsed < time.Second {
		t.Fatalf("run took %v, less than the 1s backoff — the retry path did not run", elapsed)
	}
	if d.TTFTMs >= 500 {
		t.Errorf("TTFTMs = %d on a stream that answered immediately; the retry backoff leaked into it", d.TTFTMs)
	}
}

// The ticks are drawn against the prompt, so the link back to it and the
// ordering of the three stamps are what the UI depends on.
func TestRunLoop_DeliveryPairsWithThePromptThatCausedIt(t *testing.T) {
	assistant, prompt, _ := runDeliveryScript(t, &deliveryScriptProvider{})

	if assistant.ParentID == nil || *assistant.ParentID != prompt.ID {
		t.Errorf("assistant ParentID = %v, want the prompt %q", assistant.ParentID, prompt.ID)
	}
	d := assistant.Delivery
	if d == nil {
		t.Fatal("assistant message carries no Delivery record")
	}
	if d.DispatchedAt == 0 || d.ConnectedAt == 0 || d.FirstTokenAt == 0 {
		t.Fatalf("incomplete Delivery: dispatched=%d connected=%d firstToken=%d",
			d.DispatchedAt, d.ConnectedAt, d.FirstTokenAt)
	}
	if d.DispatchedAt > d.ConnectedAt || d.ConnectedAt > d.FirstTokenAt {
		t.Errorf("stamps out of order: dispatched=%d connected=%d firstToken=%d",
			d.DispatchedAt, d.ConnectedAt, d.FirstTokenAt)
	}
	if d.FirstTokenKind != "text" {
		t.Errorf("FirstTokenKind = %q, want %q", d.FirstTokenKind, "text")
	}
	if d.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", d.Attempts)
	}
}
