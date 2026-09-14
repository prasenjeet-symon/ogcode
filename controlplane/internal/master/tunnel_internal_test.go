package master

import (
	"bufio"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/prasenjeet-symon/ogcode-control-plane/internal/auth"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/bus"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/pairing"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/registry"
)

const testRouteSecret = "test-pairing-secret"

// TestTunnelKey pins the composite route keying: empty route = legacy bare
// worker id, non-empty = "<workerID>-<route>".
func TestTunnelKey(t *testing.T) {
	for _, tc := range []struct {
		id, route, want string
	}{
		{"abc123", "", "abc123"},
		{"abc123", "treemain", "abc123-treemain"},
		{"abc123", "feature-fix-auth", "abc123-feature-fix-auth"},
	} {
		if got := tunnelKey(tc.id, tc.route); got != tc.want {
			t.Errorf("tunnelKey(%q, %q) = %q, want %q", tc.id, tc.route, got, tc.want)
		}
	}
}

// newTunnelTestServer builds a minimal master for tunnel/UI-proxy tests.
func newTunnelTestServer(t *testing.T) *Server {
	t.Helper()
	gate, err := auth.NewGate(nil, "", time.Hour, "", false)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	return New(Options{
		Registry: registry.New(),
		Auth:     pairing.New(testRouteSecret, time.Minute),
		Bus:      bus.New(256),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Gate:     gate,
	})
}

// newGatedTunnelTestServer builds a tunnel-test master whose operator gate is
// ENABLED (legacy single-password mode — the simplest enabled gate; the proxy
// gate branch is mode-agnostic, it only consults Enabled/Authenticated).
// newTunnelTestServer's gate is disabled, which is why the routing tests above
// never exercised the gate block in UIProxyHandler.
func newGatedTunnelTestServer(t *testing.T, password string) *Server {
	t.Helper()
	gate, err := auth.NewGate(nil, password, time.Hour, "", false)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	return New(Options{
		Registry: registry.New(),
		Auth:     pairing.New(testRouteSecret, time.Minute),
		Bus:      bus.New(256),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Gate:     gate,
	})
}

// deadYamuxPair builds a closed yamux session for registry entries that must
// NOT serve a request (the proxy's ErrorHandler turns a dead session into a
// 502 rather than silently falling through).
func deadYamuxPair(t *testing.T) *yamux.Session {
	t.Helper()
	c1, c2 := net.Pipe()
	c1.Close()
	c2.Close()
	sess, err := yamux.Client(c1, nil)
	if err != nil {
		t.Fatalf("yamux client over closed pipe: %v", err)
	}
	return sess
}

// serveOriginOverYamux runs a worker-side yamux server on the given conn that
// forwards every accepted HTTP request to origin and writes the response back
// over the stream.
func serveOriginOverYamux(conn net.Conn, originURL string) {
	serverSess, err := yamux.Server(conn, nil)
	if err != nil {
		return
	}
	for {
		stream, err := serverSess.Accept()
		if err != nil {
			return
		}
		go func() {
			defer stream.Close()
			req, err := http.ReadRequest(bufio.NewReader(stream))
			if err != nil {
				return
			}
			req.URL.Scheme = "http"
			req.URL.Host = originURL
			req.RequestURI = ""
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			_ = resp.Write(stream)
		}()
	}
}

