package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// The image-model preference has one gate the background pref does not: the
// model must accept images, because analyze_image has no other model to fall
// back to. TestImageModelEndpoints pins that gate beside everything the
// background handlers already do.
func TestImageModelEndpoints(t *testing.T) {
	srv := newTestServer(t)
	srv.registry.ReplaceProviders(map[string]provider.Provider{
		"test": stubProvider{id: "test", models: []provider.ModelInfo{
			{ID: "eye-model", ProviderID: "test", SupportsImages: true},
			{ID: "blind-model", ProviderID: "test"},
			{ID: "probed-vl", ProviderID: "test"},
		}},
	})
	if err := session.SetModelCapability(srv.db, &session.ModelCapability{
		ModelID:        "probed-vl",
		SupportsImages: true,
		ProbedAt:       session.Now(),
	}); err != nil {
		t.Fatalf("set capability: %v", err)
	}
	mux := srv.routes()

	// 1. Default is empty (inherit the session's own model).
	req := httptest.NewRequest(http.MethodGet, "/api/models/image", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/models/image status = %d, want 200", rec.Code)
	}
	var got session.ImageModelPref
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if got.Model != "" || got.ProviderID != "" {
		t.Fatalf("initial pref = %+v, want empty", got)
	}

	// 2. A model no provider serves is rejected at save time, as for the
	// background pref.
	missingBody, _ := json.Marshal(map[string]string{
		"model":      "eye-model-typo",
		"providerId": "test",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/models/image", bytes.NewReader(missingBody))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST unavailable model status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("not available")) {
		t.Fatalf("unavailable-model rejection body = %s, want it to name availability", rec.Body.String())
	}

	// 3. A model that is available but cannot take images is rejected too —
	// image analysis silently fails on it, so the setting has no value.
	blindBody, _ := json.Marshal(map[string]string{
		"model":      "blind-model",
		"providerId": "test",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/models/image", bytes.NewReader(blindBody))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST non-vision model status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("support images")) {
		t.Fatalf("non-vision rejection body = %s, want it to name image support", rec.Body.String())
	}

	// 3b. Neither rejection persisted anything: the pref is still empty.
	req = httptest.NewRequest(http.MethodGet, "/api/models/image", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET after rejected POSTs status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode GET response after rejected POSTs: %v", err)
	}
	if got.Model != "" || got.ProviderID != "" {
		t.Fatalf("pref after rejected POSTs = %+v, want empty (a rejected save must not persist)", got)
	}

	// 4. Set a vision-capable model and provider.
	body, _ := json.Marshal(map[string]string{
		"model":      "eye-model",
		"providerId": "test",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/models/image", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/models/image status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// Verify persisted via GET.
	req = httptest.NewRequest(http.MethodGet, "/api/models/image", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if got.Model != "eye-model" || got.ProviderID != "test" {
		t.Fatalf("stored pref = %+v, want model=eye-model provider_id=test", got)
	}

	// 4b. A model whose provider listing does not claim vision, but whose
	// probed capability in model_capabilities says SupportsImages=true, is
	// accepted — matching /api/models.
	probedBody, _ := json.Marshal(map[string]string{
		"model":      "probed-vl",
		"providerId": "test",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/models/image", bytes.NewReader(probedBody))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST probed vision model status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// 5. Clear back to empty (inherit from session).
	clearBody, _ := json.Marshal(map[string]string{
		"model":      "",
		"providerId": "",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/models/image", bytes.NewReader(clearBody))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear POST /api/models/image status = %d, want 200", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/models/image", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if got.Model != "" || got.ProviderID != "" {
		t.Fatalf("cleared pref = %+v, want empty", got)
	}
}
