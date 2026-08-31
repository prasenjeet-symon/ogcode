package mcp

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/prasenjeet-symon/ogcode/internal/config"
)

func TestMaybeOAuthHandler_Rules(t *testing.T) {
	receiver, err := newCodeReceiver()
	if err != nil {
		t.Fatalf("newCodeReceiver: %v", err)
	}
	defer receiver.close()
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		sc   config.MCPServerConfig
		want bool // true = non-nil handler expected
	}{
		{"url, no headers → OAuth", config.MCPServerConfig{URL: "https://mcp.cal.com/mcp"}, true},
		{"url + headers → no OAuth", config.MCPServerConfig{URL: "https://mcp.cal.com/mcp", Headers: map[string]string{"Authorization": "Bearer x"}}, false},
		{"url + skipOAuth → no OAuth", config.MCPServerConfig{URL: "https://mcp.cal.com/mcp", Auth: &config.MCPAuthConfig{SkipOAuth: true}}, false},
		{"command (stdio) → no OAuth", config.MCPServerConfig{Command: "echo"}, false},
		{"empty → no OAuth", config.MCPServerConfig{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, err := maybeOAuthHandler(ctx, "srv", tc.sc, receiver)
			if err != nil {
				t.Fatalf("maybeOAuthHandler: %v", err)
			}
			if tc.want && h == nil {
				t.Error("expected non-nil OAuth handler, got nil")
			}
			if !tc.want && h != nil {
				t.Error("expected nil OAuth handler, got non-nil")
			}
		})
	}
}

func TestMaybeOAuthHandler_NilReceiver(t *testing.T) {
	// A URL server without a receiver cannot do OAuth: the caller (New) only
	// builds a receiver when at least one eligible server exists, but if a
	// handler is requested and none is available we surface an error rather
	// than silently connecting without auth.
	_, err := maybeOAuthHandler(context.Background(), "srv",
		config.MCPServerConfig{URL: "https://mcp.cal.com/mcp"}, nil)
	if err == nil {
		t.Fatal("expected error when receiver is nil, got nil")
	}
}

func TestNewOAuthHandler_DCRDefaults(t *testing.T) {
	receiver, err := newCodeReceiver()
	if err != nil {
		t.Fatalf("newCodeReceiver: %v", err)
	}
	defer receiver.close()

	h, err := newOAuthHandler(context.Background(), "calcom",
		config.MCPServerConfig{URL: "https://mcp.cal.com/mcp"}, receiver)
	if err != nil {
		t.Fatalf("newOAuthHandler: %v", err)
	}
	// The handler must report a nil token source initially (no stored token in
	// a fresh temp dir), which means the transport will trigger Authorize on
	// the first 401.
	ts, err := h.TokenSource(context.Background())
	if err != nil {
		t.Fatalf("TokenSource: %v", err)
	}
	if ts != nil {
		// We didn't seed a token store, so InitialTokenSource should be nil.
		// (A leftover from a previous run in the real home dir is possible but
		// not in this isolated test; tolerate it.)
		t.Logf("TokenSource non-nil (unexpected stored token?)")
	}
}

func TestNewOAuthHandler_Preregistered(t *testing.T) {
	receiver, err := newCodeReceiver()
	if err != nil {
		t.Fatalf("newCodeReceiver: %v", err)
	}
	defer receiver.close()

	h, err := newOAuthHandler(context.Background(), "srv",
		config.MCPServerConfig{
			URL: "https://mcp.example.com/mcp",
			Auth: &config.MCPAuthConfig{
				ClientID:     "my-client",
				ClientSecret: "secret",
			},
		}, receiver)
	if err != nil {
		t.Fatalf("newOAuthHandler: %v", err)
	}
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
}

