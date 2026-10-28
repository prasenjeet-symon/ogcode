package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// vendorConfigEntry looks a provider's row up in a decoded
// GET /api/providers/config body.
func vendorConfigEntry(t *testing.T, body []byte, id string) providerConfigResponse {
	t.Helper()
	var cfgs []providerConfigResponse
	if err := json.Unmarshal(body, &cfgs); err != nil {
		t.Fatalf("decode provider configs: %v (body: %s)", err, body)
	}
	for _, c := range cfgs {
		if c.ProviderID == id {
			return c
		}
	}
	t.Fatalf("provider %q not listed in %s", id, body)
	return providerConfigResponse{}
}

// TestVendorSlotSavesListsAndRegisters verifies the whole save path end to end
// for a vendor id that is not one of the four first-party slots: the POST row
// lands in the global DB, the reload registers a live provider under its id,
// and the GET lists it after the builtins with the key masked and no env flags.
func TestVendorSlotSavesListsAndRegisters(t *testing.T) {
	srv := newTestServer(t)
	h := srv.routes()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/api/providers/config/testvendor",
		strings.NewReader(`{"apiKey":"sk-vendor","baseUrl":"https://example.com/v1"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST vendor config = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// The reload should have registered the vendor slot as a live provider.
	if srv.registry.Get("testvendor") == nil {
		t.Fatal("expected testvendor to be registered after saving a config")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/providers/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET provider configs = %d, want 200", rec.Code)
	}
	resp := vendorConfigEntry(t, rec.Body.Bytes(), "testvendor")
	if resp.APIKey != "__SET__" {
		t.Fatalf("apiKey = %q, want masked __SET__", resp.APIKey)
	}
	if resp.BaseURL != "https://example.com/v1" || resp.EffectiveBaseURL != "https://example.com/v1" {
		t.Fatalf("baseUrl/effective = %q/%q, want the saved endpoint", resp.BaseURL, resp.EffectiveBaseURL)
	}
	if resp.EnvKeySet || resp.EnvBaseURLSet {
		t.Fatal("a vendor slot must not report an env-sourced key or base URL")
	}

	// Ordering: the vendor rows come after the four builtins — the last
	// entry in a single-vendor list is the vendor.
	var cfgs []providerConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &cfgs); err != nil {
		t.Fatalf("decode provider configs: %v", err)
	}
	if len(cfgs) != 5 || cfgs[len(cfgs)-1].ProviderID != "testvendor" {
		t.Fatalf("expected 4 builtins then testvendor, got %d entries", len(cfgs))
	}
}

// TestVendorSlotRegistersWithoutAPIKey pins the zero-config case: a vendor slot
// saved with an empty key but a valid base URL still registers — a local
// vLLM or LM Studio answers without authentication.
func TestVendorSlotRegistersWithoutAPIKey(t *testing.T) {
	srv := newTestServer(t)
	h := srv.routes()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/api/providers/config/keyless",
		strings.NewReader(`{"apiKey":"","baseUrl":"http://127.0.0.1:9999/v1"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST keyless vendor config = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if p := srv.registry.Get("keyless"); p == nil {
		t.Fatal("expected a keyless vendor slot with a baseUrl to register")
	}
	// And the GET reports it with an empty (not sentinel) key.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/providers/config", nil))
	resp := vendorConfigEntry(t, rec.Body.Bytes(), "keyless")
	if resp.APIKey != "" {
		t.Fatalf("apiKey = %q, want empty for a keyless vendor slot", resp.APIKey)
	}
}

// TestDeleteVendorSlotCovers the removal path: a saved vendor slot disappears
// from both the DB row set and the live registry, the builtins and ogx refuse
// to be deleted, and deleting an already-removed id stays a clean 200.
func TestDeleteVendorSlot(t *testing.T) {
	srv := newTestServer(t)
	h := srv.routes()

	// Save a vendor slot to delete.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/api/providers/config/goner",
		strings.NewReader(`{"apiKey":"sk-g","baseUrl":"https://gone.example/v1"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST vendor config = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if srv.registry.Get("goner") == nil {
		t.Fatal("expected goner registered before the delete")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/providers/config/goner", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE vendor = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var res map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || res["removed"] != true {
		t.Fatalf("DELETE body = %s, want {removed: true}", rec.Body.String())
	}
	if srv.registry.Get("goner") != nil {
		t.Fatal("expected goner unregistered after the delete")
	}
	if _, err := session.GetProviderConfig(srv.globalDB, "goner"); err != nil {
		t.Fatalf("read back after delete: %v", err)
	}
	rows, err := session.GetAllProviderConfigs(srv.globalDB)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	for _, row := range rows {
		if row.ProviderID == "goner" {
			t.Fatal("expected the goner row removed from the DB")
		}
	}

	// Deleting an already-deleted id is a tolerable no-op.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/providers/config/goner", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("second DELETE = %d, want 200 (idempotent)", rec.Code)
	}

	// The builtins and ogx are not removable through this route.
	for _, id := range []string{"openai", "ogx"} {
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/providers/config/"+id, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("DELETE %s = %d, want 400", id, rec.Code)
		}
	}
}
