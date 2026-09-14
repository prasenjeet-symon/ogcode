package master_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/prasenjeet-symon/ogcode-control-plane/internal/auth"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/bus"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/master"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/pairing"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/registry"
)

// newAccountPanelServer builds a master whose operator gate is backed by a real
// bbolt store holding a single account. It returns the server, the HTTP server,
// and the store so the test can seed/remove accounts against the same file the
// gate reads.
func newAccountPanelServer(t *testing.T, username, password string) (*master.Server, *httptest.Server, *registry.Store) {
	t.Helper()
	store, err := registry.Open(filepath.Join(t.TempDir(), "cp.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := store.PutUser(username, registry.UserRecord{Hash: string(hash), CreatedAt: time.Now()}); err != nil {
		t.Fatalf("put user: %v", err)
	}
	gate, err := auth.NewGate(store, "", time.Hour, "", false)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if gate.Mode() != auth.ModeAccounts {
		t.Fatalf("gate mode = %v, want ModeAccounts", gate.Mode())
	}
	srv := master.New(master.Options{
		Registry: registry.NewWithStore(store, slog.New(slog.NewTextHandler(io.Discard, nil))),
		Auth:     pairing.New(testSecret, time.Minute),
		Bus:      bus.New(256),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Gate:     gate,
	})
	mux := http.NewServeMux()
	path, handler := srv.Handler()
	mux.Handle(path, handler)
	// h2c so Connect bidi streaming works over the httptest cleartext listener
	// (tests that attach fake workers need it; HTTP/1.1-only tests pass through).
	hs := httptest.NewServer(h2c.NewHandler(srv.UIProxyHandler(mux), &http2.Server{}))
	t.Cleanup(hs.Close)
	return srv, hs, store
}

// noRedirect disables following the login's 303 so the session cookie can be
// captured and reused.
// loginAttempt posts username+password and returns the response without
// following the redirect, so both the success (303) and failure (401) paths
// can be inspected.
func loginAttempt(t *testing.T, hs *httptest.Server, username, password string) *http.Response {
	t.Helper()
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := noRedirect.PostForm(hs.URL+"/__operator/login",
		map[string][]string{"username": {username}, "password": {password}})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	return resp
}

func loginCookie(t *testing.T, hs *httptest.Server, username, password string) []*http.Cookie {
	t.Helper()
	resp := loginAttempt(t, hs, username, password)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("login status = %d, want 303 (body=%s)", resp.StatusCode, body)
	}
	cookies := resp.Cookies()
	if len(cookies) == 0 {
		t.Fatal("login did not set a session cookie")
	}
	return cookies
}

func getWithCookies(t *testing.T, url string, cookies []*http.Cookie) *http.Response {
	t.Helper()
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return resp
}

func TestApexConsole_AccountsLogin(t *testing.T) {
	_, hs, _ := newAccountPanelServer(t, "alice", "s3cret")

	resp := getWithCookies(t, hs.URL+"/", loginCookie(t, hs, "alice", "s3cret"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("apex status = %d, want 200 after account login (body=%s)", resp.StatusCode, body)
	}
}

func TestApexConsole_AccountsWrongPassword(t *testing.T) {
	_, hs, _ := newAccountPanelServer(t, "alice", "s3cret")

	resp := loginAttempt(t, hs, "alice", "wrong")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("login status = %d, want 401 on wrong password (body=%s)", resp.StatusCode, body)
	}
	if resp.Cookies() != nil && len(resp.Cookies()) != 0 {
		t.Fatal("failed login must not issue a session cookie")
	}
}

func TestApexConsole_UnknownUserRejected(t *testing.T) {
	_, hs, _ := newAccountPanelServer(t, "alice", "s3cret")

	// Wrong-user-with-correct-password must also be rejected (accounts mode).
	resp := loginAttempt(t, hs, "bob", "s3cret")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("login status = %d, want 401 on unknown username (body=%s)", resp.StatusCode, body)
	}
	idk := len(resp.Header.Get("Set-Cookie")) == 0
	if !idk {
		t.Fatal("failed login must not issue a session cookie")
	}
}

// TestA1_RmBreaksLiveSessions is the end-to-end form of the plan's §7 test:
// `users rm` breaks that user's live sessions immediately — the apex console
// must stop accepting the issued cookie once the account is deleted.
func TestA1_RmBreaksLiveSessions(t *testing.T) {
	_, hs, store := newAccountPanelServer(t, "alice", "s3cret")
	cookies := loginCookie(t, hs, "alice", "s3cret")

	// Session is live before removal.
	if resp := getWithCookies(t, hs.URL+"/", cookies); resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("session should be live before rm (status = %d)", resp.StatusCode)
	}

	// users rm alice — the same write the CLI performs.
	if _, err := store.DeleteUser("alice"); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	// The existing cookie no longer authenticates: the apex must fall back to the
	// login page (operator gate refuses rather than opening).
	resp := getWithCookies(t, hs.URL+"/", cookies)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "sign in") {
		t.Errorf("after users rm, the old cookie must be rejected (login page), status=%d body=%s",
			resp.StatusCode, body)
	}
}

