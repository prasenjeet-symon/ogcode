package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// TestNavStateRoutes exercises the per-project nav state: a fresh project
// reports both panels expanded, one fold is stored without disturbing the
// other, and the stored state survives out of the HTTP layer.
func TestNavStateRoutes(t *testing.T) {
	srv := newTestServer(t)
	h := srv.routes()

	var state navStatePayload
	get := func() {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/ui/nav", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/ui/nav = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
			t.Fatalf("decode state: %v", err)
		}
	}
	post := func(body string) {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/ui/nav", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /api/ui/nav %s = %d, want 200 (body: %s)", body, rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
			t.Fatalf("decode state: %v", err)
		}
	}

	get()
	if state.SidebarCollapsed || state.SettingsNavCollapsed {
		t.Fatalf("fresh project = %+v, want both expanded", state)
	}

	// One panel at a time: the omitted field keeps its current value.
	post(`{"sidebarCollapsed":true}`)
	if !state.SidebarCollapsed || state.SettingsNavCollapsed {
		t.Fatalf("after sidebar fold = %+v, want sidebar collapsed only", state)
	}

	post(`{"settingsNavCollapsed":true}`)
	if !state.SidebarCollapsed || !state.SettingsNavCollapsed {
		t.Fatalf("after settings fold = %+v, want both collapsed", state)
	}

	stored, err := session.GetNavState(srv.db)
	if err != nil {
		t.Fatalf("stored nav state: %v", err)
	}
	if !stored.SidebarCollapsed || !stored.SettingsNavCollapsed {
		t.Fatalf("stored nav state = %+v, want both collapsed", stored)
	}

	// Expanding is a fold reported the same way.
	post(`{"sidebarCollapsed":false}`)
	if state.SidebarCollapsed || !state.SettingsNavCollapsed {
		t.Fatalf("after sidebar expand = %+v, want settings collapsed only", state)
	}
}
