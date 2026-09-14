package master_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode-control-plane/internal/auth"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/bus"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/master"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/pairing"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/registry"
)

// newPanelServer builds a master with an operator gate and a UI proxy handler
// mounted over a bare mux, so the apex console can be exercised over HTTP.
func newPanelServer(t *testing.T, password string) (*master.Server, *httptest.Server) {
	t.Helper()
	gate, err := auth.NewGate(nil, password, time.Hour, "", false)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	srv := master.New(master.Options{
		Registry: registry.New(),
		Auth:     pairing.New(testSecret, time.Minute),
		Bus:      bus.New(256),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Gate:     gate,
	})
	mux := http.NewServeMux()
	path, handler := srv.Handler()
	mux.Handle(path, handler)
	hs := httptest.NewServer(srv.UIProxyHandler(mux))
	t.Cleanup(hs.Close)
	return srv, hs
}

func TestApexConsole_ListsWorkersWithLinks(t *testing.T) {
	srv, hs := newPanelServer(t, "op-pass")

	// Register two workers directly in the registry (the ConnectRPC path is
	// covered elsewhere); one online, one offline.
	now := time.Now()
	tok, _ := pairing.New(testSecret, time.Minute).Mint(now)
	srv.Registry().Add("abc123", "laptop", []string{"linux", "docker"},
		[]registry.Workspace{{Path: "/srv/a", Name: "a", Branch: "main", Present: true}}, tok, now)
	srv.Registry().Add("def456", "desktop", nil, nil, tok, now)

	// Apex host (no worker subdomain) → the console, operator-gated. Log in first
	// (don't follow the 303 so we can capture the session cookie).
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	login, err := noRedirect.PostForm(hs.URL+"/__operator/login",
		map[string][]string{"password": {"op-pass"}})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	login.Body.Close()
	cookies := login.Cookies()
	if len(cookies) == 0 {
		t.Fatal("login did not set a session cookie")
	}

	req, _ := http.NewRequest(http.MethodGet, hs.URL+"/", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatalf("get apex: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("apex status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}
	for _, want := range []string{"laptop", "desktop", "abc123", "def456", "online", "offline"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("apex console missing %q", want)
		}
	}
	// The link must point at the worker subdomain on the same host.
	if !strings.Contains(string(body), "http://abc123."+hs.Listener.Addr().String()+"/") {
		t.Errorf("apex console missing worker link for abc123 (body=%s)", body)
	}
}

func TestApexConsole_RequiresOperatorLogin(t *testing.T) {
	_, hs := newPanelServer(t, "op-pass")

	resp, err := http.Get(hs.URL + "/")
	if err != nil {
		t.Fatalf("get apex: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unauthenticated apex status = %d, want 200 (login page)", resp.StatusCode)
	}
	if !strings.Contains(string(body), "sign in") {
		t.Errorf("unauthenticated apex should render the login page (body=%s)", body)
	}
}

func TestApexConsole_EmptyState(t *testing.T) {
	_, hs := newPanelServer(t, "")

	resp, err := http.Get(hs.URL + "/")
	if err != nil {
		t.Fatalf("get apex: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if !strings.Contains(string(body), "No workers connected yet.") {
		t.Errorf("empty apex should show the empty state (body=%s)", body)
	}
}
