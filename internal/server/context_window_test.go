package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

type windowedProvider struct{}

func (windowedProvider) ID() string { return "win" }
func (windowedProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{
		{ID: "known-model", ProviderID: "win", ContextWindow: 200_000},
		{ID: "learned-model", ProviderID: "win"},
		{ID: "unknown-model", ProviderID: "win"},
	}
}
func (windowedProvider) StreamChat(context.Context, provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	return nil, nil
}

// The context meter reads the window and compaction point from /api/models, so
// they must be the values the loop itself uses: the catalogue window, a learned
// window when the catalogue is silent, and the fallback cap when neither knows.
func TestModelsReportTheLoopsContextWindowAndCompactionPoint(t *testing.T) {
	srv := newTestServer(t)
	srv.registry.Register(windowedProvider{})
	if err := session.SetModelCapability(srv.db, &session.ModelCapability{ModelID: "learned-model", ContextWindow: 32_768, ProbedAt: session.Now()}); err != nil {
		t.Fatalf("learn window: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/models = %d", rec.Code)
	}
	var models []struct {
		ID              string `json:"id"`
		ContextWindow   int    `json:"contextWindow"`
		CompactAtTokens int    `json:"compactAtTokens"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &models); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := map[string][2]int{
		"known-model":   {200_000, 180_000}, // window − 20K reserve
		"learned-model": {32_768, 16_384},   // reserve capped at half the window
		"unknown-model": {0, 128_000},       // no window: the fallback cap
	}
	seen := 0
	for _, m := range models {
		w, ok := want[m.ID]
		if !ok {
			continue
		}
		seen++
		if m.ContextWindow != w[0] || m.CompactAtTokens != w[1] {
			t.Errorf("%s: window/compactAt = %d/%d, want %d/%d", m.ID, m.ContextWindow, m.CompactAtTokens, w[0], w[1])
		}
	}
	if seen != len(want) {
		t.Fatalf("saw %d of %d models in %s", seen, len(want), rec.Body.String())
	}
}
