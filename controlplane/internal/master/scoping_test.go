package master_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

// --- UserRecord persistence -------------------------------------------------

// Admin is a *bool so pre-admin-era records (field absent) stay admins while
// explicitly created users pin false; Repo records the assignment. Both must
// round-trip through the store's JSON encoding.
func TestUserRecord_AdminAndRepoRoundTrip(t *testing.T) {
	st, err := registry.Open(filepath.Join(t.TempDir(), "cp.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	admin := false
	records := map[string]registry.UserRecord{
		"legacy":   {Hash: "h1", CreatedAt: time.Now()}, // no admin field at all
		"plain":    {Hash: "h2", CreatedAt: time.Now(), Admin: &admin},
		"assigned": {Hash: "h3", CreatedAt: time.Now(), Admin: &admin, Repo: "https://github.com/o/r", Workspaces: []string{"alice"}},
	}
	for name, rec := range records {
		if err := st.PutUser(name, rec); err != nil {
			t.Fatalf("put %s: %v", name, err)
		}
	}
	for name, want := range records {
		got, ok, err := st.GetUser(name)
		if err != nil || !ok {
			t.Fatalf("get %s: ok=%v err=%v", name, ok, err)
		}
		if got.IsAdmin() != want.IsAdmin() {
			t.Errorf("%s: IsAdmin = %v, want %v", name, got.IsAdmin(), want.IsAdmin())
		}
		if got.Repo != want.Repo {
			t.Errorf("%s: Repo = %q, want %q", name, got.Repo, want.Repo)
		}
	}
	// The legacy record (admin field absent) must read as an admin.
	if legacy, ok, _ := st.GetUser("legacy"); !ok || !legacy.IsAdmin() {
		t.Errorf("legacy record: ok=%v admin=%v, want ok with admin=true", ok, legacy.IsAdmin())
	}
}

// --- Harness ----------------------------------------------------------------

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// h2cShim wraps h2c.NewHandler (aliased for readability at the call site).
func h2cShim(base http.Handler) http.Handler {
	return h2c.NewHandler(base, &http2.Server{})
}

// scopedHarness builds an accounts-mode master with one admin ("root") and two
// plain users: "alice" (repo-assigned) and "bob" (repo-assigned, different
// repo). It returns the pieces plus both logins' cookies.
func scopedHarness(t *testing.T) (*master.Server, *httptest.Server, *registry.Store, []*http.Cookie, []*http.Cookie, []*http.Cookie) {
	t.Helper()
	store, err := registry.Open(filepath.Join(t.TempDir(), "cp.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	mk := func(name, pw string, admin *bool, repo string) {
		t.Helper()
		hash, err := bcrypt.GenerateFromPassword([]byte("pw-"+name), bcrypt.MinCost)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		rec := registry.UserRecord{Hash: string(hash), CreatedAt: time.Now(), Admin: admin}
		if repo != "" {
			rec.Repo = repo
			rec.Workspaces = []string{master.UserWorktreeSlug(name)}
		}
		if err := store.PutUser(name, rec); err != nil {
			t.Fatalf("put %s: %v", name, err)
		}
	}
	mk("root", "", nil, "")
	mk("alice", "", boolPtr(false), "https://github.com/o/repo")
	mk("bob", "", boolPtr(false), "https://github.com/o/other")

	gate, err := auth.NewGate(store, "", time.Hour, "", false)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	srv := master.New(master.Options{
		Registry: registry.NewWithStore(store, discardLogger()),
		Auth:     pairing.New(testSecret, time.Minute),
		Bus:      bus.New(256),
		Logger:   discardLogger(),
		Gate:     gate,
	})
	mux := http.NewServeMux()
	path, handler := srv.Handler()
	mux.Handle(path, handler)
	hs := httptest.NewServer(h2cShim(srv.UIProxyHandler(mux)))
	t.Cleanup(hs.Close)

	adminCookie := loginCookie(t, hs, "root", "pw-root")
	aliceCookie := loginCookie(t, hs, "alice", "pw-alice")
	bobCookie := loginCookie(t, hs, "bob", "pw-bob")
	return srv, hs, store, adminCookie, aliceCookie, bobCookie
}

// --- Session scoping --------------------------------------------------------

// A session started under a scoped user's account is visible to that user and
// to admins; a foreign scoped user gets the not-found page (the monitor must
// not reveal another account's session id or existence).
func TestSessionView_ScopedUserSeesOnlyOwnSessions(t *testing.T) {
	srv, hs, _, adminCookie, aliceCookie, bobCookie := scopedHarness(t)

	// Two sessions: alice's (routed to a worker) and one with no routing at
	// all (a foreign session must 404 on its routing absence even for admins).
	srv.RecordSessionUser("ses_alice1", "alice")
	srv.Registry().RouteSession("ses_alice1", "w1")
	srv.RecordSessionUser("ses_bob1", "bob")
	srv.Registry().RouteSession("ses_bob1", "w1")

	// Admin sees both.
	for _, sid := range []string{"ses_alice1", "ses_bob1"} {
		resp := getWithCookies(t, hs.URL+"/sessions/"+sid, adminCookie)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("admin view %s status = %d, want 200", sid, resp.StatusCode)
		}
	}

	// Alice sees her own, not bob's.
	if resp := getWithCookies(t, hs.URL+"/sessions/ses_alice1", aliceCookie); resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("owner view status = %d, want 200", resp.StatusCode)
	}
	resp := getWithCookies(t, hs.URL+"/sessions/ses_bob1", aliceCookie)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign user view status = %d, want 404", resp.StatusCode)
	}

	// Bob symmetrically: his own renders, alice's is 404.
	if resp := getWithCookies(t, hs.URL+"/sessions/ses_bob1", bobCookie); resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("owner view status = %d, want 200", resp.StatusCode)
	}
	resp2 := getWithCookies(t, hs.URL+"/sessions/ses_alice1", bobCookie)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign user view status = %d, want 404", resp2.StatusCode)
	}
}

