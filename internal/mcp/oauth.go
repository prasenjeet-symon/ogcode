package mcp

// OAuth authorization-code (with PKCE) support for MCP HTTP servers. The
// protocol machinery — WWW-Authenticate parsing, Protected Resource and
// Authorization Server metadata discovery, Dynamic Client Registration, PKCE,
// the code exchange, and token refresh — lives in the SDK's
// auth.AuthorizationCodeHandler. This file supplies the three things the SDK
// leaves to the application:
//
//   - A codeReceiver: opens the user's browser to the auth URL and captures
//     the localhost redirect carrying the code. One shared listener per
//     Manager (a single localhost port serves all servers; the state param
//     distinguishes flows).
//   - newOAuthHandler: assembles an auth.AuthorizationCodeHandlerConfig with
//     either Dynamic Client Registration (the default, "just works") or a
//     pre-registered client, loads a stored token as InitialTokenSource, and
//     wraps both token sources in savingTokenSource so tokens persist across
//     restarts and refreshes.
//   - fetcherFor: picks the browser fetcher or the headless manual-copy
//     fallback based on the environment.

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"

	"github.com/prasenjeet-symon/ogcode/internal/config"
)

// authTimeout caps a single Authorize flow (the user clicking through the
// browser). A server whose user never authorizes eventually times out and
// contributes no tools, rather than hanging startup indefinitely.
const authTimeout = 5 * time.Minute

// codeReceiver serves a single localhost callback endpoint for all OAuth
// flows in a Manager. The redirect handler validates the state param (CSRF
// protection), delivers the AuthorizationResult on the matching channel, and
// responds with a "you can close this tab" page.
type codeReceiver struct {
	listener net.Listener
	server   *http.Server

	mu      sync.Mutex
	pending map[string]chan *auth.AuthorizationResult
}

// newCodeReceiver binds a loopback listener on an OS-chosen port and starts
// the callback server. The chosen port is advertised to authorization servers
// via DCR, so any free port works and we avoid hardcoding one that may be
// taken. Returns nil if the listener cannot be bound (treated as "no OAuth"
// — servers requiring it will simply fail to connect and contribute no
// tools).
func newCodeReceiver() (*codeReceiver, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("oauth: bind localhost listener: %w", err)
	}
	r := &codeReceiver{
		listener: listener,
		pending:  make(map[string]chan *auth.AuthorizationResult),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", r.handleCallback)
	r.server = &http.Server{Handler: mux}
	go func() { _ = r.server.Serve(listener) }()
	return r, nil
}

// callbackURL is the loopback redirect URI advertised to authorization
// servers. The receiver's shared port serves every flow; the state param
// distinguishes them.
func (r *codeReceiver) callbackURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/callback", r.listener.Addr().(*net.TCPAddr).Port)
}

// close stops the callback server. Safe to call when r is nil.
func (r *codeReceiver) close() {
	if r == nil {
		return
	}
	_ = r.server.Close()
	_ = r.listener.Close()
}

// handleCallback receives the authorization-server redirect, validates state
// against a pending flow, and delivers the result. An unknown or mismatched
// state is rejected (404 / error page) to prevent CSRF.
func (r *codeReceiver) handleCallback(w http.ResponseWriter, req *http.Request) {
	state := req.URL.Query().Get("state")
	code := req.URL.Query().Get("code")
	iss := req.URL.Query().Get("iss")

	if state == "" || code == "" {
		http.Error(w, "missing code or state", http.StatusBadRequest)
		return
	}

	r.mu.Lock()
	ch := r.pending[state]
	delete(r.pending, state)
	r.mu.Unlock()

	if ch == nil {
		http.Error(w, "unknown or expired authorization flow", http.StatusNotFound)
		return
	}

	ch <- &auth.AuthorizationResult{Code: code, State: state, Iss: iss}
	// The user sees this in the browser tab that just returned from the AS.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!DOCTYPE html><html><head><title>Authorized</title></head>` +
		`<body><p>Authorization complete. You can close this tab and return to ogcode.</p></body></html>`))
}

