package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// When a background model is explicitly configured in settings, title generation
// uses that model/provider instead of the session's model.
func TestGenerateTitleUsesConfiguredBackgroundModel(t *testing.T) {
	srv := newTestServer(t)
	srv.store = session.NewStore(srv.db)
	srv.bus = bus.New(64)

	sessionProv := titleProvider()
	bgProv := &capturingProvider{
		id: "openai",
		models: []provider.ModelInfo{
			{ID: "gpt-4o-mini", Default: true},
		},
		events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "Background titled session"},
		},
	}
	srv.registry.Register(sessionProv)
	srv.registry.Register(bgProv)
	srv.defaultProvider = sessionProv

	if err := session.SetBackgroundModel(srv.globalDB, "gpt-4o-mini", "openai"); err != nil {
		t.Fatalf("SetBackgroundModel: %v", err)
	}

	sess := &session.Session{
		ID:        "ses_bg_title",
		Title:     "New session",
		Model:     "deepseek-v4:cloud",
		Provider:  "ollama",
		CreatedAt: session.Now(),
		UpdatedAt: session.Now(),
	}
	if err := srv.store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	srv.generateTitle(sess.ID, "hello world", sess.Model, sess.Provider)
	if got := bgProv.model(); got != "gpt-4o-mini" {
		t.Fatalf("background provider model = %q, want gpt-4o-mini", got)
	}
	if got := sessionProv.model(); got != "" {
		t.Fatalf("session provider should not have been called, got model %q", got)
	}
}

func TestBackgroundModelEndpoints(t *testing.T) {
	srv := newTestServer(t)
	srv.registry.ReplaceProviders(map[string]provider.Provider{
		"anthropic": stubProvider{id: "anthropic", models: []provider.ModelInfo{
			{ID: "claude-3-5-haiku", ProviderID: "anthropic", ActiveByDefault: true},
		}},
	})
	mux := srv.routes()

	// 1. Default is empty (inherit from session).
	req := httptest.NewRequest(http.MethodGet, "/api/models/background", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/models/background status = %d, want 200", rec.Code)
	}
	var got session.BackgroundModelPref
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if got.Model != "" || got.ProviderID != "" {
		t.Fatalf("initial pref = %+v, want empty", got)
	}

	// 2. A model no provider serves is rejected at save time: persisting it
	// would stamp a dead id onto every utility call that inherits the
	// background model.
	badBody, _ := json.Marshal(map[string]string{
		"model":      "claude-3-5-haiku-typo",
		"providerId": "anthropic",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/models/background", bytes.NewReader(badBody))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST unavailable model status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// 3. Set a background model and provider.
	body, _ := json.Marshal(map[string]string{
		"model":      "claude-3-5-haiku",
		"providerId": "anthropic",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/models/background", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/models/background status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// Verify persisted via GET.
	req = httptest.NewRequest(http.MethodGet, "/api/models/background", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if got.Model != "claude-3-5-haiku" || got.ProviderID != "anthropic" {
		t.Fatalf("stored pref = %+v, want model=claude-3-5-haiku provider_id=anthropic", got)
	}

	// 4. Clear back to empty (inherit from session).
	clearBody, _ := json.Marshal(map[string]string{
		"model":      "",
		"providerId": "",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/models/background", bytes.NewReader(clearBody))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear POST /api/models/background status = %d, want 200", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/models/background", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if got.Model != "" || got.ProviderID != "" {
		t.Fatalf("cleared pref = %+v, want empty", got)
	}
}
