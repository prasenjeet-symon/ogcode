package mcp

// Token persistence for MCP OAuth. One JSON file per server under
// ~/.ogcode/mcp-tokens/, written atomically with 0600 perms so a crash
// mid-write never corrupts the store and the credential is never committed
// to the project tree. The token store is read on startup to rebuild an
// oauth2.TokenSource as the handler's InitialTokenSource (so restarts don't
// re-prompt) and written on every access-token change via savingTokenSource.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// storedToken is the on-disk representation of an OAuth token plus the
// configuration captured at authorization time, so a refresh after restart
// hits the same token endpoint with the same client identity.
type storedToken struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
	// Configuration captured at authorization time so a refresh after restart
	// hits the same token endpoint with the same client identity.
	TokenURL     string   `json:"token_url"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
}

// tokenDir returns the directory holding per-server token files. It defaults
// to ~/.ogcode/mcp-tokens/ (runtime state, like the embed-model cache) and is
// overridable via OGCODE_MCP_TOKEN_DIR for tests, matching the
// OGCODE_EMBED_MODEL_DIR precedent.
func tokenDir() (string, error) {
	if env := os.Getenv("OGCODE_MCP_TOKEN_DIR"); env != "" {
		return env, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("mcp token store: cannot resolve home directory: %w", err)
	}
	return filepath.Join(home, ".ogcode", "mcp-tokens"), nil
}

// tokenPath returns the absolute path of the token file for the named server.
func tokenPath(name string) (string, error) {
	dir, err := tokenDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".json"), nil
}

// loadToken reads the stored token for the named server. It returns (nil, nil)
// when the file is absent (first run) — a missing store is not an error; we
// re-authorize. A corrupt or unreadable file is also treated as "no token"
// rather than fatal: the worst case is one re-prompt, which is safe.
func loadToken(name string) (*storedToken, error) {
	path, err := tokenPath(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, nil // corrupt/unreadable: re-authorize rather than fail
	}
	var t storedToken
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, nil // invalid JSON: re-authorize
	}
	if t.AccessToken == "" {
		return nil, nil
	}
	return &t, nil
}

// saveToken writes the token for the named server atomically with 0600 perms.
// It writes to a temp file in the same directory and renames over the target
// so a crash mid-write leaves either the old file or no file, never a
// truncated one.
func saveToken(name string, t *storedToken) error {
	if t == nil || t.AccessToken == "" {
		return nil
	}
	dir, err := tokenDir()
	if err != nil {
		return err
	}
	if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
		return fmt.Errorf("mcp token store: %w", mkErr)
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("mcp token store: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".token-*.json")
	if err != nil {
		return fmt.Errorf("mcp token store: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("mcp token store: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("mcp token store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("mcp token store: %w", err)
	}
	finalPath := filepath.Join(dir, name+".json")
	if err := os.Rename(tmpName, finalPath); err != nil {
		cleanup()
		return fmt.Errorf("mcp token store: %w", err)
	}
	return nil
}

// tokenSourceFromStored rebuilds an oauth2.TokenSource from a stored token,
// suitable for use as the handler's InitialTokenSource. The returned source
// refreshes against the same token endpoint using the stored client
// identity. Returns nil when t is nil (no stored token → trigger Authorize).
//
// The token source's HTTP client is wrapped with tokenAuthFixer so a public
// client's refresh (client_id in body, not Basic header) works on the first
// attempt — without it, the oauth2 AuthStyleAutoDetect retry would still
// succeed but only after a wasted failed request with AuthStyleInHeader.
func tokenSourceFromStored(t *storedToken) oauth2.TokenSource {
	if t == nil {
		return nil
	}
	cfg := &oauth2.Config{
		ClientID:     t.ClientID,
		ClientSecret: t.ClientSecret,
		Endpoint: oauth2.Endpoint{
			TokenURL: t.TokenURL,
		},
		Scopes: t.Scopes,
	}
	tok := &oauth2.Token{
		AccessToken:  t.AccessToken,
		RefreshToken: t.RefreshToken,
		TokenType:    t.TokenType,
		Expiry:       t.Expiry,
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient,
		&http.Client{Transport: tokenAuthFixer{base: http.DefaultTransport}})
	return cfg.TokenSource(ctx, tok)
}

// storedWithToken returns a copy of base with the token fields replaced by
// tok, preserving the token endpoint and client identity captured at
// authorization time. A refresh response that omits refresh_token (servers
// that do not rotate) keeps the existing one — x/oauth2 already carries it
// forward, but the guard makes the persistence side explicit.
func storedWithToken(base *storedToken, tok *oauth2.Token) *storedToken {
	if base == nil || tok == nil {
		return nil
	}
	out := *base
	out.AccessToken = tok.AccessToken
	out.TokenType = tok.TokenType
	out.Expiry = tok.Expiry
	if tok.RefreshToken != "" {
		out.RefreshToken = tok.RefreshToken
	}
	return &out
}

// savingTokenSourceFromStored rebuilds the token source for a restored token
// AND persists whatever the source refreshes into. This is the restart path:
// without the saving wrapper, a refresh performed during the run mints a new
// access token (and, on servers that rotate, a new refresh token) that never
// reaches disk — so the next launch replays a stale, already-consumed refresh
// token, the server answers invalid_grant, and the user is sent through the
// browser flow again. Returns nil when t is nil (no stored token).
func savingTokenSourceFromStored(name string, t *storedToken) oauth2.TokenSource {
	src := tokenSourceFromStored(t)
	if src == nil {
		return nil
	}
	initial := &oauth2.Token{
		AccessToken:  t.AccessToken,
		RefreshToken: t.RefreshToken,
		TokenType:    t.TokenType,
		Expiry:       t.Expiry,
	}
	return newSavingTokenSource(src, initial, func(persisted *oauth2.Token) error {
		return saveToken(name, storedWithToken(t, persisted))
	})
}

// savingTokenSource wraps an oauth2.TokenSource and calls saveFn with the new
// token each time the access token value changes (the initial grant and every
// refresh). This is the persistence hook: refreshes — which the SDK performs
// transparently when the access token expires — are persisted without us
// intercepting every request. The pattern is from the SDK's
// auth_example_test.go.
type savingTokenSource struct {
	mu           sync.Mutex
	src          oauth2.TokenSource
	saveFn       func(*oauth2.Token) error
	accessToken  string
	refreshToken string
}

// newSavingTokenSource wraps wrapped so that every access-token change is
// persisted via saveFn. initial is the token the wrapper was seeded with (the
// stored token), so a refresh returning the same access token does not trigger
// a redundant save. Pass nil when there is no initial token.
func newSavingTokenSource(wrapped oauth2.TokenSource, initial *oauth2.Token, saveFn func(*oauth2.Token) error) oauth2.TokenSource {
	if wrapped == nil {
		return nil
	}
	if saveFn == nil {
		return wrapped
	}
	var accessToken, refreshToken string
	if initial != nil {
		accessToken = initial.AccessToken
		refreshToken = initial.RefreshToken
	}
	return &savingTokenSource{src: wrapped, saveFn: saveFn, accessToken: accessToken, refreshToken: refreshToken}
}

func (s *savingTokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tok, err := s.src.Token()
	if err != nil {
		return nil, err
	}
	if tok.AccessToken != s.accessToken || (tok.RefreshToken != "" && tok.RefreshToken != s.refreshToken) {
		s.accessToken = tok.AccessToken
		if tok.RefreshToken != "" {
			s.refreshToken = tok.RefreshToken
		}
		_ = s.saveFn(tok)
	}
	return tok, nil
}
