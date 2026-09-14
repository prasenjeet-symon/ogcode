package master

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"golang.org/x/crypto/bcrypt"

	"github.com/prasenjeet-symon/ogcode-control-plane/internal/auth"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/bus"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/pairing"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/registry"
)

// TestTunnelMatchesAllowlist pins the identifier match rules on the pure
// function: route-label equality, workspace-name equality, workspace-path
// suffix (segment-aligned), and the legacy bare-tunnel worker-id rule.
func TestTunnelMatchesAllowlist(t *testing.T) {
	info := registry.WorkerInfo{
		ID:   "abc123",
		Name: "laptop",
		Workspaces: []registry.Workspace{
			{Name: "main", Path: "/srv/repo-main", Present: true},
			{Name: "evolved", Path: "/srv/other/repo-evolved", Present: true},
		},
	}
	for _, tc := range []struct {
		name    string
		label   string
		allowed []string
		want    bool
	}{
		{"route label match", "abc123-treemain", []string{"treemain"}, true},
		{"route label no match", "abc123-treemain", []string{"other"}, false},
		{"workspace name match", "abc123-treemain", []string{"main"}, true},
		{"workspace path exact match", "abc123-treemain", []string{"/srv/repo-main"}, true},
		{"workspace path suffix match", "abc123-treemain", []string{"repo-main"}, true},
		{"workspace path suffix segment-aligned", "abc123-treemain", []string{"evo"}, false},
		{"second workspace name", "abc123-treemain", []string{"evolved"}, true},
		{"legacy bare label as worker id", "abc123", []string{"abc123"}, true},
		{"legacy bare via workspace name", "abc123", []string{"main"}, true},
		{"legacy bare no match", "abc123", []string{"treemain"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tunnelMatchesAllowlist(tc.label, info, tc.allowed); got != tc.want {
				t.Fatalf("tunnelMatchesAllowlist(%q) = %v, want %v", tc.label, got, tc.want)
			}
		})
	}
}

