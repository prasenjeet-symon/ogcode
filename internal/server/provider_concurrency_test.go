package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// TestProviderConcurrencyRoutes exercises the Settings→General ceiling: read
// the state, store a new value, and reject one below the minimum.
func TestProviderConcurrencyRoutes(t *testing.T) {
	srv := newTestServer(t)
	h := srv.routes()

	// The budget is process-wide, so leave the default behind for other tests.
	t.Setenv("OGCODE_PROVIDER_MAX_CONCURRENT", "")
	t.Cleanup(func() { provider.ApplyConcurrency(0) })

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/providers/concurrency", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/providers/concurrency = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var state struct {
		Value        int  `json:"value"`
		EnvSet       bool `json:"envSet"`
		Min          int  `json:"min"`
		DefaultValue int  `json:"defaultValue"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if state.EnvSet {
		t.Fatal("envSet = true, want false with the variable cleared")
	}
	if state.Min != provider.MinConcurrentRequests {
		t.Fatalf("min = %d, want %d", state.Min, provider.MinConcurrentRequests)
	}
	if state.DefaultValue != provider.DefaultMaxConcurrentRequests {
		t.Fatalf("defaultValue = %d, want %d", state.DefaultValue, provider.DefaultMaxConcurrentRequests)
	}
	if state.Value != provider.DefaultMaxConcurrentRequests {
		t.Fatalf("value = %d, want %d with nothing stored", state.Value, provider.DefaultMaxConcurrentRequests)
	}

	rec = httptest.NewRecorder()
	body := strings.NewReader(`{"value":12}`)
	req := httptest.NewRequest(http.MethodPost, "/api/providers/concurrency", body)
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/providers/concurrency = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if state.Value != 12 {
		t.Fatalf("value after set = %d, want 12", state.Value)
	}
	if got := provider.MaxConcurrentRequests(); got != 12 {
		t.Fatalf("capacity in force = %d, want 12", got)
	}
	if stored, err := session.GetProviderConcurrency(srv.globalDB); err != nil || stored != 12 {
		t.Fatalf("stored concurrency = %d, %v; want 12, nil", stored, err)
	}

	rec = httptest.NewRecorder()
	body = strings.NewReader(`{"value":1}`)
	req = httptest.NewRequest(http.MethodPost, "/api/providers/concurrency", body)
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST below the minimum = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := provider.MaxConcurrentRequests(); got != 12 {
		t.Fatalf("capacity after rejection = %d, want 12", got)
	}
}
