package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/session"
)

func getProjectSettings(t *testing.T, h http.Handler) session.ProjectSettings {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/project/settings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/project/settings = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var settings session.ProjectSettings
	if err := json.Unmarshal(rec.Body.Bytes(), &settings); err != nil {
		t.Fatalf("decode settings: %v (body: %s)", err, rec.Body.String())
	}
	return settings
}

// A fresh project reports the defaults over HTTP rather than 500-ing on the
// missing row, so the settings screen renders before anything is ever saved.
func TestProjectSettingsRouteDefaults(t *testing.T) {
	h := newTestServer(t).routes()

	if !getProjectSettings(t, h).CompactContext {
		t.Error("a project with no saved settings should report compact_context enabled")
	}
}

func TestProjectSettingsRouteRoundTrip(t *testing.T) {
	h := newTestServer(t).routes()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/project/settings",
		strings.NewReader(`{"compactContext":false}`))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/project/settings = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// The response is the new state, so the UI never has to re-fetch to know
	// what it just saved.
	var saved session.ProjectSettings
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
	}
	if saved.CompactContext {
		t.Error("POST response should echo the disabled state")
	}
	if getProjectSettings(t, h).CompactContext {
		t.Error("compact_context should still read back disabled")
	}
}

func TestProjectSettingsRouteRejectsBadBody(t *testing.T) {
	h := newTestServer(t).routes()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/project/settings",
		strings.NewReader(`not json`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("POST with a malformed body = %d, want 400", rec.Code)
	}
	if !getProjectSettings(t, h).CompactContext {
		t.Error("a rejected write must not have changed the stored settings")
	}
}
