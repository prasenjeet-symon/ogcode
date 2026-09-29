package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
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

// ogxRefresh drives POST /api/ogx/refresh and returns the parsed payload.
func ogxRefresh(t *testing.T, s *Server) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleOGXRefresh(rec, httptest.NewRequest(http.MethodPost, "/api/ogx/refresh", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh: status %d, body %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("refresh: bad body: %v", err)
	}
	return out
}

// TestOGXRefreshFollowsTheGateway pins the live plan check: the gateway's
// catalogue is the plan, so a plan bought after connecting is picked up — and
// one that has lapsed dropped — by asking again, without a reconnect. A token
// the gateway no longer accepts, or a gateway that cannot be reached, changes
// nothing and says which it was.
func TestOGXRefreshFollowsTheGateway(t *testing.T) {
	var (
		mu       sync.Mutex
		models   []string
		refusing string
	)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if refusing != "" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": refusing})
			return
		}
		data := []map[string]string{}
		for _, id := range models {
			data = append(data, map[string]string{"id": id, "object": "model", "owned_by": "oglab"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
	}))
	defer gateway.Close()
	t.Setenv("OGX_GATEWAY_URL", gateway.URL+"/v1")
	set := func(ids []string, refuse string) {
		mu.Lock()
		models, refusing = ids, refuse
		mu.Unlock()
	}

	// Connected before buying: stored planless, no provider.
	s := ogxTestServer(t)
	state := ogxConnect(t, s).Query().Get("state")
	s.handleOGXCallback(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet,
		"/api/ogx/callback?state="+url.QueryEscape(state)+"&token=ogx-tok-later&email=p%40oz.dev&plan=none", nil))
	if p := s.registry.Get(provider.OGXProviderID); p != nil {
		t.Fatal("a planless link registered a provider")
	}

	// The plan is bought on the web side: a refresh finds it and registers it.
	set([]string{"deepseek-v4.1-flash", "glm-5.3-flash"}, "")
	st := ogxRefresh(t, s)
	if st["check"] != "ok" || st["models"] != float64(2) || st["plan"] != provider.OGXProviderID {
		t.Fatalf("after buying = %v, want an ok check that found the plan's 2 models", st)
	}
	if p := s.registry.Get(provider.OGXProviderID); p == nil {
		t.Fatal("refresh found a plan but did not register the provider")
	}

	// The plan lapses: the next refresh records it and drops the provider.
	set(nil, "")
	if st := ogxRefresh(t, s); st["check"] != "ok" || st["plan"] != "none" {
		t.Fatalf("after lapsing = %v, want the link recorded planless", st)
	}
	if p := s.registry.Get(provider.OGXProviderID); p != nil {
		t.Fatal("a lapsed plan's provider is still registered")
	}

	// A revoked token and an unreachable gateway say which, and change nothing.
	set([]string{"glm-5.3-flash"}, "a valid install token is required")
	if st := ogxRefresh(t, s); st["check"] != "revoked" || st["plan"] != "none" {
		t.Fatalf("revoked = %v, want check revoked and the plan untouched", st)
	}
	set([]string{"glm-5.3-flash"}, "a valid client signature is required")
	if st := ogxRefresh(t, s); st["check"] != "unreachable" {
		t.Fatalf("unsigned client = %v, want unreachable rather than revoked", st)
	}
	gateway.Close()
	if st := ogxRefresh(t, s); st["check"] != "unreachable" || st["plan"] != "none" {
		t.Fatalf("gateway down = %v, want unreachable and the plan untouched", st)
	}
	// The token never appears in the payload.
	if b, _ := json.Marshal(ogxRefresh(t, s)); strings.Contains(string(b), "ogx-tok-later") {
		t.Fatalf("refresh leaks the token: %s", b)
	}
}

func TestOGXRefreshWithoutALink(t *testing.T) {
	s := ogxTestServer(t)
	if st := ogxRefresh(t, s); st["connected"] != false || st["check"] != nil {
		t.Fatalf("refresh with no link = %v, want plain disconnected", st)
	}
}
