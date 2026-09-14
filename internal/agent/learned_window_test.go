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
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// overflowLearningProvider serves one Anthropic-style context-overflow 400 on
// its first main attempt and succeeds afterward, answering the summarizer
// (llmCompact's) with a short text. Its model carries NO catalog window — the
// shape of an Ollama local model or a dynamic OpenAI-compatible endpoint — so
// the only place the loop could learn the real window from is the overflow
// body.
type overflowLearningProvider struct {
	mu        sync.Mutex
	overflowN int // which main attempt (1-based) gets the overflow; 0 = never
	calls     int
	overflows int
}

func (m *overflowLearningProvider) ID() string { return "mock" }
func (m *overflowLearningProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-learn", ProviderID: "mock"}} // ContextWindow 0 = catalog silent
}

func textStream(text string) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent, 4)
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: text}
		fr := "stop"
		ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
	}()
	return ch
}

func (m *overflowLearningProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	m.mu.Lock()
	if len(req.System) > 0 && strings.Contains(req.System[0], "conversation summarizer") {
		m.mu.Unlock()
		return textStream("compact summary"), nil
	}
	m.calls++
	call := m.calls
	overflow := m.overflowN > 0 && call == m.overflowN
	if overflow {
		m.overflows++
		m.overflowN = 0 // exactly one overflow: the retry after learning succeeds
	}
	m.mu.Unlock()
	if overflow {
		return nil, &provider.APIError{
			Provider:   "mock",
			StatusCode: 400,
			Body:       "prompt is too long: 195000 tokens > 200000 maximum",
		}
	}
	return textStream("recovered answer"), nil
}

// TestRunLoop_LearnsContextWindowFromOverflowError pins the passive-learning
// hook end to end: a run whose first request overflows a catalog-silent model
// must persist the window the provider named in the error body, so the next
// run (and every later compaction decision) sizes from a real figure instead
// of the 128k fallback.
func TestRunLoop_LearnsContextWindowFromOverflowError(t *testing.T) {
	resetCacheVerdicts()
	t.Cleanup(resetCacheVerdicts)

	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := session.SetModelCapability(database, &session.ModelCapability{
		ModelID: "mock-learn", SupportsImages: false, ProbedAt: session.Now(),
	}); err != nil {
		t.Fatalf("seed capability: %v", err)
	}
	if err := session.SetModelCacheSupport(database, "mock-learn", "mock", string(provider.CacheSupported), session.Now()); err != nil {
		t.Fatalf("seed cache verdict: %v", err)
	}

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	mock := &overflowLearningProvider{overflowN: 1}
	reg.Register(mock)

	tools := tool.NewRegistry()
	tools.Register(noopGrepTool{})

	lr := &LoopRunner{
		Store: store, Bus: bus.New(64), Registry: reg, Tools: tools,
		Dir: t.TempDir(), MaxSteps: 10,
	}
	sess := &session.Session{
		ID: session.NewSessionID(), ProjectID: "p", Directory: t.TempDir(),
		Title: "t", Model: "mock-learn", SessionType: "build",
		CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	userMsg := &session.MessageInfo{ID: session.NewMessageID(), SessionID: sess.ID, Role: session.RoleUser, CreatedAt: session.Now()}
	if err := store.CreateMessage(userMsg); err != nil {
		t.Fatalf("create user msg: %v", err)
	}
	textData, _ := json.Marshal(session.TextPartData{Text: "do the task"})
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
	case <-time.After(30 * time.Second):
		t.Fatal("RunLoop did not complete in time")
	}

	mock.mu.Lock()
	overflows := mock.overflows
	mock.mu.Unlock()
	if overflows != 1 {
		t.Errorf("overflow rejections = %d, want 1 (learned on the first, succeeded on the retry)", overflows)
	}
	cap, ok, err := session.GetModelCapability(lr.Store.DB(), "mock-learn")
	if err != nil || !ok {
		t.Fatalf("capability record after run: ok=%v err=%v", ok, err)
	}
	if cap.ContextWindow != 200000 {
		t.Errorf("learned ContextWindow = %d, want 200000 (the figure the overflow body named)", cap.ContextWindow)
	}
	if cap.SupportsImages {
		t.Error("learning must not manufacture an image verdict")
	}
}

// windowCatalogProvider advertises a context window in its catalog for one
// model and stays silent for another — letting one registry serve both the
// catalog-wins case and the learned-fills-in case.
type windowCatalogProvider struct {
	mu    sync.Mutex
	calls int
}

func (m *windowCatalogProvider) ID() string { return "mock" }
func (m *windowCatalogProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{
		{ID: "ctx-model", ProviderID: "mock", ContextWindow: 64000},
		{ID: "zero-model", ProviderID: "mock", ContextWindow: 0},
	}
}
func (m *windowCatalogProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	return textStream("ok"), nil
}

// TestResolveRunModel_ContextWindowPreference pins the resolution order the
// loop's compaction sizing rides on: the catalog is authoritative when it
// knows the model; a window learned from an overflow error fills in ONLY when
// the catalog is silent; and with neither source the window stays 0 (the
// compaction threshold then falls back to the fixed 128k cap).
func TestResolveRunModel_ContextWindowPreference(t *testing.T) {
	resetCacheVerdicts()
	t.Cleanup(resetCacheVerdicts)

	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	// Seed capability records for two of the three models: ctx-model's record
	// carries a STALE learned window (0) to prove the catalog still wins, and
	// zero-model's carries the learned one. never-model has no record — its
	// record-shaped seed (window 0) also keeps resolveImageSupport off the
	// probe path. Seeding a record with window 0 is what the image probe does
	// on a fresh model, so this is the real-world shape.
	for model, window := range map[string]int{"ctx-model": 0, "zero-model": 123456, "unseeded-model": 0} {
		if err := session.SetModelCapability(database, &session.ModelCapability{
			ModelID: model, SupportsImages: false, ProbedAt: session.Now(), ContextWindow: window,
		}); err != nil {
			t.Fatalf("seed capability %s: %v", model, err)
		}
	}

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	reg.Register(&windowCatalogProvider{})

	lr := &LoopRunner{Store: store, Bus: bus.New(64), Registry: reg, Tools: tool.NewRegistry(), Dir: t.TempDir(), MaxSteps: 5}
	ctx := context.Background()

	cases := []struct {
		name    string
		model   string
		want    int
		because string
	}{
		{"catalog wins over everything", "ctx-model", 64000, "the catalog is authoritative when it knows the model"},
		{"learned fills the silent catalog", "zero-model", 123456, "catalog reports 0 → the learned window fills in"},
		{"no source at all stays 0", "unseeded-model", 0, "neither catalog nor a learned window → unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sess := &session.Session{Model: c.model}
			_, modelID, _, window := lr.resolveRunModel(ctx, sess, session.NewSessionID())
			if modelID != c.model {
				t.Fatalf("resolved model = %q, want %q", modelID, c.model)
			}
			if window != c.want {
				t.Errorf("contextWindow = %d, want %d (%s)", window, c.want, c.because)
			}
		})
	}
}