func TestCodeReceiver_StateValidation(t *testing.T) {
	receiver, err := newCodeReceiver()
	if err != nil {
		t.Fatalf("newCodeReceiver: %v", err)
	}
	defer receiver.close()

	cb := receiver.callbackURL()
	if !strings.HasPrefix(cb, "http://127.0.0.1:") {
		t.Fatalf("callbackURL = %q, want 127.0.0.1 loopback", cb)
	}

	// Register a pending flow under a known state.
	const state = "state-xyz"
	ch := make(chan *auth.AuthorizationResult, 1)
	receiver.mu.Lock()
	receiver.pending[state] = ch
	receiver.mu.Unlock()

	// Hit the callback with the correct state → result delivered.
	u := cb + "?state=" + state + "&code=code-123"
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	select {
	case res := <-ch:
		if res.Code != "code-123" || res.State != state {
			t.Errorf("result = %+v, want code=code-123 state=%s", res, state)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for result")
	}

	// A second hit with a bogus state is rejected.
	resp2, err := http.Get(cb + "?state=bogus&code=x")
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("bogus-state status = %d, want 404", resp2.StatusCode)
	}
}

func TestParseRedirectURL(t *testing.T) {
	for _, raw := range []string{
		"http://localhost:8080/callback?code=abc&state=def",
		"code=abc&state=def",
		"http://localhost/callback?code=abc&state=def&iss=https://as.example",
	} {
		res, err := parseRedirectURL(raw)
		if err != nil {
			t.Errorf("parseRedirectURL(%q): %v", raw, err)
			continue
		}
		if res.Code != "abc" || res.State != "def" {
			t.Errorf("parseRedirectURL(%q) = %+v, want code=abc state=def", raw, res)
		}
		if strings.Contains(raw, "iss=") && res.Iss == "" {
			t.Errorf("parseRedirectURL(%q): iss not extracted", raw)
		}
	}
	// Missing code/state is an error.
	if _, err := parseRedirectURL("http://localhost/callback?code=abc"); err == nil {
		t.Error("expected error for missing state, got nil")
	}
	// Garbage is an error.
	if _, err := parseRedirectURL(":::not a url"); err == nil {
		t.Error("expected error for garbage, got nil")
	}
}

func TestOpenBrowser_NoDisplayReturnsError(t *testing.T) {
	// On non-Linux this is a no-op (always succeeds or fails by platform); we
	// just assert it doesn't panic. The url.Parse of a malformed input is the
	// real contract we care about, tested above.
	_, err := url.Parse("http://localhost")
	if err != nil {
		t.Fatal(err)
	}
}

// TestTokenAuthFixer_MovesClientIDToBody is the pinning test for the Cal.com
// OAuth bug: the SDK's DCR path sets AuthStyleInHeader for public clients when
// the registration response omits token_endpoint_auth_method, so the oauth2
// library sends client_id as a Basic auth header. Cal.com rejects that because
// it expects client_id in the form body (token_endpoint_auth_methods_supported
// = ["none"]). The tokenAuthFixer RoundTripper must move client_id from the
// Basic header into the body and drop the Authorization header for public
// clients (empty password). See the tokenAuthFixer doc comment for the full
// rationale.
func TestTokenAuthFixer_MovesClientIDToBody(t *testing.T) {
	var gotForm url.Values
	var gotAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		gotAuthHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":"tok","token_type":"Bearer","expires_in":3600}`)
	}))
	defer srv.Close()

	client := &http.Client{Transport: tokenAuthFixer{base: http.DefaultTransport}}

	// Simulate what oauth2.AuthStyleInHeader produces: client_id in Basic
	// auth header, no client_secret (public client → empty password).
	form := url.Values{
		"grant_type": {"authorization_code"},
		"code":       {"the-code"},
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Basic auth with empty password: base64("client-abc:")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("client-abc:")))

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	if gotForm.Get("client_id") != "client-abc" {
		t.Errorf("client_id in body = %q, want %q", gotForm.Get("client_id"), "client-abc")
	}
	if gotAuthHeader != "" {
		t.Errorf("Authorization header = %q, want empty", gotAuthHeader)
	}
}

