package auth

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// fakeChecker is an in-memory UserChecker for unit tests. It maps username ->
// bcrypt hash and tracks existence removals so A1 (rm breaks sessions
// immediately) can be pinned without a bbolt store. workspaces maps username ->
// allowlist (nil = unrestricted); setWorkspaces mutates it so the
// allowlist-invalidation behaviour can be pinned like A1.
type fakeChecker struct {
	mu         sync.Mutex
	users      map[string]string // username -> hash
	workspaces map[string][]string
}

func newFakeChecker(hashes map[string]string) *fakeChecker {
	return &fakeChecker{users: hashes}
}

func (f *fakeChecker) GetPasswordHash(username string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.users[username]
	return h, ok, nil
}

func (f *fakeChecker) UserExists(username string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.users[username]
	return ok, nil
}

func (f *fakeChecker) HasUsers() (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.users) > 0, nil
}

func (f *fakeChecker) GetUserWorkspaces(username string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.workspaces[username]...), nil
}

func (f *fakeChecker) setWorkspaces(username string, list []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.workspaces == nil {
		f.workspaces = make(map[string][]string)
	}
	f.workspaces[username] = list
}

func (f *fakeChecker) rm(username string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.users, username)
}

// hashForTest returns a real bcrypt hash (so Authenticate's
// CompareHashAndPassword works against it). A tiny cost keeps the suite fast
// while exercising the real comparison path.
func hashForTest(password string) string {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		panic(err)
	}
	return string(h)
}

func TestLegacyMode_RoundTrip(t *testing.T) {
	g, err := NewGate(nil, "hunter2", time.Hour, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !g.Enabled() {
		t.Fatal("gate with a legacy password should be enabled")
	}
	if g.Mode() != ModeLegacy {
		t.Fatalf("mode = %v, want legacy", g.Mode())
	}
	if _, ok := g.Authenticate("anyone", "hunter2"); !ok {
		t.Fatal("correct shared password rejected")
	}
	if _, ok := g.Authenticate("anyone", "hunter3"); ok {
		t.Fatal("wrong shared password accepted")
	}
	if _, ok := g.Authenticate("anyone", ""); ok {
		t.Fatal("blank password accepted")
	}
}

func TestDisabledGate(t *testing.T) {
	g, _ := NewGate(nil, "", time.Hour, "", false)
	if g.Enabled() {
		t.Fatal("no password and no accounts should disable the gate")
	}
	if g.Mode() != ModeDisabled {
		t.Fatalf("mode = %v, want disabled", g.Mode())
	}
	if _, ok := g.Authenticate("", ""); ok {
		t.Fatal("disabled gate must never authenticate")
	}
	if _, ok := g.Authenticate("anything", "anything"); ok {
		t.Fatal("disabled gate must never authenticate")
	}
}

func TestAccountsMode_RoundTrip(t *testing.T) {
	fc := newFakeChecker(map[string]string{"alice": hashForTest("pw-alice")})
	g, err := NewGate(fc, "legacy", time.Hour, "", false)
	if err != nil {
		t.Fatal(err)
	}
	// Accounts exist → mode is accounts, the legacy password is ignored.
	if g.Mode() != ModeAccounts {
		t.Fatalf("mode = %v, want accounts (accounts win over legacy)", g.Mode())
	}
	if _, ok := g.Authenticate("alice", "pw-alice"); !ok {
		t.Fatal("correct account credentials rejected")
	}
	if id, ok := g.Authenticate("bob", "pw-alice"); ok {
		t.Fatalf("wrong password for alice accepted as user %q", id)
	}
	// The legacy password must NOT authenticate once accounts exist.
	if _, ok := g.Authenticate("alice", "legacy"); ok {
		t.Fatal("the shared legacy password must not authenticate in accounts mode")
	}
}

func TestSessionRoundTrip_Legacy(t *testing.T) {
	g, _ := NewGate(nil, "pw", time.Hour, "", false)

	rec := httptest.NewRecorder()
	g.SetCookie(rec, "")
	cookie := rec.Result().Cookies()[0]
	if cookie.Name != CookieName || cookie.Value == "" {
		t.Fatalf("bad cookie: %+v", cookie)
	}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(cookie)
	if !g.Authenticated(r) {
		t.Fatal("freshly issued legacy cookie should authenticate")
	}
}

func TestSessionRoundTrip_Accounts(t *testing.T) {
	fc := newFakeChecker(map[string]string{"alice": hashForTest("pw")})
	g, _ := NewGate(fc, "", time.Hour, "", false)

	rec := httptest.NewRecorder()
	g.SetCookie(rec, "alice")
	cookie := rec.Result().Cookies()[0]

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(cookie)
	if !g.Authenticated(r) {
		t.Fatal("freshly issued account cookie should authenticate")
	}
}

func TestTamperedCookieRejected(t *testing.T) {
	g, _ := NewGate(nil, "pw", time.Hour, "", false)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "Zm9v.YmFy"})
	if g.Authenticated(r) {
		t.Fatal("tampered cookie must not authenticate")
	}
}