// allowlistProxy builds a master whose gate is in accounts mode backed by a
// real bbolt store, registers worker "abc123" with two workspaces, and wires
// one live tunnel for route "treemain" (plus optionally the legacy bare
// tunnel). Returns the UIProxyHandler, the gate for issuing cookies, and the
// store for admin-side allowlist edits.
func allowlistProxy(t *testing.T, aliceWorkspaces []string, bareTunnel bool) (http.Handler, *auth.Gate, *registry.Store) {
	t.Helper()
	storePath := t.TempDir() + "/cp.db"
	store, err := registry.Open(storePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	hash, err := bcrypt.GenerateFromPassword([]byte("pw-alice"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := store.PutUser("alice", registry.UserRecord{
		Hash:       string(hash),
		CreatedAt:  time.Now(),
		Workspaces: aliceWorkspaces,
	}); err != nil {
		t.Fatalf("seed alice: %v", err)
	}
	// A second account so rm/last-account guards never interfere.
	hash2, err := bcrypt.GenerateFromPassword([]byte("pw-bob"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("seed hash: %v", err)
	}
	if err := store.PutUser("bob", registry.UserRecord{Hash: string(hash2), CreatedAt: time.Now()}); err != nil {
		t.Fatalf("seed bob: %v", err)
	}

	gate, err := auth.NewGate(store, "", time.Hour, "", false)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	srv := New(Options{
		Registry: registry.NewWithStore(store, slog.New(slog.NewTextHandler(io.Discard, nil))),
		Auth:     pairing.New(testRouteSecret, time.Minute),
		Bus:      bus.New(256),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Gate:     gate,
	})
	now := time.Now()
	pairer := pairing.New(testRouteSecret, time.Minute)
	tok, err := pairer.Mint(now)
	if err != nil {
		t.Fatalf("mint worker token: %v", err)
	}
	srv.reg.Add("abc123", "laptop", nil, nil, tok, now)
	srv.reg.SetWorkspaces("abc123", []registry.Workspace{
		{Name: "main", Path: "/srv/repo-main", Present: true},
		{Name: "evolved", Path: "/srv/other/repo-evolved", Present: true},
	})

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
	if bareTunnel {
		bareConn, bareServerConn := net.Pipe()
		t.Cleanup(func() { _ = bareConn.Close() })
		t.Cleanup(func() { _ = bareServerConn.Close() })
		bareSess, err := yamux.Client(bareConn, nil)
		if err != nil {
			t.Fatalf("yamux bare client: %v", err)
		}
		go serveOriginOverYamux(bareServerConn, origin.Listener.Addr().String())
		srv.tunnels.set("abc123", "", bareSess)
	}
	return srv.UIProxyHandler(http.NewServeMux()), gate, store
}

// loginAlice posts operator credentials on the subdomain and returns the
// session cookies the scoped login set.
func loginAlice(t *testing.T, proxy http.Handler, username, password string) []*http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	post := httptest.NewRequest(http.MethodPost, "http://abc123-treemain.panel.example"+operatorLoginPath,
		strings.NewReader("username="+username+"&password="+password))
	post.Host = "abc123-treemain.panel.example"
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	proxy.ServeHTTP(rec, post)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login %q = %d, want 303 (body=%s)", username, rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login did not set a session cookie")
	}
	return cookies
}

// TestUIProxy_AllowlistDeniesForeignWorkspace pins the enforcement half: an
// account allowed only "other-workspace" gets the dark 403 page on a browser
// navigation and a bare 403 on an API fetch; the origin is never contacted.
func TestUIProxy_AllowlistDeniesForeignWorkspace(t *testing.T) {
	proxy, _, _ := allowlistProxy(t, []string{"other-workspace"}, false)
	cookies := loginAlice(t, proxy, "alice", "pw-alice")

	htmlRec := subGet(t, proxy, "/", "text/html,application/xhtml+xml", cookies)
	if htmlRec.Code != http.StatusForbidden {
		t.Fatalf("restricted HTML GET = %d, want 403 (body=%s)", htmlRec.Code, htmlRec.Body.String())
	}
	if !strings.Contains(htmlRec.Body.String(), "Workspace not allowed") ||
		!strings.Contains(htmlRec.Body.String(), "abc123-treemain") {
		t.Errorf("403 page should name the denied workspace (body=%s)", htmlRec.Body.String())
	}
	if strings.Contains(htmlRec.Body.String(), "path=") {
		t.Errorf("restricted HTML GET must not reach the origin (body=%s)", htmlRec.Body.String())
	}

	apiRec := subGet(t, proxy, "/api/config", "application/json", cookies)
	if apiRec.Code != http.StatusForbidden {
		t.Fatalf("restricted non-HTML GET = %d, want 403", apiRec.Code)
	}
	if !strings.Contains(apiRec.Body.String(), "workspace not allowed") {
		t.Errorf("403 body = %q, want the workspace-not-allowed error", apiRec.Body.String())
	}
}

// TestUIProxy_AllowlistAllowsMatchingRoute pins the other half: a session whose
// account may open "treemain" IS proxied — navigation and API fetch alike —
// and an unrestricted account (empty allowlist at login) passes too.
func TestUIProxy_AllowlistAllowsMatchingRoute(t *testing.T) {
	for _, tc := range []struct {
		name      string
		allowlist []string
	}{
		{"route label allowlist", []string{"treemain"}},
		{"empty allowlist is unrestricted", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxy, _, _ := allowlistProxy(t, tc.allowlist, false)
			cookies := loginAlice(t, proxy, "alice", "pw-alice")
			rec := subGet(t, proxy, "/", "text/html", cookies)
			if rec.Code != http.StatusOK || rec.Body.String() != "path=/" {
				t.Fatalf("allowed tunneled GET = %d %q, want the origin answer", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestUIProxy_AllowlistMatchesWorkspaceNameOrPath pins the indirect matches:
// the allowlist may name the workspace by its registry name or a path suffix,
// not only by route label.
func TestUIProxy_AllowlistMatchesWorkspaceNameOrPath(t *testing.T) {
	for _, tc := range []struct {
		name      string
		allowlist []string
		wantOK    bool
	}{
		{"by workspace name", []string{"main"}, true},
		{"by workspace path", []string{"/srv/repo-main"}, true},
		{"by workspace path suffix", []string{"repo-main"}, true},
		{"segment suffix must not over-match", []string{"in"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxy, _, _ := allowlistProxy(t, tc.allowlist, false)
			cookies := loginAlice(t, proxy, "alice", "pw-alice")
			rec := subGet(t, proxy, "/", "text/html", cookies)
			if tc.wantOK {
				if rec.Code != http.StatusOK || rec.Body.String() != "path=/" {
					t.Fatalf("allowlist %v: GET = %d %q, want the origin answer",
						tc.allowlist, rec.Code, rec.Body.String())
				}
				return
			}
			if rec.Code != http.StatusForbidden {
				t.Fatalf("allowlist %v: GET = %d, want 403", tc.allowlist, rec.Code)
			}
		})
	}
}

// TestUIProxy_AllowlistLegacyBareTunnel pins enforcement on the legacy bare
// tunnel, where the label IS the worker id: the allowlist may name the worker
// id itself (or one of its workspace names/paths).
func TestUIProxy_AllowlistLegacyBareTunnel(t *testing.T) {
	for _, tc := range []struct {
		name      string
		allowlist []string
		wantOK    bool
	}{
		{"worker id allowed", []string{"abc123"}, true},
		{"workspace name allowed", []string{"evolved"}, true},
		{"unrelated id denied", []string{"other-laptop"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxy, _, _ := allowlistProxy(t, tc.allowlist, true)
			cookies := loginAlice(t, proxy, "alice", "pw-alice")

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "http://abc123.panel.example/", nil)
			req.Host = "abc123.panel.example"
			req.Header.Set("Accept", "text/html")
			for _, c := range cookies {
				req.AddCookie(c)
			}
			proxy.ServeHTTP(rec, req)
			if tc.wantOK {
				if rec.Code != http.StatusOK || rec.Body.String() != "path=/" {
					t.Fatalf("allowlist %v: bare GET = %d %q, want the origin answer",
						tc.allowlist, rec.Code, rec.Body.String())
				}
				return
			}
			if rec.Code != http.StatusForbidden {
				t.Fatalf("allowlist %v: bare GET = %d, want 403", tc.allowlist, rec.Code)
			}
		})
	}
}

// TestUIProxy_AllowlistChangeInvalidatesSession pins the end-to-end immediacy
// of the cookie fingerprint: after an admin rewrites alice's allowlist via the
// store, her already-issued cookie no longer reaches the origin — validate
// fails on the pin mismatch, so the request is answered by the gate, not the
// workspace check.
func TestUIProxy_AllowlistChangeInvalidatesSession(t *testing.T) {
	proxy, _, store := allowlistProxy(t, []string{"treemain"}, false)
	cookies := loginAlice(t, proxy, "alice", "pw-alice")

	okRec := subGet(t, proxy, "/", "text/html", cookies)
	if okRec.Code != http.StatusOK {
		t.Fatalf("precondition: allowed GET = %d (body=%s)", okRec.Code, okRec.Body.String())
	}

	rec, ok, err := store.GetUser("alice")
	if err != nil || !ok {
		t.Fatalf("lookup alice: err=%v ok=%v", err, ok)
	}
	rec.Workspaces = []string{"nowhere-else"}
	if err := store.PutUser("alice", rec); err != nil {
		t.Fatalf("rewrite allowlist: %v", err)
	}

	afterRec := subGet(t, proxy, "/", "text/html", cookies)
	if afterRec.Code != http.StatusOK || strings.Contains(afterRec.Body.String(), "path=") {
		t.Fatalf("post-change GET = %d %q, want the login page (session invalidated)",
			afterRec.Code, afterRec.Body.String())
	}
}

// TestUIProxy_AllowlistFailsClosedOnStoreError pins the fail-closed seam: a
// gate whose store errors on the allowlist read denies the request even though
// the session itself authenticated.
func TestUIProxy_AllowlistFailsClosedOnStoreError(t *testing.T) {
	store, err := registry.Open(t.TempDir() + "/cp.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := store.PutUser("alice", registry.UserRecord{Hash: string(hash), CreatedAt: time.Now()}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	gate, err := auth.NewGate(&erringWorkspacesStore{Store: store}, "", time.Hour, "", false)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	srv := New(Options{
		Registry: registry.NewWithStore(store, slog.New(slog.NewTextHandler(io.Discard, nil))),
		Auth:     pairing.New(testRouteSecret, time.Minute),
		Bus:      bus.New(256),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Gate:     gate,
	})
	now2 := time.Now()
	pairer2 := pairing.New(testRouteSecret, time.Minute)
	tok2, err := pairer2.Mint(now2)
	if err != nil {
		t.Fatalf("mint worker token: %v", err)
	}
	srv.reg.Add("abc123", "laptop", nil, nil, tok2, now2)

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "path="+r.URL.Path)
	}))
	t.Cleanup(origin.Close)
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })
	t.Cleanup(func() { _ = serverConn.Close() })
	clientSess, err := yamux.Client(clientConn, nil)
	if err != nil {
		t.Fatalf("yamux: %v", err)
	}
	go serveOriginOverYamux(serverConn, origin.Listener.Addr().String())
	srv.tunnels.set("abc123", "treemain", clientSess)
	proxy := srv.UIProxyHandler(http.NewServeMux())

	cookies := loginAlice(t, proxy, "alice", "pw")
	rec := subGet(t, proxy, "/", "text/html", cookies)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "path=") {
		t.Fatalf("store-error GET = %d %q, want a denial, never the origin", rec.Code, rec.Body.String())
	}
}

// erringWorkspacesStore wraps a real store but fails GetUserWorkspaces AFTER
// the first call, pinning that the gate fails closed on a mid-session store
// error rather than treating a read error as "unrestricted": the login itself
// must succeed (first read OK), then every later read — validate's
// fingerprint recompute, WorkspacesForUser — errors and denies.
type erringWorkspacesStore struct {
	*registry.Store
	calls int
}

func (e *erringWorkspacesStore) GetUserWorkspaces(username string) ([]string, error) {
	e.calls++
	if e.calls > 1 {
		return nil, io.ErrUnexpectedEOF
	}
	return e.Store.GetUserWorkspaces(username)
}