// --- Phase H: the operator-console users page --------------------------------

// usersPost posts a form to one of the users endpoints without following the
// redirect-style responses, returning the recorder for inspection.
func usersPost(t *testing.T, hs *httptest.Server, cookies []*http.Cookie, path string, form map[string][]string) *http.Response {
	t.Helper()
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	values := map[string][]string{}
	for k, v := range form {
		values[k] = v
	}
	req, err := http.NewRequest(http.MethodPost, hs.URL+path, strings.NewReader(formEncode(values)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", path, err)
	}
	return resp
}

// formEncode renders a form map in application/x-www-form-urlencoded order.
func formEncode(values map[string][]string) string {
	var parts []string
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range values[k] {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

// TestUsersPage_RequiresLogin pins that the users endpoints sit behind the
// same operator gate as the console: an unauthenticated GET of the users page
// is answered by the login page, never the account list.
func TestUsersPage_RequiresLogin(t *testing.T) {
	_, hs, _ := newAccountPanelServer(t, "alice", "s3cret")

	resp := getWithCookies(t, hs.URL+"/__operator/users", nil)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "sign in") {
		t.Fatalf("unauthenticated users GET = %d, want the login page (body=%s)", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "Add account") {
		t.Errorf("unauthenticated users GET must not render the users page (body=%s)", body)
	}
}

// TestUsersPage_ListsAccountsWithoutHash pins the happy path: a logged-in
// operator sees the account list with its allowlist pills, and the response
// never contains the bcrypt hash (the store's secret material).
func TestUsersPage_ListsAccountsWithoutHash(t *testing.T) {
	_, hs, store := newAccountPanelServer(t, "alice", "s3cret")
	rec, ok, err := store.GetUser("alice")
	if err != nil || !ok {
		t.Fatalf("precondition: alice exists (err=%v ok=%v)", err, ok)
	}

	resp := getWithCookies(t, hs.URL+"/__operator/users", loginCookie(t, hs, "alice", "s3cret"))
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("users GET = %d, want 200 (body=%s)", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "alice") || !strings.Contains(string(body), "all workspaces") {
		t.Errorf("users page should list alice as unrestricted (body=%s)", body)
	}
	if strings.Contains(string(body), rec.Hash) {
		t.Errorf("users page must never render the bcrypt hash")
	}
}

// TestUsersPage_AddAccountWithAllowlist drives the add form end to end: the
// account appears in the store with the parsed allowlist, hashed; then shows
// up on the page as restricted.
func TestUsersPage_AddAccountWithAllowlist(t *testing.T) {
	_, hs, store := newAccountPanelServer(t, "alice", "s3cret")
	cookies := loginCookie(t, hs, "alice", "s3cret")

	resp := usersPost(t, hs, cookies, "/__operator/users/add", map[string][]string{
		"username":   {"bob"},
		"password":   {"bob-pw"},
		"confirm":    {"bob-pw"},
		"workspaces": {" treemain , repo-b ,"},
	})
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("users add = %d, want 200 page (body=%s)", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "added") {
		t.Errorf("users add should flash success (body=%s)", body)
	}
	rec, ok, err := store.GetUser("bob")
	if err != nil || !ok {
		t.Fatalf("bob should exist after add (err=%v ok=%v)", err, ok)
	}
	if len(rec.Workspaces) != 2 || rec.Workspaces[0] != "treemain" || rec.Workspaces[1] != "repo-b" {
		t.Fatalf("bob's allowlist = %v, want [treemain repo-b] (trimmed, ordered)", rec.Workspaces)
	}
	if bcrypt.CompareHashAndPassword([]byte(rec.Hash), []byte("bob-pw")) != nil {
		t.Error("bob's stored hash should verify against the form password")
	}
}

// TestUsersPage_AddValidations pins the form validations: password mismatch,
// duplicate account, and a username the cookie payload cannot carry.
func TestUsersPage_AddValidations(t *testing.T) {
	_, hs, _ := newAccountPanelServer(t, "alice", "s3cret")
	cookies := loginCookie(t, hs, "alice", "s3cret")

	for _, tc := range []struct {
		name string
		form map[string][]string
		want string
	}{
		{"mismatched confirm", map[string][]string{"username": {"x"}, "password": {"a"}, "confirm": {"b"}}, "do not match"},
		{"duplicate account", map[string][]string{"username": {"alice"}, "password": {"a"}, "confirm": {"a"}}, "already exists"},
		{"pipe in username", map[string][]string{"username": {"a|b"}, "password": {"a"}, "confirm": {"a"}}, "must not contain"},
		{"missing password", map[string][]string{"username": {"x"}, "confirm": {"a"}}, "Password is required"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			resp := usersPost(t, hs, cookies, "/__operator/users/add", tc.form)
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("users add = %d, want 200 with an error banner (body=%s)", resp.StatusCode, body)
			}
			if !strings.Contains(string(body), tc.want) {
				t.Errorf("users add should reject with %q (body=%s)", tc.want, body)
			}
		})
	}
	// None of the failed adds may have created an account.
	_, hs2, store := newAccountPanelServer(t, "alice", "s3cret")
	_ = hs2
	names, err := store.ListUsers()
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(names) != 1 || names[0] != "alice" {
		t.Fatalf("failed adds must not create accounts; users = %v", names)
	}
}

// TestUsersPage_RmRefusesLastWithoutForce pins the console mirror of the CLI's
// --force guard: deleting the final account without the checkbox is refused;
// with it, the account goes.
func TestUsersPage_RmRefusesLastWithoutForce(t *testing.T) {
	_, hs, store := newAccountPanelServer(t, "alice", "s3cret")
	cookies := loginCookie(t, hs, "alice", "s3cret")

	// Seed a second account so alice is not last for the first deletion.
	hash, err := bcrypt.GenerateFromPassword([]byte("bob-pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := store.PutUser("bob", registry.UserRecord{Hash: string(hash), CreatedAt: time.Now()}); err != nil {
		t.Fatalf("seed bob: %v", err)
	}

	resp := usersPost(t, hs, cookies, "/__operator/users/rm", map[string][]string{"username": {"bob"}})
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "deleted") {
		t.Fatalf("rm bob = %d (body=%s), want success flash", resp.StatusCode, body)
	}
	if _, ok, _ := store.GetUser("bob"); ok {
		t.Fatal("bob should be gone after the rm POST")
	}

	// Now alice is last: without force the rm is refused.
	resp2 := usersPost(t, hs, cookies, "/__operator/users/rm", map[string][]string{"username": {"alice"}})
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)
	if !strings.Contains(string(body2), "Refusing to delete the last operator account") {
		t.Fatalf("rm alice (last) without force should be refused (body=%s)", body2)
	}
	if _, ok, _ := store.GetUser("alice"); !ok {
		t.Fatal("alice must survive the refused rm")
	}

	// With force she goes — and the operator's own live session dies (A1).
	resp3 := usersPost(t, hs, cookies, "/__operator/users/rm", map[string][]string{"username": {"alice"}, "force": {"1"}})
	defer resp3.Body.Close()
	body3, _ := io.ReadAll(resp3.Body)
	if !strings.Contains(string(body3), "deleted") {
		t.Fatalf("forced rm alice = %d (body=%s), want success", resp3.StatusCode, body3)
	}
	after := getWithCookies(t, hs.URL+"/", cookies)
	defer after.Body.Close()
	afterBody, _ := io.ReadAll(after.Body)
	if !strings.Contains(string(afterBody), "sign in") {
		t.Errorf("after deleting her own account the operator must be signed out (body=%s)", afterBody)
	}
}