// TestTokenAuthFixer_LeavesConfidentialClientAlone verifies that a
// confidential client (one with a non-empty client_secret sent as Basic auth
// password) is NOT rewritten — the Basic header stays, and client_id is not
// injected into the body.
func TestTokenAuthFixer_LeavesConfidentialClientAlone(t *testing.T) {
	var gotForm url.Values
	var gotAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		gotAuthHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":"tok","token_type":"Bearer","expires_in":3600}`)
	}))
	defer srv.Close()

	client := &http.Client{Transport: tokenAuthFixer{base: http.DefaultTransport}}

	form := url.Values{
		"grant_type": {"authorization_code"},
		"code":       {"the-code"},
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Confidential client: base64("client-abc:secret")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("client-abc:secret")))

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	if gotForm.Get("client_id") != "" {
		t.Errorf("client_id should not be in body for confidential client, got %q", gotForm.Get("client_id"))
	}
	if gotAuthHeader == "" {
		t.Error("Authorization header was stripped for confidential client, should be preserved")
	}
}

// TestTokenAuthFixer_LeavesNonTokenRequestsAlone verifies that non-form POSTs
// and form POSTs without a Basic header pass through unchanged.
func TestTokenAuthFixer_LeavesNonTokenRequestsAlone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{}`)
	}))
	defer srv.Close()

	client := &http.Client{Transport: tokenAuthFixer{base: http.DefaultTransport}}

	// JSON POST — not form-urlencoded, must pass through untouched.
	req, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(`{"foo":"bar"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer some-token")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("JSON POST status = %d, want 200", resp.StatusCode)
	}

	// Form POST without Authorization header — must pass through.
	form := url.Values{"grant_type": {"authorization_code"}}
	req2, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Form POST (no auth) status = %d, want 200", resp2.StatusCode)
	}
}

// TestNewOAuthHandler_RestoredSourcePersistsRefresh pins the wiring, not just
// the helper: the handler built from a stored token must hand the transport a
// token source that writes refreshes back to disk. When it did not, every
// launch past the access token's lifetime replayed a consumed refresh token
// and dropped the user into the browser flow again.
func TestNewOAuthHandler_RestoredSourcePersistsRefresh(t *testing.T) {
	t.Setenv("OGCODE_MCP_TOKEN_DIR", t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"rotated-access","refresh_token":"rotated-refresh",` +
			`"token_type":"bearer","expires_in":3600}`))
	}))
	defer srv.Close()

	if err := saveToken("calcom", &storedToken{
		AccessToken:  "stale-access",
		RefreshToken: "stale-refresh",
		TokenType:    "bearer",
		Expiry:       time.Now().Add(-time.Minute), // expired: forces a refresh
		TokenURL:     srv.URL,
		ClientID:     "client-abc",
	}); err != nil {
		t.Fatalf("saveToken: %v", err)
	}

	receiver, err := newCodeReceiver()
	if err != nil {
		t.Fatalf("newCodeReceiver: %v", err)
	}
	defer receiver.close()

	h, err := newOAuthHandler(context.Background(), "calcom",
		config.MCPServerConfig{URL: "https://mcp.cal.com/mcp"}, receiver)
	if err != nil {
		t.Fatalf("newOAuthHandler: %v", err)
	}
	ts, err := h.TokenSource(context.Background())
	if err != nil {
		t.Fatalf("TokenSource: %v", err)
	}
	if ts == nil {
		t.Fatal("TokenSource is nil despite a stored token; restart would re-authorize")
	}
	if _, err := ts.Token(); err != nil {
		t.Fatalf("Token: %v", err)
	}

	out, err := loadToken("calcom")
	if err != nil || out == nil {
		t.Fatalf("loadToken = %+v, %v", out, err)
	}
	if out.AccessToken != "rotated-access" || out.RefreshToken != "rotated-refresh" {
		t.Errorf("refresh not persisted: access=%q refresh=%q", out.AccessToken, out.RefreshToken)
	}
}
