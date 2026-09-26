package server

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// OGX is the subscription plan sold by OG Lab (ogcode's maker). Everything
// commercial — sign-up, sign-in, payment, plan state — happens on the web
// side in the user's browser; ogcode's whole job is to hand the browser off
// and receive the token that links this install to the account.
//
// The flow, end to end:
//
//  1. POST /api/ogx/connect — mints a single-use state and returns the OG Lab
//     connect URL carrying that state plus a redirect_uri pointing back at
//     this server. The frontend opens the URL in a new tab.
//  2. The user signs up / signs in / pays on the web side.
//  3. The web side redirects the browser to the redirect_uri with the state
//     it was given and the freshly minted token.
//  4. GET /api/ogx/callback — validates and consumes the state, stores the
//     token in the global DB, and renders a "return to ogcode" page.
//  5. The settings screen, polling GET /api/ogx/status since step 1, sees
//     connected=true and flips the tab to its active state.
//
// Model routing for the plan runs through OG Lab's gateway using this token:
// the provider is registered from the stored link and serves exactly the models
// the plan grants.

// defaultOGXConnectURL is where the browser hand-off goes when the env var is
// unset. OGX_CONNECT_URL overrides it (with a trailing ? or &, no space) so a
// development or staging deployment can point at its own web side.
const defaultOGXConnectURL = "https://ogx.ogcode.xyz/connect"

// ogxStateTTL bounds how long a minted state is redeemable. The user is
// clicking through sign-up and payment, so it is generous — but a state that
// was never redeemed must not stay valid forever, because whoever presents it
// gets to write a token into this install.
const ogxStateTTL = 15 * time.Minute

// ogxPendingStates tracks the states minted by /connect and not yet redeemed
// by /callback. In memory on purpose: a state is only meaningful to the
// server process that minted it, and a restart mid-flow simply means clicking
// Connect again.
type ogxPendingStates struct {
	mu     sync.Mutex
	states map[string]time.Time // state -> expiry
}

// mint creates, records, and returns a new single-use state.
func (p *ogxPendingStates) mint() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("mint ogx state: %w", err)
	}
	state := base64.RawURLEncoding.EncodeToString(raw)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.states == nil {
		p.states = map[string]time.Time{}
	}
	now := time.Now()
	for s, exp := range p.states {
		if now.After(exp) {
			delete(p.states, s)
		}
	}
	p.states[state] = now.Add(ogxStateTTL)
	return state, nil
}

// redeem consumes a state, reporting whether it was one we minted and still
// live. Single use: a replayed redirect fails the second time.
func (p *ogxPendingStates) redeem(state string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	exp, ok := p.states[state]
	if !ok {
		return false
	}
	delete(p.states, state)
	return time.Now().Before(exp)
}

// handleOGXConnect starts the browser hand-off: mints a state and returns the
// URL the frontend should open. The redirect_uri is pinned to loopback rather
// than built from the Host the browser used, because the web side refuses any
// non-loopback redirect (see oglab's loopbackRedirect: scheme must be http and
// the hostname localhost or a loopback IP). A user reaching ogcode over the LAN
// or through a tunnel would otherwise send a Host the web side rejects and the
// redirect would never come back.
func (s *Server) handleOGXConnect(w http.ResponseWriter, r *http.Request) {
	state, err := s.ogxPending.mint()
	if err != nil {
		http.Error(w, "failed to start OGX connect", http.StatusInternalServerError)
		return
	}
	base := os.Getenv("OGX_CONNECT_URL")
	if base == "" {
		base = defaultOGXConnectURL
	}
	q := url.Values{}
	q.Set("state", state)
	q.Set("redirect_uri", fmt.Sprintf("http://127.0.0.1:%d/api/ogx/callback", s.Port()))
	q.Set("app", provider.OGXAppID)
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": base + sep + q.Encode()})
}

// handleOGXCallback is the redirect target the web side sends the browser to
// after sign-up and payment. It is a top-level browser navigation, so both
// outcomes render small HTML pages rather than JSON.
func (s *Server) handleOGXCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if !s.ogxPending.redeem(q.Get("state")) {
		ogxCallbackPage(w, http.StatusBadRequest, "This connect link is invalid or has expired.",
			"Return to ogcode settings and click Connect again.")
		return
	}
	token := q.Get("token")
	if token == "" {
		ogxCallbackPage(w, http.StatusBadRequest, "The redirect carried no token.",
			"Return to ogcode settings and click Connect again.")
		return
	}
	acct := &session.OGXAccount{
		Token: token,
		Email: q.Get("email"),
		Plan:  q.Get("plan"),
	}
	if err := session.SetOGXAccount(s.globalDB, acct); err != nil {
		ogxCallbackPage(w, http.StatusInternalServerError, "ogcode could not save the connection.",
			"Return to ogcode settings and click Connect again.")
		return
	}
	// The link just appeared, so the registry has to learn about it now: the
	// running server holds providers built at startup, and the next prompt would
	// otherwise miss the plan until a restart.
	s.reloadProviders()
	ogxCallbackPage(w, http.StatusOK, "OGX connected.",
		"You can close this tab and return to ogcode.")
}

// handleOGXStatus reports the connection for the settings tab. The token
// itself never leaves the server — connected is a boolean, not a credential.
func (s *Server) handleOGXStatus(w http.ResponseWriter, r *http.Request) {
	acct, err := session.GetOGXAccount(s.globalDB)
	if err != nil {
		http.Error(w, "failed to read OGX account", http.StatusInternalServerError)
		return
	}
	if acct == nil {
		writeJSON(w, http.StatusOK, map[string]any{"connected": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"connected":   true,
		"email":       acct.Email,
		"plan":        acct.Plan,
		"connectedAt": acct.TimeConnected,
	})
}

// handleOGXDisconnect forgets the local connection. The plan and account on
// the web side are untouched — reconnecting restores them.
func (s *Server) handleOGXDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := session.DeleteOGXAccount(s.globalDB); err != nil {
		http.Error(w, "failed to disconnect OGX", http.StatusInternalServerError)
		return
	}
	// Mirror the connect path: drop the provider from the live registry rather
	// than leaving it until the next restart.
	s.reloadProviders()
	writeJSON(w, http.StatusOK, map[string]any{"connected": false})
}

// ogxCallbackPage renders the minimal page the browser lands on after the
// redirect, in the same spirit as the MCP OAuth "you can close this tab"
// page.
func ogxCallbackPage(w http.ResponseWriter, status int, headline, detail string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w,
		`<!doctype html><html><head><title>ogcode</title></head>`+
			`<body style="font-family:system-ui;margin:4rem auto;max-width:28rem">`+
			`<p><strong>%s</strong></p><p>%s</p></body></html>`,
		html.EscapeString(headline), html.EscapeString(detail))
}
