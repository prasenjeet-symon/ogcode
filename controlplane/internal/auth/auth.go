// Package auth is the master's operator login: a bcrypt-backed per-employee
// gate plus an HMAC-signed session cookie. Stdlib for signing, x/crypto/bcrypt
// for password hashing — no auth framework, no session store (the cookie carries
// its own expiry and signature; account existence is checked against the bbolt
// store at validate time so `users rm` breaks a live session immediately).
//
// Two gate modes with one release of coexistence:
//
//   - ModeAccounts: the `users` bucket is non-empty. Login is username+password
//     against a bcrypt hash; the cookie carries the user id. This is the
//     plan-of-record per-employee gate.
//   - ModeLegacy: no accounts and an operatorPassword configured. Single shared
//     password, cookie carries no user id. Kept for one release so an existing
//     deployment can migrate without a hard cut (the plan says remove the field
//     the release after). A deprecation warning points at `users add`.
//   - ModeDisabled: neither. The caller decides to run open (dev) with a warning.
//
// This gates only the browser-facing worker-UI proxy. The worker<->master
// ConnectRPC/tunnel channel is authenticated separately by the pairing secret
// and worker token and is NOT affected by this gate.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// CookieName is the operator session cookie name.
const CookieName = "og_operator"

// UserChecker is the store slice the gate needs. Satisfied by *registry.Store.
// Keeping it an interface (not the concrete type) keeps the auth package
// decoupled from the registry's schema.
type UserChecker interface {
	// GetPasswordHash returns the stored bcrypt hash for username, true if the
	// account exists, and any lookup error. The gate must fail closed on error.
	GetPasswordHash(username string) (hash string, exists bool, err error)
	// UserExists reports whether an account with the cookie's user id still
	// exists (the A1 validate-time check). The gate must fail closed on error.
	UserExists(username string) (bool, error)
	// HasUsers reports whether the users bucket is non-empty, used to choose
	// ModeAccounts over ModeLegacy. The gate must fail closed on error.
	HasUsers() (bool, error)
	// GetUserWorkspaces returns the account's workspace allowlist (nil/empty =
	// the account may open any workspace). Consulted at login (to pin the
	// allowlist into the session cookie) and at validate time (to re-derive the
	// fingerprint). The gate must fail closed on error.
	GetUserWorkspaces(username string) ([]string, error)
}

// Mode is which operator gate is active.
type Mode int

const (
	// ModeDisabled means no password and no accounts — the gate enforces nothing.
	ModeDisabled Mode = iota
	// ModeLegacy is the coexistence window: a shared operatorPassword, no users.
	ModeLegacy
	// ModeAccounts is per-employee login backed by the bbolt users bucket.
	ModeAccounts
	// ModeLocked is an explicit fail-closed state for an unrecoverable accounts
	// store (A2): the gate is Enabled (so the worker-UI proxy demands a session)
	// but authentication ALWAYS fails until a human re-seeds accounts and
	// restarts. It exists so a corrupt users bucket can never "recover
	// authentication into the open".
	ModeLocked
)

func (m Mode) String() string {
	switch m {
	case ModeLegacy:
		return "legacy"
	case ModeAccounts:
		return "accounts"
	case ModeLocked:
		return "locked"
	default:
		return "disabled"
	}
}

// Gate performs operator authentication.
type Gate struct {
	store        UserChecker
	mode         Mode
	signingKey   []byte // random per process start; a restart invalidates sessions
	ttl          time.Duration
	cookieDomain string
	secure       bool

	// legacy password (ModeLegacy only), kept as bytes and compared with
	// constant-time compare as before.
	legacyPassword []byte

	// dummyHash is a bcrypt hash of a random secret, precomputed at
	// construction. On an unknown username the presented password is compared
	// against it (rather than rejected instantly) so response timing does not
	// enumerate valid usernames (A3).
	dummyHash []byte
}

