package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/memfile"
	"github.com/prasenjeet-symon/ogcode/internal/project"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// continuityScriptProvider returns a plain text answer per call and records
// every request. Its answer is chosen from the request itself so the two turns
// (and the background summary calls) stay deterministic without shared mutable
// state: the summary synthesis is recognised by its system prompt, turn one by
// its user text.
type continuityScriptProvider struct {
	mu       sync.Mutex
	messages [][]provider.ModelMessage
}

func (m *continuityScriptProvider) ID() string { return "mock" }
func (m *continuityScriptProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", ProviderID: "mock"}}
}

func (m *continuityScriptProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	m.mu.Lock()
	snapshot := make([]provider.ModelMessage, len(req.Messages))
	copy(snapshot, req.Messages)
	m.messages = append(m.messages, snapshot)
	m.mu.Unlock()

	text := "TURN_TWO_DONE"
	isSummary := false
	for _, s := range req.System {
		if strings.Contains(s, "memory scribe") {
			isSummary = true
			break
		}
	}
	if isSummary {
		text = "SUMMARY_TEXT"
	} else {
		var joined strings.Builder
		for _, msg := range req.Messages {
			if msg.Role == "user" {
				var c string
				_ = json.Unmarshal(msg.Content, &c)
				joined.WriteString(c)
			}
		}
		if strings.Contains(joined.String(), "turn one") {
			text = "RESPONSE_ONE_MARKER"
		}
	}

	ch := make(chan provider.StreamEvent, 4)
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: text}
		fr := "stop"
		ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
	}()
	return ch, nil
}

func (m *continuityScriptProvider) requests() [][]provider.ModelMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]provider.ModelMessage, len(m.messages))
	copy(out, m.messages)
	return out
}

// TestRunLoop_TurnMemoryReinjectsPreviousResponse is the end-to-end continuity
// check for the per-turn markdown route: with only the current turn on the wire,
// the previous turn's final answer must still reach the model, wrapped in
// <previous_response>, so a bare follow-up has continuity.
func TestRunLoop_TurnMemoryReinjectsPreviousResponse(t *testing.T) {
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
	mock := &continuityScriptProvider{}
	reg.Register(mock)

	dir := t.TempDir()
	lr := &LoopRunner{
		Store: store, Bus: bus.New(64), Registry: reg, Tools: tool.NewRegistry(),
		Dir: dir, MaxSteps: 10,
		// Turn-memory route active: this is what makes the loop send only the
		// current turn and re-inject the previous response.
		MemFiles: memfile.NewStore(database), MemBarrier: memfile.NewManager(), TurnMemory: true,
	}

	sess := &session.Session{
		ID: session.NewSessionID(), ProjectID: dir, Directory: dir,
		Title: "t", Model: "mock-model", SessionType: "build",
		CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	runTurn := func(text string) {
		um := &session.MessageInfo{ID: session.NewMessageID(), SessionID: sess.ID, Role: session.RoleUser, CreatedAt: session.Now()}
		if err := store.CreateMessage(um); err != nil {
			t.Fatalf("create user msg: %v", err)
		}
		td, _ := json.Marshal(session.TextPartData{Text: text})
		if err := store.CreatePart(&session.Part{
			ID: session.NewPartID(), MessageID: um.ID, SessionID: sess.ID,
			Type: session.PartText, Data: td, CreatedAt: session.Now(), UpdatedAt: session.Now(),
		}); err != nil {
			t.Fatalf("create user part: %v", err)
		}
		done := make(chan error, 1)
		go func() { done <- lr.RunLoop(context.Background(), sess.ID, "build", 0, 0) }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("RunLoop error: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("RunLoop did not complete in time")
		}
	}

	runTurn("turn one")
	runTurn("turn two")

	// Drain background summary writes so nothing touches the temp DB after cleanup.
	lr.MemBarrier.Wait(project.Resolve(dir))

	// Find the main-loop request that carries the previous response (summary
	// requests are recognised by their scribe system prompt and skipped).
	var turnTwoLead string
	found := false
	for _, msgs := range mock.requests() {
		if len(msgs) == 0 || msgs[0].Role != "user" {
			continue
		}
		var lead string
		_ = json.Unmarshal(msgs[0].Content, &lead)
		if strings.Contains(lead, "<previous_response>") {
			turnTwoLead = lead
			found = true
		}
	}

	if !found {
		t.Fatal("no request carried a <previous_response> block; continuity was not injected")
	}
	if !strings.Contains(turnTwoLead, "RESPONSE_ONE_MARKER") {
		t.Errorf("previous turn's response missing from the block:\n%s", turnTwoLead)
	}
	if !strings.Contains(turnTwoLead, "</previous_response>") {
		t.Errorf("block not closed:\n%s", turnTwoLead)
	}
	if !strings.HasSuffix(strings.TrimRight(turnTwoLead, "\n"), "turn two") {
		t.Errorf("current user message must follow the block:\n%s", turnTwoLead)
	}

	// Turn one had no prior response, so its request must NOT carry the block.
	for _, msgs := range mock.requests() {
		if len(msgs) == 0 || msgs[0].Role != "user" {
			continue
		}
		var lead string
		_ = json.Unmarshal(msgs[0].Content, &lead)
		if strings.Contains(lead, "turn one") && strings.Contains(lead, "<previous_response>") {
			t.Errorf("turn one wrongly carried a previous-response block:\n%s", lead)
		}
	}
}