// getAuthorizationCode implements auth.AuthorizationCodeFetcher. It registers
// a state-keyed channel, opens the user's browser (or prints the URL for the
// manual-copy fallback in headless environments), then waits for the redirect,
// the context, or the authTimeout — whichever comes first.
func (r *codeReceiver) getAuthorizationCode(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
	parsed, err := url.Parse(args.URL)
	if err != nil {
		return nil, fmt.Errorf("oauth: parse auth URL: %w", err)
	}
	state := parsed.Query().Get("state")
	if state == "" {
		return nil, errors.New("oauth: auth URL has no state param")
	}

	ch := make(chan *auth.AuthorizationResult, 1)
	r.mu.Lock()
	r.pending[state] = ch
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.pending, state)
		r.mu.Unlock()
	}()

	if err := openBrowser(args.URL); err != nil {
		// Browser launch failed (headless, no DISPLAY). Fall back to manual
		// copy: print the URL and read the redirected URL from stdin.
		return manualCodeFetch(ctx, args.URL)
	}

	timer := time.NewTimer(authTimeout)
	defer timer.Stop()

	flowCtx := ctx
	if _, ok := flowCtx.Deadline(); !ok {
		c, cancel := context.WithTimeout(ctx, authTimeout)
		flowCtx = c
		defer cancel()
	}

	select {
	case res := <-ch:
		return res, nil
	case <-flowCtx.Done():
		return nil, fmt.Errorf("oauth: authorization timed out or cancelled: %w", flowCtx.Err())
	}
}

// openBrowser attempts to open urlStr in the user's default browser. Returns
// an error if no GUI/browser is available (the caller then falls back to
// manual copy).
func openBrowser(urlStr string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", urlStr).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", urlStr).Start()
	case "linux":
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return errors.New("no display")
		}
		return exec.Command("xdg-open", urlStr).Start()
	default:
		return fmt.Errorf("unsupported platform %q", runtime.GOOS)
	}
}

// manualCodeFetch prints the auth URL to stderr and prompts the user to paste
// the full redirected URL (containing code + state) back on stdin. This is the
// standard device-less OAuth manual flow for headless environments (CI, SSH).
func manualCodeFetch(ctx context.Context, authURL string) (*auth.AuthorizationResult, error) {
	fmt.Fprintf(os.Stderr, "\n[mcp] OAuth authorization required.\n")
	fmt.Fprintf(os.Stderr, "[mcp] Open this URL in a browser and authorize:\n  %s\n", authURL)
	fmt.Fprintf(os.Stderr, "[mcp] After authorizing, paste the full redirected URL here:\n  ")

	type line struct {
		s   string
		err error
	}
	ch := make(chan line, 1)
	go func() {
		var s string
		_, err := fmt.Scanln(&s)
		ch <- line{s, err}
	}()

	timer := time.NewTimer(authTimeout)
	defer timer.Stop()
	select {
	case l := <-ch:
		if l.err != nil {
			return nil, fmt.Errorf("oauth: reading redirected URL: %w", l.err)
		}
		return parseRedirectURL(l.s)
	case <-timer.C:
		return nil, errors.New("oauth: timed out waiting for redirected URL")
	case <-ctx.Done():
		return nil, fmt.Errorf("oauth: cancelled: %w", ctx.Err())
	}
}

// parseRedirectURL extracts code + state (+ iss) from the pasted redirected
// URL. Accepts both a full URL and a bare query string.
func parseRedirectURL(raw string) (*auth.AuthorizationResult, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		// Not a URL — try parsing as a query string.
		u, err = url.Parse("http://localhost/callback?" + raw)
		if err != nil {
			return nil, fmt.Errorf("oauth: could not parse redirected URL: %w", err)
		}
	}
	q := u.Query()
	code, state := q.Get("code"), q.Get("state")
	if code == "" || state == "" {
		return nil, errors.New("oauth: redirected URL is missing code or state")
	}
	return &auth.AuthorizationResult{Code: code, State: state, Iss: q.Get("iss")}, nil
}

