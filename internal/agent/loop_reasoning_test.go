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

// reasoningScriptProvider replays a fixed sequence of stream events so a test
// can reproduce exactly the block shapes the Anthropic stream produces.
type reasoningScriptProvider struct {
	script []provider.StreamEvent
}

func (m *reasoningScriptProvider) ID() string { return "mock-reasoning" }
func (m *reasoningScriptProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-reasoning-model", ProviderID: "mock-reasoning"}}
}

func (m *reasoningScriptProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent, len(m.script)+2)
	go func() {
		defer close(ch)
		for _, evt := range m.script {
			select {
			case ch <- evt:
			case <-ctx.Done():
				return
			}
		}
		fr := "stop"
		select {
		case ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}:
		case <-ctx.Done():
		}
	}()
	return ch, nil
}

// runReasoningScript drives one turn and returns the reasoning parts the loop
// stored on the assistant message, in order.
func runReasoningScript(t *testing.T, script []provider.StreamEvent) []session.ReasoningPartData {
	t.Helper()
	resetCacheVerdicts()
	t.Cleanup(resetCacheVerdicts)

	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if err := session.SetModelCapability(database, &session.ModelCapability{
		ModelID: "mock-reasoning-model", SupportsImages: false, ProbedAt: session.Now(),
	}); err != nil {
		t.Fatalf("set capability: %v", err)
	}
	if err := session.SetModelCacheSupport(database, "mock-reasoning-model", "mock-reasoning",
		string(provider.CacheSupported), session.Now()); err != nil {
		t.Fatalf("seed cache verdict: %v", err)
	}

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	reg.Register(&reasoningScriptProvider{script: script})

	lr := &LoopRunner{
		Store: store, Bus: bus.New(64), Registry: reg, Tools: tool.NewRegistry(),
		Dir: t.TempDir(), MaxSteps: 4,
	}
	sess := &session.Session{
		ID: session.NewSessionID(), ProjectID: "p", Directory: t.TempDir(),
		Title: "t", Model: "mock-reasoning-model", SessionType: "build",
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
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run loop: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("run loop did not finish")
	}

	msgs, err := store.GetMessages(sess.ID, "", 100)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	var out []session.ReasoningPartData
	for _, m := range msgs {
		if m.Info.Role != session.RoleAssistant {
			continue
		}
		for _, p := range m.Parts {
			if p.Type != session.PartReasoning {
				continue
			}
			var d session.ReasoningPartData
			if err := json.Unmarshal(p.Data, &d); err != nil {
				t.Fatalf("bad reasoning part: %v", err)
			}
			out = append(out, d)
		}
	}
	return out
}

// A thinking block whose text is withheld — `display: "omitted"`, the default on
// current models — produces no text deltas at all: the block start and the
// closing signature are the only evidence it existed. It still has to be
// replayed exactly as received, so losing it here means the next request in the
// tool-use turn sends an incomplete sequence and the turn fails.
func TestRunLoop_StoresTextFreeThinkingBlock(t *testing.T) {
	parts := runReasoningScript(t, []provider.StreamEvent{
		{Type: provider.EventReasoningStart},
		{Type: provider.EventReasoningSignature, Signature: "ErkBCgIYAhIM..."},
		{Type: provider.EventTextDelta, Text: "the answer"},
	})

	if len(parts) != 1 {
		t.Fatalf("expected the text-free block to be stored, got %d parts: %+v", len(parts), parts)
	}
	if parts[0].Text != "" {
		t.Errorf("expected empty thinking text, got %q", parts[0].Text)
	}
	if parts[0].Signature != "ErkBCgIYAhIM..." {
		t.Errorf("expected the signature stored, got %q", parts[0].Signature)
	}
	if parts[0].Model != "mock-reasoning-model" {
		t.Errorf("expected the producing model recorded, got %q", parts[0].Model)
	}
}

// Two thinking blocks in one response must stay two blocks. Merging them keeps
// only the last signature and presents the pair as a single block, which no
// longer matches what the model generated — a 400 on replay.
func TestRunLoop_KeepsThinkingBlocksSeparate(t *testing.T) {
	parts := runReasoningScript(t, []provider.StreamEvent{
		{Type: provider.EventReasoningStart},
		{Type: provider.EventReasoning, Text: "first thought"},
		{Type: provider.EventReasoningSignature, Signature: "sig1=="},
		{Type: provider.EventReasoningStart},
		{Type: provider.EventReasoning, Text: "second thought"},
		{Type: provider.EventReasoningSignature, Signature: "sig2=="},
		{Type: provider.EventTextDelta, Text: "the answer"},
	})

	if len(parts) != 2 {
		t.Fatalf("expected 2 separate blocks, got %d: %+v", len(parts), parts)
	}
	if parts[0].Text != "first thought" || parts[0].Signature != "sig1==" {
		t.Errorf("first block wrong: %+v", parts[0])
	}
	if parts[1].Text != "second thought" || parts[1].Signature != "sig2==" {
		t.Errorf("second block wrong: %+v", parts[1])
	}
}

// A redacted block carries an opaque payload and no text, and must round-trip as
// a redacted_thinking block rather than an empty thinking block.
func TestRunLoop_StoresRedactedThinkingBlock(t *testing.T) {
	parts := runReasoningScript(t, []provider.StreamEvent{
		{Type: provider.EventReasoningRedacted, RedactedData: "EuYBCg=="},
		{Type: provider.EventTextDelta, Text: "the answer"},
	})

	if len(parts) != 1 {
		t.Fatalf("expected the redacted block stored, got %d parts: %+v", len(parts), parts)
	}
	if parts[0].RedactedData != "EuYBCg==" {
		t.Errorf("expected the payload stored, got %+v", parts[0])
	}
	if parts[0].Signature != "" || parts[0].Text != "" {
		t.Errorf("redacted block must carry neither text nor signature: %+v", parts[0])
	}
}