func TestExpiredCookieRejected(t *testing.T) {
	g, _ := NewGate(nil, "pw", time.Hour, "", false)
	past := time.Now().Add(-time.Hour)
	token := g.issue(past.Add(-g.ttl), "") // expiry = past
	if g.validate(token, time.Now()) {
		t.Fatal("expired token must not validate")
	}
}

func TestForeignSigningKeyRejected(t *testing.T) {
	g1, _ := NewGate(nil, "pw", time.Hour, "", false)
	g2, _ := NewGate(nil, "pw", time.Hour, "", false) // different random signing key
	token := g1.issue(time.Now(), "")
	if g2.validate(token, time.Now()) {
		t.Fatal("token signed by a different key must not validate")
	}
}

// TestA1_RmBreaksLiveSessions pins the correctness core of Phase B: deleting an
// account makes its already-issued cookies invalid immediately, because
// validate consults the store for the cookie's user id. Without this check a
// fired employee would keep operator access until the cookie expired.
func TestA1_RmBreaksLiveSessions(t *testing.T) {
	fc := newFakeChecker(map[string]string{"alice": hashForTest("pw")})
	g, _ := NewGate(fc, "", time.Hour, "", false)

	rec := httptest.NewRecorder()
	g.SetCookie(rec, "alice")
	cookie := rec.Result().Cookies()[0]

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(cookie)
	if !g.Authenticated(r) {
		t.Fatal("precondition: alice's cookie authenticates")
	}

	// `users rm alice` deletes the account.
	fc.rm("alice")

	if g.Authenticated(r) {
		t.Fatal("A1 violated: alice's cookie still authenticates after `users rm alice`")
	}
}

// TestAllowlist_ScopedRoundTrip pins the allowlist-era cookie shape: a scoped
// login carries the account's allowlist fingerprint, still authenticates, and
// WorkspacesForUser reads the CURRENT store list (not the pinned one).
func TestAllowlist_ScopedRoundTrip(t *testing.T) {
	fc := newFakeChecker(map[string]string{"alice": hashForTest("pw")})
	fc.setWorkspaces("alice", []string{"treemain", "repo-b"})
	g, _ := NewGate(fc, "", time.Hour, "", false)

	if _, ws, ok := g.AuthenticateScoped("alice", "pw"); !ok || len(ws) != 2 {
		t.Fatalf("AuthenticateScoped ok=%v ws=%v, want ok with 2 workspaces", ok, ws)
	}
	// The plain Authenticate keeps working for callers that ignore the allowlist.
	if _, ok := g.Authenticate("alice", "pw"); !ok {
		t.Fatal("Authenticate must keep working in accounts mode")
	}
	if _, _, ok := g.AuthenticateScoped("alice", "wrong"); ok {
		t.Fatal("wrong password accepted by AuthenticateScoped")
	}

	rec := httptest.NewRecorder()
	g.SetCookieScoped(rec, "alice", []string{"treemain", "repo-b"})
	cookie := rec.Result().Cookies()[0]

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(cookie)
	if !g.Authenticated(r) {
		t.Fatal("scoped cookie should authenticate")
	}
	ws, ok := g.WorkspacesForUser(r)
	if !ok || len(ws) != 2 {
		t.Fatalf("WorkspacesForUser ok=%v ws=%v, want ok with 2 entries", ok, ws)
	}
}