// maybeOAuthHandler returns an auth.OAuthHandler for the server, or nil. The
// rule: a URL server with no static Headers gets an OAuth handler unless
// auth.skipOAuth is set. A server with Headers keeps the static-token path
// and gets no handler. Non-URL servers get no handler (stdio/SSE).
func maybeOAuthHandler(ctx context.Context, name string, sc config.MCPServerConfig, receiver *codeReceiver) (auth.OAuthHandler, error) {
	if sc.URL == "" || len(sc.Headers) > 0 {
		return nil, nil
	}
	if sc.Auth != nil && sc.Auth.SkipOAuth {
		return nil, nil
	}
	if receiver == nil {
		return nil, errors.New("oauth: no code receiver available")
	}
	return newOAuthHandler(ctx, name, sc, receiver)
}

// newOAuthHandler builds an auth.AuthorizationCodeHandler for the server.
// With no auth.clientId it uses Dynamic Client Registration (the default that
// makes Cal.com work with zero extra config); with auth.clientId it uses a
// pre-registered client. A stored token from a previous run is loaded as
// InitialTokenSource so restarts don't re-prompt. Both token-source paths —
// the restored one and the one NewTokenSource builds after a fresh code
// exchange — are wrapped in savingTokenSource, so the initial grant and every
// subsequent refresh are persisted to disk.
func newOAuthHandler(ctx context.Context, name string, sc config.MCPServerConfig, receiver *codeReceiver) (auth.OAuthHandler, error) {
	redirectURL := receiver.callbackURL()
	fetcher := auth.AuthorizationCodeFetcher(receiver.getAuthorizationCode)

	cfg := &auth.AuthorizationCodeHandlerConfig{
		RedirectURL:              redirectURL,
		AuthorizationCodeFetcher: fetcher,
		RequestRefreshToken:      true,
		// Wrap the HTTP client so token endpoint POSTs for public clients
		// (no secret) send client_id in the form body instead of a Basic
		// auth header. The SDK's DCR path defaults to AuthStyleInHeader when
		// the registration response omits token_endpoint_auth_method (Cal.com
		// does exactly this), which makes Cal.com reject the token exchange
		// with "Missing required parameters: client_id". The RoundTripper
		// catches that case at the wire level so the fix is independent of
		// the SDK's auth-style choice. See tokenAuthFixer docs for the full
		// rationale.
		Client: &http.Client{Transport: tokenAuthFixer{base: http.DefaultTransport}},
	}

	if sc.Auth != nil && sc.Auth.ClientID != "" {
		// Pre-registered client. DCR is skipped.
		creds := &oauthex.ClientCredentials{ClientID: sc.Auth.ClientID}
		if sc.Auth.ClientSecret != "" {
			creds.ClientSecretAuth = &oauthex.ClientSecretAuth{ClientSecret: sc.Auth.ClientSecret}
		}
		cfg.PreregisteredClient = creds
	} else {
		// Dynamic Client Registration — the default. The redirect URI we
		// advertise is the receiver's loopback callback.
		cfg.DynamicClientRegistrationConfig = &auth.DynamicClientRegistrationConfig{
			Metadata: &oauthex.ClientRegistrationMetadata{
				ClientName:   "ogcode",
				RedirectURIs: []string{redirectURL},
				GrantTypes:   []string{"authorization_code", "refresh_token"},
			},
		}
	}

	// Load a stored token, if one exists, and seed the handler with it so a
	// restart does not re-prompt the user.
	stored, err := loadToken(name)
	if err != nil {
		// loadToken is non-fatal (returns nil on missing/corrupt); a real
		// error here is a filesystem problem worth surfacing.
		return nil, fmt.Errorf("oauth: load token: %w", err)
	}
	if stored != nil {
		// The restored source must save too. Refreshes taken on this path go
		// through the oauth2 library, not through NewTokenSource (the SDK only
		// calls that after a fresh code exchange in Authorize), so an unwrapped
		// source silently drops every rotated refresh token on the floor.
		cfg.InitialTokenSource = savingTokenSourceFromStored(name, stored)
	}

	// Persist every access-token change (the initial grant and each refresh).
	cfg.NewTokenSource = func(c context.Context, oc *oauth2.Config, tok *oauth2.Token) (oauth2.TokenSource, error) {
		_ = saveToken(name, tokenToStored(tok, oc)) // best-effort; refresh still works
		return newSavingTokenSource(oc.TokenSource(c, tok), tok, func(persisted *oauth2.Token) error {
			return saveToken(name, tokenToStored(persisted, oc))
		}), nil
	}

	handler, err := auth.NewAuthorizationCodeHandler(cfg)
	if err != nil {
		return nil, fmt.Errorf("oauth: build handler: %w", err)
	}
	return handler, nil
}

