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
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// racingSummaryProvider returns a plain text answer per main-loop call and
// records every request. The turn-summary synthesis call (recognised by its
// system prompt) blocks until released, so the test can start the next turn
// while the previous turn's summary is still in flight.
type racingSummaryProvider struct {
	mu            sync.Mutex
	messages      [][]provider.ModelMessage
	systemPrompts []string
	// releaseSum, when non-nil, is closed to let the summary call return.
	releaseSum chan struct{}
	// summaryEntered is closed when the summary call is entered.
	summaryEntered chan struct{}
	once           sync.Once
	// summaryCalls counts summary synthesis calls.
	summaryCalls int
}

func (m *racingSummaryProvider) ID() string { return "mock" }
func (m *racingSummaryProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", ProviderID: "mock"}}
}

func (m *racingSummaryProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	text := "TURN_DONE"
	isSummary := false
	for _, s := range req.System {
		if strings.Contains(s, "summarizing one completed turn") {
			isSummary = true
			break
		}
	}

	if isSummary {
		m.mu.Lock()
		m.summaryCalls++
		m.mu.Unlock()
		m.once.Do(func() { close(m.summaryEntered) })
		if m.releaseSum != nil {
			select {
			case <-m.releaseSum:
			case <-ctx.Done():
				ch := make(chan provider.StreamEvent, 1)
				close(ch)
				return ch, nil
			}
		}
		text = "SUMMARY_TEXT"
	} else {
		m.mu.Lock()
		snapshot := make([]provider.ModelMessage, len(req.Messages))
		copy(snapshot, req.Messages)
		m.messages = append(m.messages, snapshot)
		m.systemPrompts = append(m.systemPrompts, strings.Join(req.System, "\n"))
		m.mu.Unlock()
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

func (m *racingSummaryProvider) requests() [][]provider.ModelMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]provider.ModelMessage, len(m.messages))
	copy(out, m.messages)
	return out
}

func (m *racingSummaryProvider) systems() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.systemPrompts))
	copy(out, m.systemPrompts)
	return out
}

// TestRunLoop_WaitsForInflightSummary proves a turn that starts while the
// previous turn's background summary is still in flight waits for it, then
// injects it — instead of starting without it (the old behaviour).
func TestRunLoop_WaitsForInflightSummary(t *testing.T) {
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
	mock := &racingSummaryProvider{
		releaseSum:     make(chan struct{}),
		summaryEntered: make(chan struct{}),
	}
	reg.Register(mock)

	dir := t.TempDir()
	lr := &LoopRunner{
		Store: store, Bus: bus.New(64), Registry: reg, Tools: tool.NewRegistry(),
		Dir: dir, MaxSteps: 10,
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
		case <-time.After(30 * time.Second):
			t.Fatal("RunLoop did not complete in time")
		}
	}

	// Turn one: ends, fires the background summary (which blocks).
	runTurn("turn one")

	// Wait until the summary call is actually in flight and blocked.
	select {
	case <-mock.summaryEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("background summary was not started")
	}

	// Turn two starts while the summary is still blocked: it must wait for
	// it, then proceed. Release the summary on a timer so the test cannot
	// hang if the wait is broken.
	releaseTimer := time.AfterFunc(2*time.Second, func() { close(mock.releaseSum) })
	runTurn("turn two")
	releaseTimer.Stop()

	// The second turn's main-loop request must carry the summary in its
	// system prompt (formatRecentTurnSummaries is appended there).
	found := false
	for _, sys := range mock.systems() {
		if strings.Contains(sys, "Recent turn continuity") && strings.Contains(sys, "SUMMARY_TEXT") {
			found = true
		}
	}
	if !found {
		t.Fatal("turn two did not include the in-flight summary; the wait did not work")
	}

	// The tracker must be empty now — the entry was deregistered.
	if _, ok := turnSummaryInFlight.Load(sess.ID); ok {
		t.Error("turnSummaryInFlight still holds an entry after the summary completed")
	}
	// Both turns synthesize a summary at end of turn (turn two's fires too),
	// but turn two must have injected turn one's before it ran.
	mock.mu.Lock()
	calls := mock.summaryCalls
	mock.mu.Unlock()
	if calls < 1 {
		t.Errorf("expected at least 1 summary synthesis call, got %d", calls)
	}
}