// NewGate builds a Gate. The mode is resolved from the store + legacy password:
// if any account exists, ModeAccounts wins over a configured operatorPassword;
// otherwise a non-empty operatorPassword gives ModeLegacy; neither gives
// ModeDisabled. The bcrypt cost uses bcrypt.DefaultCost so a legit login and
// the dummy-hash rejection take comparable time.
func NewGate(store UserChecker, legacyPassword string, ttl time.Duration, cookieDomain string, secure bool) (*Gate, error) {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate session signing key: %w", err)
	}
	// Dummy hash of a random secret for the constant-time unknown-user path.
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate dummy hash secret: %w", err)
	}
	dummy, err := bcrypt.GenerateFromPassword(secret, bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("precompute dummy hash: %w", err)
	}

	mode := ModeDisabled
	if store != nil {
		// Any account present means per-employee login (the coexistence rule:
		// accounts win over a configured operatorPassword). A store lookup error
		// is fail-closed — treat as disabled rather than silently running unauth
		// or legacy.
		has, err := store.HasUsers()
		if err != nil {
			return nil, fmt.Errorf("resolve user store: %w", err)
		}
		if has {
			mode = ModeAccounts
		} else if legacyPassword != "" {
			mode = ModeLegacy
		}
	} else if legacyPassword != "" {
		mode = ModeLegacy
	}

	return &Gate{
		store:          store,
		mode:           mode,
		signingKey:     key,
		ttl:            ttl,
		cookieDomain:   cookieDomain,
		secure:         secure,
		legacyPassword: []byte(legacyPassword),
		dummyHash:      dummy,
	}, nil
}

// NewLockedGate builds a Gate in ModeLocked: the gate is Enabled (operator
// surfaces demand a session) but every authentication attempt fails. Used by
// the master when the accounts store is unrecoverably corrupt (A2) — it is the
// fail-closed posture that never opens authentication after a loss of the
// users bucket.
func NewLockedGate(ttl time.Duration, cookieDomain string, secure bool) (*Gate, error) {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate session signing key: %w", err)
	}
	return &Gate{
		mode:         ModeLocked,
		signingKey:   key,
		ttl:          ttl,
		cookieDomain: cookieDomain,
		secure:       secure,
	}, nil
}

// Mode returns the resolved gate mode.
func (g *Gate) Mode() Mode { return g.mode }

// Enabled reports whether the gate enforces auth.
func (g *Gate) Enabled() bool { return g.mode != ModeDisabled }

// Authenticate verifies a username+password and returns the user id to embed in
// the session cookie on success. In ModeLegacy the username is ignored (there is
// one shared password); in ModeAccounts an unknown user or wrong password is
// rejected in comparable time (A3), and a store lookup error fails closed.
func (g *Gate) Authenticate(username, password string) (userID string, ok bool) {
	id, _, ok := g.AuthenticateScoped(username, password)
	return id, ok
}

// AuthenticateScoped verifies a username+password like Authenticate and
// additionally returns the account's workspace allowlist (nil/empty =
// unrestricted) so the caller can pin it into the session cookie. ModeLegacy
// has no per-user allowlist and returns ("", nil, true) on success. A store
// error — including reading the allowlist — fails closed.
func (g *Gate) AuthenticateScoped(username, password string) (userID string, workspaces []string, ok bool) {
	switch g.mode {
	case ModeDisabled, ModeLocked:
		return "", nil, false
	case ModeLegacy:
		if subtle.ConstantTimeCompare([]byte(password), g.legacyPassword) == 1 {
			return "", nil, true // legacy cookie carries no user id
		}
		return "", nil, false
	}

	// ModeAccounts.
	if g.store == nil {
		return "", nil, false
	}
	hash, exists, err := g.store.GetPasswordHash(username)
	if err != nil {
		return "", nil, false // fail closed on store error
	}
	if !exists {
		// Unknown user: still run a bcrypt compare so the response time matches
		// a wrong-password-on-a-real-account (A3 constant-time path).
		_ = bcrypt.CompareHashAndPassword(g.dummyHash, []byte(password))
		return "", nil, false
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return "", nil, false
	}
	workspaces, err = g.store.GetUserWorkspaces(username)
	if err != nil {
		return "", nil, false // fail closed on store error
	}
	return username, workspaces, true
}

// Authenticated reports whether the request carries a valid, unexpired session
// for a user that still exists in the store (A1: `users rm` breaks live
// sessions). Fail-closed: a store lookup error rejects.
func (g *Gate) Authenticated(r *http.Request) bool {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return false
	}
	return g.validate(c.Value, time.Now())
}