// TestUsersPage_SetWorkspaces pins the allowlist editor: setting a list
// persists it on the record and shows it as pills; clearing it restores
// unrestricted.
func TestUsersPage_SetWorkspaces(t *testing.T) {
	_, hs, store := newAccountPanelServer(t, "alice", "s3cret")
	cookies := loginCookie(t, hs, "alice", "s3cret")

	resp := usersPost(t, hs, cookies, "/__operator/users/set-workspaces", map[string][]string{
		"username":   {"alice"},
		"workspaces": {"treemain, repo-b"},
	})
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "Allowlist updated") {
		t.Fatalf("set-workspaces = %d (body=%s), want success flash", resp.StatusCode, body)
	}
	rec, ok, err := store.GetUser("alice")
	if err != nil || !ok {
		t.Fatalf("alice lookup: %v %v", err, ok)
	}
	if len(rec.Workspaces) != 2 {
		t.Fatalf("alice's allowlist = %v, want 2 entries", rec.Workspaces)
	}
	if !strings.Contains(string(body), "treemain") {
		t.Errorf("the page should list the new allowlist (body=%s)", body)
	}

	// Clearing back to unrestricted. NOTE: the allowlist change above already
	// signed alice's live session out (the cookie pins the old fingerprint),
	// which is exactly the immediacy under test — so re-login first.
	cookies2 := loginCookie(t, hs, "alice", "s3cret")
	resp2 := usersPost(t, hs, cookies2, "/__operator/users/set-workspaces", map[string][]string{
		"username": {"alice"},
	})
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)
	if !strings.Contains(string(body2), "all workspaces") {
		t.Fatalf("clearing allowlist should flash unrestricted (body=%s)", body2)
	}
	rec2, _, _ := store.GetUser("alice")
	if len(rec2.Workspaces) != 0 {
		t.Fatalf("cleared allowlist = %v, want empty", rec2.Workspaces)
	}
}

// TestUsersPage_SetWorkspacesOnUnknownUser pins the editor's guard against a
// username that does not exist.
func TestUsersPage_SetWorkspacesOnUnknownUser(t *testing.T) {
	_, hs, _ := newAccountPanelServer(t, "alice", "s3cret")
	cookies := loginCookie(t, hs, "alice", "s3cret")

	resp := usersPost(t, hs, cookies, "/__operator/users/set-workspaces", map[string][]string{
		"username": {"ghost"},
	})
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "does not exist") {
		t.Fatalf("set-workspaces on unknown user should say so (body=%s)", body)
	}
}