// TestUIProxy_RoutesCompositeHostToWorktreeServer pins the Phase-2 routing:
// a request to "<workerID>-<label>.<host>" is proxied to the yamux session the
// worker registered under that composite key — a bare-worker-id tunnel with the
// same id does NOT capture it.
func TestUIProxy_RoutesCompositeHostToWorktreeServer(t *testing.T) {
	srv := newTunnelTestServer(t)

	// A fake origin the "worker" serves over its end of the yamux pair.
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "worktree-ui")
	}))
	defer origin.Close()

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	// Master side = yamux client (the Tunnel handler would build this).
	clientSess, err := yamux.Client(clientConn, nil)
	if err != nil {
		t.Fatalf("yamux client: %v", err)
	}
	go serveOriginOverYamux(serverConn, origin.Listener.Addr().String())

	// Register the composite key AND decoys (closed sessions under the bare id
	// and a wrong route) to prove the host's route label decides which tunnel
	// serves the request.
	srv.tunnels.set("abc123", "wrong", deadYamuxPair(t))
	srv.tunnels.set("abc123", "treemain", clientSess)
	srv.tunnels.set("abc123", "", deadYamuxPair(t))

	base := http.NewServeMux()
	proxy := srv.UIProxyHandler(base)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://abc123-treemain.example/", nil)
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("tunneled GET = %d, body %q", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "worktree-ui" {
		t.Fatalf("tunneled GET body = %q, want worktree-ui", got)
	}
}

// TestUIProxy_BareHostStillRoutes pins that a legacy tunnel (no route) still
// answers under the bare worker id, unchanged from Phase 1.
func TestUIProxy_BareHostStillRoutes(t *testing.T) {
	srv := newTunnelTestServer(t)

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "legacy-ui")
	}))
	defer origin.Close()

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	clientSess, err := yamux.Client(clientConn, nil)
	if err != nil {
		t.Fatalf("yamux client: %v", err)
	}
	go serveOriginOverYamux(serverConn, origin.Listener.Addr().String())

	srv.tunnels.set("def456", "", clientSess)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://def456.example/", nil)
	srv.UIProxyHandler(http.NewServeMux()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "legacy-ui" {
		t.Fatalf("legacy tunneled GET = %d %q", rec.Code, rec.Body.String())
	}
}

// TestUIProxy_RoutesHyphenatedWorkerID pins that routing is an exact whole-label
// match, NOT a split on the first hyphen: a worker whose id itself contains
// hyphens (validWorkerID accepts them) still routes its worktree subdomain to
// its own tunnel.
func TestUIProxy_RoutesHyphenatedWorkerID(t *testing.T) {
	srv := newTunnelTestServer(t)

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "hyphenated-worker-ui")
	}))
	defer origin.Close()

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	clientSess, err := yamux.Client(clientConn, nil)
	if err != nil {
		t.Fatalf("yamux client: %v", err)
	}
	go serveOriginOverYamux(serverConn, origin.Listener.Addr().String())

	srv.tunnels.set("my-laptop-01", "treemain", clientSess)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://my-laptop-01-treemain.example/", nil)
	srv.UIProxyHandler(http.NewServeMux()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "hyphenated-worker-ui" {
		t.Fatalf("hyphenated-id tunneled GET = %d %q", rec.Code, rec.Body.String())
	}
}

// TestTunnelRegistry_Routes pins the console-facing enumeration: routes(workerID)
// returns only that worker's non-empty route labels, sorted.
func TestTunnelRegistry_Routes(t *testing.T) {
	srv := newTunnelTestServer(t)

	srv.tunnels.set("abc123", "zeta", deadYamuxPair(t))
	srv.tunnels.set("abc123", "alpha", deadYamuxPair(t))
	srv.tunnels.set("abc123", "", deadYamuxPair(t)) // legacy bare tunnel: excluded
	srv.tunnels.set("def456", "alpha", deadYamuxPair(t))
	srv.tunnels.set("abc123", "alpha", deadYamuxPair(t)) // replace: still one entry

	got := srv.tunnels.routes("abc123")
	if len(got) != 2 || got[0] != "alpha" || got[1] != "zeta" {
		t.Fatalf("routes(abc123) = %v, want [alpha zeta]", got)
	}
	if got := srv.tunnels.routes("nosuch"); got != nil {
		t.Fatalf("routes(nosuch) = %v, want nil", got)
	}
}