// SetCookie writes a fresh signed session cookie for userID. In ModeLegacy
// userID is ignored (the legacy cookie carries no user id). The cookie carries
// no workspace fingerprint, so it validates as "unrestricted" — a
// pre-allowlist-shaped cookie kept valid for one release of coexistence. New
// logins must use SetCookieScoped.
func (g *Gate) SetCookie(w http.ResponseWriter, userID string) {
	c := &http.Cookie{
		Name:     CookieName,
		Value:    g.issue(time.Now(), userID),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   g.secure,
		Expires:  time.Now().Add(g.ttl),
	}
	if g.cookieDomain != "" {
		c.Domain = g.cookieDomain
	}
	http.SetCookie(w, c)
}

// SetCookieScoped writes a fresh signed session cookie for userID, pinning the
// account's workspace allowlist as a fingerprint (workspaceFingerprint). The
// pin is what makes an admin-side allowlist change invalidate live sessions at
// validate time (the same immediacy A1 gives `users rm`): validate recomputes
// the fingerprint from the store's CURRENT allowlist and compares. An empty
// allowlist is pinned too — "unrestricted as of login" — so restricting the
// account later still forces a re-login. In ModeLegacy the user/allowlist
// arguments are ignored.
func (g *Gate) SetCookieScoped(w http.ResponseWriter, userID string, workspaces []string) {
	c := &http.Cookie{
		Name:     CookieName,
		Value:    g.issueScoped(time.Now(), userID, workspaceFingerprint(workspaces)),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   g.secure,
		Expires:  time.Now().Add(g.ttl),
	}
	if g.cookieDomain != "" {
		c.Domain = g.cookieDomain
	}
	http.SetCookie(w, c)
}

// ClearCookie expires the session cookie.
func (g *Gate) ClearCookie(w http.ResponseWriter) {
	c := &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   g.secure,
		MaxAge:   -1,
	}
	if g.cookieDomain != "" {
		c.Domain = g.cookieDomain
	}
	http.SetCookie(w, c)
}

// WorkspacesForUser returns the CURRENT workspace allowlist for the session's
// user — read from the store at call time, never from the cookie's pinned
// fingerprint — plus ok=false when the request carries no usable session or a
// store error occurs. ok=false means deny: the caller must fail closed (403
// everything). Outside ModeAccounts there is no per-user allowlist at all, so
// it returns (nil, true): unrestricted, as before per-user accounts shipped.
// A nil/empty result with ok=true means the account is unrestricted. Call this
// only after Authenticated passed; it re-verifies the cookie signature itself
// so it never trusts an unverified user id.
func (g *Gate) WorkspacesForUser(r *http.Request) ([]string, bool) {
	if g.mode != ModeAccounts || g.store == nil {
		// No per-user allowlist exists outside ModeAccounts.
		return nil, true
	}
	userID, ok := g.UserForSession(r)
	if !ok {
		return nil, false
	}
	workspaces, err := g.store.GetUserWorkspaces(userID)
	if err != nil {
		return nil, false // fail closed on store error
	}
	return workspaces, true
}

// UserForSession verifies the request's session cookie and returns the user id
// it carries — ok=false when the request carries no usable session (no cookie,
// bad signature, expired, account removed) or the gate is not in ModeAccounts.
// The id is re-verified against the store (A1), never trusted from the cookie
// alone. Callers that need the allowlist use WorkspacesForUser.
func (g *Gate) UserForSession(r *http.Request) (string, bool) {
	if g.mode != ModeAccounts || g.store == nil {
		return "", false
	}
	c, err := r.Cookie(CookieName)
	if err != nil {
		return "", false
	}
	userID, ok := g.decodeSession(c.Value, time.Now())
	if !ok || userID == "" {
		return "", false
	}
	return userID, true
}

// issue produces "base64url(payload).base64url(hmac(payload))". The payload is
// the expiry unix seconds, plus "|userID" in ModeAccounts (the cookie then
// carries the logged-in user's id so validate can confirm it still exists).
// This is the pre-allowlist cookie shape, kept valid so sessions issued before
// the workspace allowlist shipped survive the upgrade.
func (g *Gate) issue(now time.Time, userID string) string {
	payload := strconv.FormatInt(now.Add(g.ttl).Unix(), 10)
	if g.mode == ModeAccounts && userID != "" {
		payload = payload + "|" + userID
	}
	sig := g.sign(payload)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(sig)
}

