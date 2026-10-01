package server

import (
	"context"
	"sync"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// capturingProvider records the model its StreamChat was asked for, so a test
// can assert which model a utility call (title generation) ran on.
type capturingProvider struct {
	id       string
	models   []provider.ModelInfo
	events   []provider.StreamEvent
	mu       sync.Mutex
	gotModel string
}

func (p *capturingProvider) ID() string                   { return p.id }
func (p *capturingProvider) Models() []provider.ModelInfo { return p.models }
func (p *capturingProvider) StreamChat(_ context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	p.mu.Lock()
	p.gotModel = req.Model
	p.mu.Unlock()
	ch := make(chan provider.StreamEvent, len(p.events))
	for _, e := range p.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func (p *capturingProvider) model() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.gotModel
}

// titleProvider builds a provider whose catalogue leads with minimax-m3:cloud —
// the id substring-matching "mini" that a "fast model" heuristic used to pick
// for every ollama session.
func titleProvider() *capturingProvider {
	return &capturingProvider{
		id: "ollama",
		models: []provider.ModelInfo{
			{ID: "minimax-m3:cloud"},
			{ID: "deepseek-v4:cloud", Default: true},
		},
		events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "Fix the login page"},
		},
	}
}

// runTitleModel generates a title for a fresh session and returns the model the
// title call actually ran on.
func runTitleModel(t *testing.T, sessionModel string) string {
	t.Helper()
	srv := newTestServer(t)
	srv.store = session.NewStore(srv.db)
	srv.bus = bus.New(64)

	p := titleProvider()
	srv.registry.Register(p)
	srv.defaultProvider = p

	sess := &session.Session{
		ID:        "ses_title",
		Title:     "New session",
		Model:     sessionModel,
		Provider:  "ollama",
		CreatedAt: session.Now(),
		UpdatedAt: session.Now(),
	}
	if err := srv.store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	srv.generateTitle(sess.ID, "hello world", sess.Model, sess.Provider)
	return p.model()
}

// Title generation must run on the model the user chose for the session, the
// way the risk check, compaction and the memory summary do. The old code
// substring-matched haiku/mini/flash against the provider's catalogue, so
// minimax-m3:cloud — the first ollama model, and the only one containing "mini"
// — titled every session instead of the session's own model.
func TestGenerateTitleUsesTheSessionModel(t *testing.T) {
	if got := runTitleModel(t, "deepseek-v4:cloud"); got != "deepseek-v4:cloud" {
		t.Fatalf("title model = %q, want the session's model deepseek-v4:cloud", got)
	}
}

// When the session names no model, the title falls back to the provider's
// default — never to a substring match.
func TestGenerateTitleFallsBackToTheProviderDefault(t *testing.T) {
	if got := runTitleModel(t, ""); got != "deepseek-v4:cloud" {
		t.Fatalf("title model = %q, want the provider default deepseek-v4:cloud", got)
	}
}