// gatedProxyWorktreeUI builds a gated master with one live worktree tunnel
// backed by an origin that echoes its path, and returns the UIProxyHandler.
// Every gate test below drives this one tunnel via subGet.
func gatedProxyWorktreeUI(t *testing.T, password string) http.Handler {
	t.Helper()
	srv := newGatedTunnelTestServer(t, password)

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "path="+r.URL.Path)
	}))
	t.Cleanup(origin.Close)

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })
	t.Cleanup(func() { _ = serverConn.Close() })

	clientSess, err := yamux.Client(clientConn, nil)
	if err != nil {
		t.Fatalf("yamux client: %v", err)
	}
	go serveOriginOverYamux(serverConn, origin.Listener.Addr().String())

	srv.tunnels.set("abc123", "treemain", clientSess)

	return srv.UIProxyHandler(http.NewServeMux())
}

// subGet issues a GET against proxy with a worker-subdomain Host header and an
// optional Accept header / session cookie, recording the response.
func subGet(t *testing.T, proxy http.Handler, path, accept string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://abc123-treemain.panel.example"+path, nil)
	req.Host = "abc123-treemain.panel.example"
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	proxy.ServeHTTP(rec, req)
	return rec
}

// --- Phase G: the operator gate on proxied worker-subdomain requests ---------

// TestUIProxy_WorkerSubdomainGated pins the §G bullet: with the gate enabled,
// an unauthenticated request on a worker subdomain NEVER reaches the tunneled
// UI. A browser navigation (Accept: text/html) is answered with the login
// page; a non-HTML request (the UI's own /api/* fetches) gets a bare 401.
func TestUIProxy_WorkerSubdomainGated(t *testing.T) {
	proxy := gatedProxyWorktreeUI(t, "op-pass")

	// Browser navigation → the login page (200), never the origin.
	htmlRec := subGet(t, proxy, "/", "text/html,application/xhtml+xml", nil)
	if htmlRec.Code != http.StatusOK {
		t.Fatalf("unauthenticated HTML GET = %d, want 200 login page", htmlRec.Code)
	}
	if !strings.Contains(htmlRec.Body.String(), "sign in") {
		t.Errorf("unauthenticated HTML GET should render the login page (body=%s)", htmlRec.Body.String())
	}
	if strings.Contains(htmlRec.Body.String(), "path=") {
		t.Errorf("unauthenticated HTML GET must not reach the tunneled origin (body=%s)", htmlRec.Body.String())
	}

	// Non-HTML (the UI's API fetches) → bare 401, no origin contact.
	apiRec := subGet(t, proxy, "/api/config", "application/json", nil)
	if apiRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated non-HTML GET = %d, want 401", apiRec.Code)
	}
	if !strings.Contains(apiRec.Body.String(), "operator login required") {
		t.Errorf("401 body = %q, want the operator-login-required error", apiRec.Body.String())
	}
}