// tokenToStored converts an oauth2.Token plus the config that produced it into
// the persisted form, capturing the token endpoint and client identity so a
// refresh after restart hits the same endpoints.
func tokenToStored(tok *oauth2.Token, oc *oauth2.Config) *storedToken {
	if tok == nil {
		return nil
	}
	return &storedToken{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		TokenType:    tok.TokenType,
		Expiry:       tok.Expiry,
		TokenURL:     oc.Endpoint.TokenURL,
		ClientID:     oc.ClientID,
		ClientSecret: oc.ClientSecret,
		Scopes:       oc.Scopes,
	}
}

// tokenAuthFixer is an http.RoundTripper that fixes token-endpoint requests
// for public OAuth clients (clients with no secret). Some authorization
// servers — notably Cal.com — advertise token_endpoint_auth_methods_supported
// = ["none"] (public client: client_id goes in the form body) but their
// Dynamic Client Registration response omits token_endpoint_auth_method. The
// MCP Go SDK's DCR path (go-sdk v1.7.0, authorization_code.go:564) maps that
// empty field to AuthStyleInHeader, so golang.org/x/oauth2 sends client_id as
// a Basic auth header and never puts it in the body. Cal.com rejects that with
// "Missing required parameters: client_id".
//
// The SDK sets a FIXED auth style (not AuthStyleAutoDetect), so the oauth2
// library's built-in retry-with-params never fires. This RoundTripper catches
// the mismatch at the wire level: when a token-endpoint POST carries a Basic
// Authorization header, the form body is a grant_type we recognise
// (authorization_code or refresh_token), and the Basic auth password is empty
// (public client — no secret), it moves client_id from the header into the
// body and drops the Authorization header. All other requests pass through
// untouched.
type tokenAuthFixer struct {
	base http.RoundTripper
}

func (f tokenAuthFixer) RoundTrip(req *http.Request) (*http.Response, error) {
	// Only touch form-POSTs with a Basic auth header.
	if req.Method != http.MethodPost ||
		!strings.HasPrefix(req.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		return f.base.RoundTrip(req)
	}
	authHeader := req.Header.Get("Authorization")
	if authHeader == "" {
		return f.base.RoundTrip(req)
	}
	const basicPrefix = "Basic "
	if !strings.HasPrefix(authHeader, basicPrefix) {
		return f.base.RoundTrip(req)
	}

	// Decode the Basic credentials to check for a public client (no password).
	decoded, err := base64Decode(authHeader[len(basicPrefix):])
	if err != nil {
		return f.base.RoundTrip(req)
	}
	// Basic auth is "user:pass"; a public client has an empty password.
	idx := strings.IndexByte(decoded, ':')
	if idx < 0 || idx != len(decoded)-1 {
		// password is non-empty (idx != len-1) — this is a confidential
		// client, leave the header alone.
		return f.base.RoundTrip(req)
	}
	clientID := decoded[:idx]

	// Read and parse the form body.
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return f.base.RoundTrip(req)
	}
	_ = req.Body.Close()
	form, err := url.ParseQuery(string(body))
	if err != nil {
		return f.base.RoundTrip(req)
	}

	// Only fix token-endpoint grant types we care about.
	switch form.Get("grant_type") {
	case "authorization_code", "refresh_token":
	default:
		return f.base.RoundTrip(req)
	}

	// Move client_id from the Basic header into the body and drop the header.
	if form.Get("client_id") == "" {
		form.Set("client_id", clientID)
	}
	fixedBody := form.Encode()
	req.Body = io.NopCloser(bytes.NewReader([]byte(fixedBody)))
	req.ContentLength = int64(len(fixedBody))
	req.Header.Del("Authorization")
	return f.base.RoundTrip(req)
}

// base64Decode decodes a standard base64 string, tolerating padding errors
// from Basic auth values that were url-encoded before base64.
func base64Decode(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