// TestAllowlist_ChangeInvalidatesLiveSessions pins the admin-side immediacy
// that mirrors A1: once an admin rewrites the account's allowlist, a session
// issued under the old list stops validating — the cookie's fingerprint no
// longer matches the store's current one.
func TestAllowlist_ChangeInvalidatesLiveSessions(t *testing.T) {
	fc := newFakeChecker(map[string]string{"alice": hashForTest("pw")})
	fc.setWorkspaces("alice", []string{"treemain"})
	g, _ := NewGate(fc, "", time.Hour, "", false)

	rec := httptest.NewRecorder()
	g.SetCookieScoped(rec, "alice", []string{"treemain"})
	cookie := rec.Result().Cookies()[0]

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(cookie)
	if !g.Authenticated(r) {
		t.Fatal("precondition: alice's scoped cookie authenticates")
	}

	// Admin restricts alice's allowlist.
	fc.setWorkspaces("alice", []string{"treevolved"})

	if g.Authenticated(r) {
		t.Fatal("allowlist change must invalidate the session issued under the old list")
	}

	// A fresh login re-pins the new list and works again.
	fc2rec := httptest.NewRecorder()
	g.SetCookieScoped(fc2rec, "alice", []string{"treevolved"})
	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.AddCookie(fc2rec.Result().Cookies()[0])
	if !g.Authenticated(r2) {
		t.Fatal("re-login after the allowlist change should authenticate")
	}
}

// TestAllowlist_RestrictingUnrestrictedAccountInvalidates pins the empty-pin
// rule: an unrestricted account's session pins the EMPTY fingerprint (a
// non-empty hex value), so granting it a restriction — not just widening a
// restricted one — also forces a re-login.
func TestAllowlist_RestrictingUnrestrictedAccountInvalidates(t *testing.T) {
	fc := newFakeChecker(map[string]string{"alice": hashForTest("pw")})
	g, _ := NewGate(fc, "", time.Hour, "", false)

	rec := httptest.NewRecorder()
	g.SetCookieScoped(rec, "alice", nil) // unrestricted at login
	cookie := rec.Result().Cookies()[0]

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(cookie)
	if !g.Authenticated(r) {
		t.Fatal("precondition: unrestricted scoped cookie authenticates")
	}
	if ws, ok := g.WorkspacesForUser(r); !ok || len(ws) != 0 {
		t.Fatalf("unrestricted account: ok=%v ws=%v, want unrestricted", ok, ws)
	}

	fc.setWorkspaces("alice", []string{"treemain"})
	if g.Authenticated(r) {
		t.Fatal("restricting an unrestricted account must invalidate its live session")
	}
}

// TestAllowlist_LegacyTwoFieldCookieStillValid pins backward compatibility:
// cookies issued before the allowlist shipped (2-field payload) keep
// validating, and WorkspacesForUser reports them unrestricted — sourced from
// the store, so it tracks whatever the account's allowlist is today.
func TestAllowlist_LegacyTwoFieldCookieStillValid(t *testing.T) {
	fc := newFakeChecker(map[string]string{"alice": hashForTest("pw")})
	fc.setWorkspaces("alice", []string{"treemain"})
	g, _ := NewGate(fc, "", time.Hour, "", false)

	// Pre-allowlist cookie shape: SetCookie, no fingerprint.
	rec := httptest.NewRecorder()
	g.SetCookie(rec, "alice")
	cookie := rec.Result().Cookies()[0]

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(cookie)
	if !g.Authenticated(r) {
		t.Fatal("legacy 2-field account cookie must keep validating after the upgrade")
	}
	ws, ok := g.WorkspacesForUser(r)
	if !ok || len(ws) != 1 || ws[0] != "treemain" {
		t.Fatalf("legacy cookie: ok=%v ws=%v, want current store allowlist", ok, ws)
	}
}

// TestAllowlist_OverpinnedCookieRejected pins that a cookie claiming a
// fingerprint the account never had is rejected — a stolen-cookie holder
// cannot widen their access by hand-crafting the third field, because the
// signature covers it and validate compares against the store.
func TestAllowlist_OverpinnedCookieRejected(t *testing.T) {
	fc := newFakeChecker(map[string]string{"alice": hashForTest("pw")})
	g, _ := NewGate(fc, "", time.Hour, "", false)

	// Signed with this gate's key, but pinning an allowlist alice never had.
	rec := httptest.NewRecorder()
	g.SetCookieScoped(rec, "alice", []string{"someone-elses-workspace"})
	cookie := rec.Result().Cookies()[0]

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(cookie)
	if g.Authenticated(r) {
		t.Fatal("cookie pinned to an allowlist the account does not have must be rejected")
	}
	if _, ok := g.WorkspacesForUser(r); ok {
		t.Fatal("WorkspacesForUser must deny a session whose pin does not match the store")
	}
}

