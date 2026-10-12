package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
)

// TestConfigReportsHostOS pins the delivery surface for the OS-aware search
// settings: /api/config carries the OS the server runs on, which is what lets
// the web UI show the Safari engine and its setup guide only where Safari
// search can actually drive a browser.
func TestConfigReportsHostOS(t *testing.T) {
	srv := newTestServer(t)
	h := srv.routes()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	var cfg map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("decode /api/config: %v (body %s)", err, rec.Body.String())
	}
	if cfg["os"] != runtime.GOOS {
		t.Errorf("/api/config os = %v, want %v", cfg["os"], runtime.GOOS)
	}
}
