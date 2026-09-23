package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
)

// ogxTestServer is newTestServer with a bound port, so the connect flow has a
// real number to build its loopback redirect_uri from. The shared helper leaves
// the port unset on purpose — other tests do not need one.
func ogxTestServer(t *testing.T) *Server {
	t.Helper()
	s := newTestServer(t)
	s.port.Store(7777)
	return s
}

// ogxConnect drives POST /api/ogx/connect and returns the parsed hand-off URL
// the frontend would open. The request carries a LAN Host on purpose: the
// redirect must be loopback regardless of how the browser reached the server,
// because the web side refuses a non-loopback redirect.
func ogxConnect(t *testing.T, s *Server) *url.URL {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://192.168.1.50:7777/api/ogx/connect", nil)
	rec := httptest.NewRecorder()
	s.handleOGXConnect(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("connect: status %d, body %s", rec.Code, rec.Body.String())
	}
	var body struct{ URL string }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("connect: bad body: %v", err)
	}
	u, err := url.Parse(body.URL)
	if err != nil {
		t.Fatalf("connect: unparseable url %q: %v", body.URL, err)
	}
	return u
}

func ogxStatus(t *testing.T, s *Server) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleOGXStatus(rec, httptest.NewRequest(http.MethodGet, "/api/ogx/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: status %d", rec.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("status: bad body: %v", err)
	}
	return out
}

func TestOGXConnectFlow(t *testing.T) {
	s := ogxTestServer(t)

	// Before anything: disconnected.
	if st := ogxStatus(t, s); st["connected"] != false {
		t.Fatalf("expected disconnected initially, got %v", st)
	}
	if p := s.registry.Get(provider.OGXProviderID); p != nil {
		t.Fatalf("ogx provider registered before connecting: %v", p.ID())
	}

	u := ogxConnect(t, s)
	state := u.Query().Get("state")
	if state == "" {
		t.Fatal("connect URL carries no state")
	}
	// The redirect is pinned to loopback, not built from the LAN Host the
	// browser used — the web side refuses anything else.
	if got := u.Query().Get("redirect_uri"); got != "http://127.0.0.1:7777/api/ogx/callback" {
		t.Fatalf("redirect_uri = %q", got)
	}

	// The browser comes back with the state and a token.
	cb := httptest.NewRequest(http.MethodGet,
		"/api/ogx/callback?state="+url.QueryEscape(state)+"&token=ogx-tok-1&email=p%40oz.dev&plan=OGX+Pro", nil)
	rec := httptest.NewRecorder()
	s.handleOGXCallback(rec, cb)
	if rec.Code != http.StatusOK {
		t.Fatalf("callback: status %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "OGX connected") {
		t.Fatalf("callback page missing confirmation: %s", rec.Body.String())
	}
	// Connecting swaps the provider into the live registry, no restart needed.
	p := s.registry.Get(provider.OGXProviderID)
	if p == nil {
		t.Fatal("ogx provider not registered after a plan-carrying callback")
	}

	st := ogxStatus(t, s)
	if st["connected"] != true || st["email"] != "p@oz.dev" || st["plan"] != "OGX Pro" {
		t.Fatalf("unexpected status after connect: %v", st)
	}
	// The token must never appear in the status payload.
	if b, _ := json.Marshal(st); strings.Contains(string(b), "ogx-tok-1") {
		t.Fatalf("status leaks the token: %s", b)
	}

	// A replayed redirect fails: the state was consumed.
	rec = httptest.NewRecorder()
	s.handleOGXCallback(rec, cb)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("replayed callback: status %d, want 400", rec.Code)
	}

	// Disconnect forgets the link and drops the provider from the registry.
	rec = httptest.NewRecorder()
	s.handleOGXDisconnect(rec, httptest.NewRequest(http.MethodDelete, "/api/ogx", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("disconnect: status %d", rec.Code)
	}
	if st := ogxStatus(t, s); st["connected"] != false {
		t.Fatalf("expected disconnected after disconnect, got %v", st)
	}
	if p := s.registry.Get(provider.OGXProviderID); p != nil {
		t.Fatalf("ogx provider still registered after disconnect: %v", p.ID())
	}
}

// TestOGXPlanlessLinkRegistersNoProvider pins the registration gate: the
// gateway's catalogue IS the plan, so a link without one would register a
// provider that can serve nothing yet still outranks the other providers.
func TestOGXPlanlessLinkRegistersNoProvider(t *testing.T) {
	s := ogxTestServer(t)

	state := ogxConnect(t, s).Query().Get("state")
	rec := httptest.NewRecorder()
	s.handleOGXCallback(rec, httptest.NewRequest(http.MethodGet,
		"/api/ogx/callback?state="+url.QueryEscape(state)+"&token=ogx-tok-none&plan=none", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("callback: status %d, body %s", rec.Code, rec.Body.String())
	}
	if st := ogxStatus(t, s); st["connected"] != true {
		t.Fatalf("planless link should still be stored as connected: %v", st)
	}
	if p := s.registry.Get(provider.OGXProviderID); p != nil {
		t.Fatalf("planless link registered a provider: %v", p.ID())
	}
}

func TestOGXCallbackRejectsBadState(t *testing.T) {
	s := ogxTestServer(t)

	// Unknown state: rejected, nothing stored.
	rec := httptest.NewRecorder()
	s.handleOGXCallback(rec, httptest.NewRequest(http.MethodGet,
		"/api/ogx/callback?state=never-minted&token=tok", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown state: status %d, want 400", rec.Code)
	}
	if st := ogxStatus(t, s); st["connected"] != false {
		t.Fatalf("unknown state stored a connection: %v", st)
	}

	// Valid state but no token: rejected, and the state is spent — the flow
	// restarts rather than accepting a token for a state that already failed.
	state := ogxConnect(t, s).Query().Get("state")
	rec = httptest.NewRecorder()
	s.handleOGXCallback(rec, httptest.NewRequest(http.MethodGet,
		"/api/ogx/callback?state="+url.QueryEscape(state), nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing token: status %d, want 400", rec.Code)
	}
	if st := ogxStatus(t, s); st["connected"] != false {
		t.Fatalf("missing token stored a connection: %v", st)
	}
}
