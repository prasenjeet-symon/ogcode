package mcp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestTokenStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OGCODE_MCP_TOKEN_DIR", dir)

	in := &storedToken{
		AccessToken:  "access-123",
		RefreshToken: "refresh-456",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour).UTC(),
		TokenURL:     "https://example.com/token",
		ClientID:     "client-abc",
		Scopes:       []string{"read", "write"},
	}
	if err := saveToken("calcom", in); err != nil {
		t.Fatalf("saveToken: %v", err)
	}

	// File must be 0600 and live under the token dir.
	path := filepath.Join(dir, "calcom.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perms = %o, want 0600", perm)
	}

	out, err := loadToken("calcom")
	if err != nil {
		t.Fatalf("loadToken: %v", err)
	}
	if out == nil {
		t.Fatal("loadToken returned nil")
	}
	if out.AccessToken != in.AccessToken || out.RefreshToken != in.RefreshToken ||
		out.TokenURL != in.TokenURL || out.ClientID != in.ClientID ||
		out.TokenType != in.TokenType {
		t.Errorf("round-trip mismatch:\ngot  %+v\nwant %+v", out, in)
	}
}

func TestTokenStore_MissingFileReturnsNil(t *testing.T) {
	t.Setenv("OGCODE_MCP_TOKEN_DIR", t.TempDir())
	got, err := loadToken("never-existed")
	if err != nil {
		t.Fatalf("loadToken err = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("loadToken = %+v, want nil", got)
	}
}

func TestTokenStore_CorruptFileReturnsNil(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OGCODE_MCP_TOKEN_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadToken("broken")
	if err != nil {
		t.Fatalf("loadToken err = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("loadToken = %+v, want nil for corrupt file", got)
	}
}

func TestSavingTokenSource_PersistsOnRefresh(t *testing.T) {
	// Sequence: first (change from "" → save), second (change from first →
	// save), second (repeat → no save). So 2 saves.
	fake := &fakeTokenSource{tokens: []*oauth2.Token{
		{AccessToken: "first", TokenType: "Bearer"},
		{AccessToken: "second", TokenType: "Bearer"},
		{AccessToken: "second", TokenType: "Bearer"}, // same value: no save
	}}

	var saved []*oauth2.Token
	wrapped := newSavingTokenSource(fake, nil, func(tok *oauth2.Token) error {
		saved = append(saved, tok)
		return nil
	})

	for i := 0; i < 3; i++ {
		if _, err := wrapped.Token(); err != nil {
			t.Fatalf("Token() call %d: %v", i, err)
		}
	}

	if len(saved) != 2 {
		t.Fatalf("saveFn called %d times, want 2 (first grant + first refresh)", len(saved))
	}
	if saved[0].AccessToken != "first" {
		t.Errorf("saved[0].AccessToken = %q, want %q", saved[0].AccessToken, "first")
	}
	if saved[1].AccessToken != "second" {
		t.Errorf("saved[1].AccessToken = %q, want %q", saved[1].AccessToken, "second")
	}
	// calls should advance the underlying source each Token().
	if fake.calls != 3 {
		t.Errorf("underlying calls = %d, want 3", fake.calls)
	}
}

type fakeTokenSource struct {
	tokens []*oauth2.Token
	calls  int
}

func (f *fakeTokenSource) Token() (*oauth2.Token, error) {
	f.calls++
	if len(f.tokens) == 0 {
		return nil, nil
	}
	tok := f.tokens[0]
	f.tokens = f.tokens[1:]
	return tok, nil
}

func TestTokenSourceFromStored_NilWhenNoToken(t *testing.T) {
	if ts := tokenSourceFromStored(nil); ts != nil {
		t.Errorf("tokenSourceFromStored(nil) = %v, want nil", ts)
	}
}

func TestTokenSourceFromStored_RebuildsSource(t *testing.T) {
	st := &storedToken{
		AccessToken:  "a1",
		RefreshToken: "r1",
		TokenType:    "Bearer",
		TokenURL:     "https://example.com/token",
		ClientID:     "c1",
		Scopes:       []string{"s1"},
	}
	ts := tokenSourceFromStored(st)
	if ts == nil {
		t.Fatal("expected non-nil token source")
	}
	tok, err := ts.Token()
	if err != nil {
		t.Fatalf("Token(): %v", err)
	}
	if tok.AccessToken != "a1" {
		t.Errorf("AccessToken = %q, want %q", tok.AccessToken, "a1")
	}
	if tok.RefreshToken != "r1" {
		t.Errorf("RefreshToken = %q, want %q", tok.RefreshToken, "r1")
	}
}

// TestSavingTokenSourceFromStored_PersistsRefresh is the regression test for
// the re-authorize-on-every-launch bug: a restored token source refreshed
// during the run but never wrote the new tokens back, so the next launch
// replayed an already-consumed refresh token and fell through to the browser
// flow. The restored source must persist refreshes exactly like the
// post-exchange one does.
func TestSavingTokenSourceFromStored_PersistsRefresh(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OGCODE_MCP_TOKEN_DIR", dir)

	// A token endpoint that rotates the refresh token on every refresh —
	// the behaviour that turns "not persisted" into "must re-authorize".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if got := r.Form.Get("grant_type"); got != "refresh_token" {
			t.Errorf("grant_type = %q, want refresh_token", got)
		}
		if got := r.Form.Get("refresh_token"); got != "refresh-old" {
			t.Errorf("refresh_token = %q, want refresh-old", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access-new","refresh_token":"refresh-new",` +
			`"token_type":"bearer","expires_in":3600}`))
	}))
	defer srv.Close()

	// Expired access token on disk: using it forces a refresh.
	stored := &storedToken{
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
		TokenType:    "bearer",
		Expiry:       time.Now().Add(-time.Minute),
		TokenURL:     srv.URL,
		ClientID:     "client-abc",
	}
	if err := saveToken("calcom", stored); err != nil {
		t.Fatalf("saveToken: %v", err)
	}

	ts := savingTokenSourceFromStored("calcom", stored)
	if ts == nil {
		t.Fatal("savingTokenSourceFromStored returned nil")
	}
	tok, err := ts.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok.AccessToken != "access-new" {
		t.Errorf("access token = %q, want access-new", tok.AccessToken)
	}

	// The refreshed pair must be on disk, or the next launch re-authorizes.
	out, err := loadToken("calcom")
	if err != nil || out == nil {
		t.Fatalf("loadToken = %+v, %v", out, err)
	}
	if out.AccessToken != "access-new" {
		t.Errorf("persisted access token = %q, want access-new", out.AccessToken)
	}
	if out.RefreshToken != "refresh-new" {
		t.Errorf("persisted refresh token = %q, want refresh-new", out.RefreshToken)
	}
	// Client identity and endpoint must survive the rewrite so the *next*
	// refresh still knows where to go and as whom.
	if out.TokenURL != srv.URL || out.ClientID != "client-abc" {
		t.Errorf("persisted config lost: token_url=%q client_id=%q", out.TokenURL, out.ClientID)
	}
}

// TestSavingTokenSourceFromStored_NilToken pins that no stored token yields no
// source, which is what makes the SDK trigger Authorize on first run.
func TestSavingTokenSourceFromStored_NilToken(t *testing.T) {
	if ts := savingTokenSourceFromStored("calcom", nil); ts != nil {
		t.Errorf("savingTokenSourceFromStored(nil) = %v, want nil", ts)
	}
}