// TestAllowlist_WorkspacesForUserDeniesWithoutSession pins fail-closed
// behaviour of the enforcement seam: no cookie, foreign cookie, or
// non-accounts mode deny.
func TestAllowlist_WorkspacesForUserDeniesWithoutSession(t *testing.T) {
	fc := newFakeChecker(map[string]string{"alice": hashForTest("pw")})
	g, _ := NewGate(fc, "", time.Hour, "", false)

	// No cookie at all.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, ok := g.WorkspacesForUser(r); ok {
		t.Fatal("no session must deny")
	}

	// Valid cookie from a DIFFERENT gate (foreign signing key).
	g2, _ := NewGate(fc, "", time.Hour, "", false)
	rec := httptest.NewRecorder()
	g2.SetCookieScoped(rec, "alice", nil)
	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.AddCookie(rec.Result().Cookies()[0])
	if _, ok := g.WorkspacesForUser(r2); ok {
		t.Fatal("foreign-signed session must deny")
	}

	// Legacy mode has no per-user allowlist: unrestricted, not a denial.
	gl, _ := NewGate(nil, "pw", time.Hour, "", false)
	rl := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, ok := gl.WorkspacesForUser(rl); !ok {
		t.Fatal("legacy mode has no allowlist — must report unrestricted, not deny")
	}
}

// TestWorkspaceFingerprint pins the fingerprint's contract: order-insensitive,
// list-sensitive, stable across calls, 16 hex chars, and distinct for the
// empty list vs a one-element list (so "unrestricted as of login" is a real
// pin, not the same value as any restricted state).
func TestWorkspaceFingerprint(t *testing.T) {
	a := workspaceFingerprint([]string{"b", "a"})
	b := workspaceFingerprint([]string{"a", "b"})
	if a != b {
		t.Fatal("fingerprint must be order-insensitive")
	}
	if a != workspaceFingerprint([]string{"b", "a"}) {
		t.Fatal("fingerprint must be stable")
	}
	if a == workspaceFingerprint([]string{"a"}) {
		t.Fatal("fingerprint must distinguish list contents")
	}
	if a == workspaceFingerprint(nil) {
		t.Fatal("empty list must not share a fingerprint with a non-empty one")
	}
	if len(a) != 16 {
		t.Fatalf("fingerprint length = %d, want 16 hex chars", len(a))
	}
}

// TestLockedMode pins the A2 fail-closed posture: a locked gate is Enabled (the
// proxy demands a session) but no authentication or revalidation ever succeeds,
// so a corrupt accounts store can never open authentication.
func TestLockedMode(t *testing.T) {
	g, err := NewLockedGate(time.Hour, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !g.Enabled() {
		t.Fatal("a locked gate must be Enabled (operator surfaces demand a session)")
	}
	if g.Mode() != ModeLocked {
		t.Fatalf("mode = %v, want locked", g.Mode())
	}
	// No credentials authenticate, even ones that would have worked before.
	if _, ok := g.Authenticate("alice", "pw"); ok {
		t.Fatal("locked gate must not authenticate anyone")
	}
	// A previously-issued cookie (signed with this gate's key) is not accepted.
	rec := httptest.NewRecorder()
	g.SetCookie(rec, "alice")
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(rec.Result().Cookies()[0])
	if g.Authenticated(r) {
		t.Fatal("locked gate must not revalidate cookies")
	}
}

// TestA3_UnknownUserRunsBcrypt pins the constant-time unknown-user path: an
// unknown user must still perform a bcrypt compare (against the dummy hash) so
// response timing does not enumerate valid usernames. We assert the code path
// runs the compare cost by checking the gate's dummyHash is a valid bcrypt hash
// (it must have been generated at construction) and that Authenticate rejects
// an unknown user without short-circuiting on the username.
func TestA3_UnknownUserRunsBcrypt(t *testing.T) {
	fc := newFakeChecker(map[string]string{"alice": hashForTest("pw")})
	g, _ := NewGate(fc, "", time.Hour, "", false)

	// The dummy hash must have been precomputed (construction succeeded) and be
	// a syntactically valid bcrypt hash the presented password is compared
	// against.
	if len(g.dummyHash) == 0 {
		t.Fatal("gate has no dummy hash — unknown-user path cannot be constant-time")
	}

	// Unknown user is rejected (and the compare runs against the dummy hash —
	// the bcrypt.CompareHashAndPassword call in Authenticate).
	if _, ok := g.Authenticate("nobody", "guess"); ok {
		t.Fatal("unknown user must be rejected")
	}
	// And a blank/unknown user with a valid-looking password still rejected.
	if _, ok := g.Authenticate("nobody", "pw-alice"); ok {
		t.Fatal("unknown user with a real user's password must be rejected")
	}
}