// TestUIProxy_WorkerSubdomainLoginLogoutExempt pins that the operator
// login/logout endpoints are reachable ON the worker subdomain while the gate
// is closed — otherwise a browser landing on a worktree URL could never sign
// in — and that a successful login there yields a cookie the gate accepts.
func TestUIProxy_WorkerSubdomainLoginLogoutExempt(t *testing.T) {
	proxy := gatedProxyWorktreeUI(t, "op-pass")

	// GET renders the form.
	formRec := subGet(t, proxy, operatorLoginPath, "text/html", nil)
	if formRec.Code != http.StatusOK || !strings.Contains(formRec.Body.String(), "sign in") {
		t.Fatalf("worker-subdomain login GET = %d, want the form (body=%s)", formRec.Code, formRec.Body.String())
	}

	// POST the right password → 303 + session cookie.
	postRec := httptest.NewRecorder()
	post := httptest.NewRequest(http.MethodPost, "http://abc123-treemain.panel.example"+operatorLoginPath,
		strings.NewReader("username=&password=op-pass"))
	post.Host = "abc123-treemain.panel.example"
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	proxy.ServeHTTP(postRec, post)
	if postRec.Code != http.StatusSeeOther {
		t.Fatalf("worker-subdomain login POST = %d, want 303 (body=%s)", postRec.Code, postRec.Body.String())
	}
	cookies := postRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("worker-subdomain login did not set a session cookie")
	}

	// The cookie issued on the subdomain authenticates the proxied UI.
	okRec := subGet(t, proxy, "/", "text/html", cookies)
	if okRec.Code != http.StatusOK || okRec.Body.String() != "path=/" {
		t.Fatalf("authenticated tunneled GET = %d %q, want the origin answer", okRec.Code, okRec.Body.String())
	}

	// Wrong password → 401 with the form, still no origin contact.
	badRec := httptest.NewRecorder()
	bad := httptest.NewRequest(http.MethodPost, "http://abc123-treemain.panel.example"+operatorLoginPath,
		strings.NewReader("username=&password=wrong"))
	bad.Host = "abc123-treemain.panel.example"
	bad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	proxy.ServeHTTP(badRec, bad)
	if badRec.Code != http.StatusUnauthorized || !strings.Contains(badRec.Body.String(), "Incorrect") {
		t.Fatalf("bad worker-subdomain login = %d, want 401 form (body=%s)", badRec.Code, badRec.Body.String())
	}

	// Logout clears the session; the UI gates again.
	logoutRec := subGet(t, proxy, operatorLogoutPath, "text/html", cookies)
	if logoutRec.Code != http.StatusSeeOther {
		t.Fatalf("logout = %d, want 303", logoutRec.Code)
	}
	afterRec := subGet(t, proxy, "/", "text/html", logoutRec.Result().Cookies())
	if strings.Contains(afterRec.Body.String(), "path=") {
		t.Fatalf("post-logout request still reached the tunneled origin (body=%s)", afterRec.Body.String())
	}
}

// TestUIProxy_WorkerSubdomainAuthenticatedPinsThrough pins the happy path of
// the §G gate: a request carrying a valid operator session cookie IS proxied to
// the worker UI — the gate must not over-block.
func TestUIProxy_WorkerSubdomainAuthenticatedPassesThrough(t *testing.T) {
	proxy := gatedProxyWorktreeUI(t, "op-pass")

	postRec := httptest.NewRecorder()
	post := httptest.NewRequest(http.MethodPost, "http://abc123-treemain.panel.example"+operatorLoginPath,
		strings.NewReader("password=op-pass"))
	post.Host = "abc123-treemain.panel.example"
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	proxy.ServeHTTP(postRec, post)
	if postRec.Code != http.StatusSeeOther {
		t.Fatalf("login POST = %d, want 303 (body=%s)", postRec.Code, postRec.Body.String())
	}
	cookies := postRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login did not set a session cookie")
	}

	// Both a navigation and an API fetch pass through to the origin.
	for _, tc := range []struct{ path, accept string }{
		{"/", "text/html,application/xhtml+xml"},
		{"/api/config", "application/json"},
	} {
		rec := subGet(t, proxy, tc.path, tc.accept, cookies)
		if rec.Code != http.StatusOK || rec.Body.String() != "path="+tc.path {
			t.Fatalf("authenticated GET %s (Accept %s) = %d %q, want the origin answer",
				tc.path, tc.accept, rec.Code, rec.Body.String())
		}
	}
}

// TestUIProxy_WorkerSubdomainDisabledGatePassesThrough pins the other half of
// the gate contract: with NO operator configured (the mode the routing tests
// run in), worker subdomains serve the UI unauthenticated — the gate must not
// appear "enabled by default" and lock out a single-operator setup.
func TestUIProxy_WorkerSubdomainDisabledGatePassesThrough(t *testing.T) {
	proxy := gatedProxyWorktreeUI(t, "") // empty password → ModeDisabled

	rec := subGet(t, proxy, "/", "text/html", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "path=/" {
		t.Fatalf("disabled-gate tunneled GET = %d %q, want the origin answer", rec.Code, rec.Body.String())
	}
}