// The session monitor's SSE endpoint is scoped with the same rule.
func TestSessionEvents_ScopedUserDeniedForeignSession(t *testing.T) {
	srv, hs, _, _, aliceCookie, _ := scopedHarness(t)

	srv.RecordSessionUser("ses_bob1", "bob")
	srv.Registry().RouteSession("ses_bob1", "w1")

	resp := getWithCookies(t, hs.URL+"/sessions/ses_bob1/events", aliceCookie)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign user SSE status = %d, want 404", resp.StatusCode)
	}
}

// --- Console scoping --------------------------------------------------------

// The apex console lists only the workers a scoped account's allowlist names;
// admins (and non-accounts modes) see everything.
func TestApexConsole_ScopedUserSeesOnlyOwnWorker(t *testing.T) {
	srv, hs, _, adminCookie, aliceCookie, _ := scopedHarness(t)

	// Two workers: "w-repo" advertises a workspace matching alice's pinned
	// allowlist identifier ("alice" path segment), "w-other" does not.
	srv.Registry().Add("w-repo", "repo worker", nil, []registry.Workspace{
		{Path: "/srv/repos/repo/.ogcode/worktrees/user/alice", Name: "alice", Present: true},
	}, pairing.Token{}, time.Now())
	srv.Registry().Add("w-other", "other worker", nil, []registry.Workspace{
		{Path: "/srv/other", Name: "other", Present: true},
	}, pairing.Token{}, time.Now())

	page := func(cookies []*http.Cookie) string {
		resp := getWithCookies(t, hs.URL+"/", cookies)
		defer resp.Body.Close()
		body := new(strings.Builder)
		_, _ = io.Copy(body, resp.Body)
		return body.String()
	}

	adminPage := page(adminCookie)
	if !strings.Contains(adminPage, "repo worker") || !strings.Contains(adminPage, "other worker") {
		t.Error("admin console must list every worker")
	}

	alicePage := page(aliceCookie)
	if !strings.Contains(alicePage, "repo worker") {
		t.Error("alice's console must list her assigned repo's worker")
	}
	if strings.Contains(alicePage, "other worker") {
		t.Error("alice's console must not list an unrelated worker")
	}
}

// A scoped account with an allowlist matching nothing on a worker sees no
// workers at all rather than every worker.
func TestApexConsole_ScopedUserWithNoMatchSeesNone(t *testing.T) {
	srv, hs, _, _, aliceCookie, _ := scopedHarness(t)
	srv.Registry().Add("w-other", "other worker", nil, []registry.Workspace{
		{Path: "/srv/other", Name: "other", Present: true},
	}, pairing.Token{}, time.Now())

	resp := getWithCookies(t, hs.URL+"/", aliceCookie)
	defer resp.Body.Close()
	body := new(strings.Builder)
	_, _ = io.Copy(body, resp.Body)
	if strings.Contains(body.String(), "other worker") {
		t.Error("scoped account must not see a worker its allowlist does not name")
	}
}