// issueScoped is issue for the allowlist era: the ModeAccounts payload gains a
// third "|<wsFP>" field pinning the account's allowlist as of login.
func (g *Gate) issueScoped(now time.Time, userID, wsFP string) string {
	payload := strconv.FormatInt(now.Add(g.ttl).Unix(), 10)
	if g.mode == ModeAccounts && userID != "" {
		payload = payload + "|" + userID + "|" + wsFP
	}
	sig := g.sign(payload)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(sig)
}

func (g *Gate) sign(payload string) []byte {
	m := hmac.New(sha256.New, g.signingKey)
	m.Write([]byte(payload))
	return m.Sum(nil)
}

// decodeSession verifies the cookie's signature and expiry against the gate's
// mode and returns the user id it carries ("" for a legacy cookie). Payload
// shapes, split on "|":
//
//	ModeLegacy:   "<expiry>"                          (1 field)
//	ModeAccounts: "<expiry>|<userID>"                 (2 fields — pre-allowlist
//	                                                  cookie, unrestricted)
//	              "<expiry>|<userID>|<wsFP>"          (3 fields — pinned allowlist)
//
// Anything else is malformed and rejected. Fail-closed: a store lookup error
// rejects, as does a wsFP that no longer matches the store's current allowlist.
func (g *Gate) decodeSession(token string, now time.Time) (userID string, ok bool) {
	if g.mode == ModeLocked {
		// A locked gate can never have issued a valid session; reject everything.
		return "", false
	}
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	if subtle.ConstantTimeCompare(sig, g.sign(string(payload))) != 1 {
		return "", false
	}
	payloadStr := string(payload)

	if g.mode != ModeAccounts {
		// A legacy cookie is just the expiry.
		exp, err := strconv.ParseInt(payloadStr, 10, 64)
		if err != nil {
			return "", false
		}
		if !now.Before(time.Unix(exp, 0)) {
			return "", false
		}
		return "", true
	}

	fields := strings.Split(payloadStr, "|")
	if len(fields) < 2 || len(fields) > 3 {
		return "", false // account cookie must carry expiry|userID[|wsFP]
	}
	expStr, userID := fields[0], fields[1]
	wsFP := ""
	if len(fields) == 3 {
		wsFP = fields[2]
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return "", false
	}
	if !now.Before(time.Unix(exp, 0)) {
		return "", false
	}
	if g.store == nil {
		return "", false
	}
	exists, err := g.store.UserExists(userID)
	if err != nil {
		return "", false // fail closed on store error
	}
	if !exists {
		return "", false // A1: `users rm` breaks that user's sessions immediately
	}
	if wsFP != "" {
		// The cookie pins the allowlist as of login; recompute from the store's
		// current value so an admin-side change (set-workspaces) kills the
		// session with the same immediacy A1 gives `users rm`.
		current, err := g.store.GetUserWorkspaces(userID)
		if err != nil {
			return "", false // fail closed on store error
		}
		if subtle.ConstantTimeCompare([]byte(wsFP), []byte(workspaceFingerprint(current))) != 1 {
			return "", false
		}
	}
	return userID, true
}

func (g *Gate) validate(token string, now time.Time) bool {
	_, ok := g.decodeSession(token, now)
	return ok
}

// workspaceFingerprint is the short hash a session cookie carries for the
// account's allowlist: the first 16 hex chars of sha256 over the workspace
// identifiers sorted and comma-joined. It is not a secret and reveals nothing
// on its own; it exists so validate can detect that the store's allowlist has
// drifted from the one in force at login without the cookie having to carry
// (or reveal) the list itself. Empty list → fingerprint of the empty string,
// deliberately a NON-empty hex value, so "unrestricted as of login" is still
// pinned and a later restriction invalidates the session.
func workspaceFingerprint(list []string) string {
	sorted := append([]string(nil), list...)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, ",")))
	return hex.EncodeToString(sum[:])[:16]
}
