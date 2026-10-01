package logging

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestScrub(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Authorization: Bearer abcdefghijklmnop", "Authorization: Bearer [REDACTED]"},
		{"GET https://generativelanguage.googleapis.com/v1beta/models?key=AIzaSyA-123", "GET https://generativelanguage.googleapis.com/v1beta/models?key=[REDACTED]"},
		{"/api/mcp/oauth/callback?code=4/0Ab_xyz&state=s1", "/api/mcp/oauth/callback?code=[REDACTED]&state=s1"},
		{"dial https://bob:hunter2@proxy.example.com:8080", "dial https://[REDACTED]@proxy.example.com:8080"},
		{"api_key=sk-live-123 and more", "api_key=[REDACTED] and more"},
		{`{"error":"bad","api_key":"abc123"}`, `{"error":"bad","api_key":"[REDACTED]"}`},
		{"invalid x-api-key: sk-ant-api03-AAAAAAAAAAAAAAAA", "invalid x-api-key: [REDACTED]"},
		{"key sk-proj-ABCDEFGHIJKLMNOPQRSTUVWX leaked", "key [REDACTED] leaked"},
		{"token ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", "token [REDACTED]"},
		{"jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N", "jwt [REDACTED]"},
		{"tavily tvly-dev-ABCDEFGHIJKLMNOP1234", "tavily [REDACTED]"},
		// Left alone: counts, prose, and short query names outside a URL.
		{"maxTokens=4096 inputTokens=12", "maxTokens=4096 inputTokens=12"},
		{"exit status code=1", "exit status code=1"},
		{"unexpected token: }", "unexpected token: }"},
		{"https://example.com/docs?page=2", "https://example.com/docs?page=2"},
	} {
		if got := Scrub(tc.in); got != tc.want {
			t.Errorf("Scrub(%q)\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

func TestSecretKeys(t *testing.T) {
	for key, want := range map[string]bool{
		"apiKey": true, "api_key": true, "OPENAI_API_KEY": true, "token": true,
		"refreshToken": true, "client-secret": true, "password": true,
		"Authorization": true, "cookie": true, "auth": true,
		"tokens": false, "inputTokens": false, "session": false, "author": false,
		"key": false, "path": false,
	} {
		if got := isSecretKey(key); got != want {
			t.Errorf("isSecretKey(%q) = %v, want %v", key, got, want)
		}
	}
}

// The file handler redacts attributes by name and by content, in the message,
// in errors, inside groups and in attributes bound with With.
func TestHandlerRedacts(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(fileHandler(&buf, Options{Level: slog.LevelInfo, Format: "text"}))
	log.With("apiKey", "plain-secret-value").Info("calling https://u:p@host/x?key=AIzaSecret",
		"token", "t0ps3cret",
		"tokens", 1234,
		"err", errors.New("401: Bearer abcdefghijklmnopqrstuvwxyz rejected"),
		slog.Group("req", "password", "pw123", "url", "https://h/?access_token=zzz"),
	)
	out := buf.String()
	for _, leak := range []string{"plain-secret-value", "u:p@", "AIzaSecret", "t0ps3cret", "abcdefghijklmnop", "pw123", "zzz"} {
		if strings.Contains(out, leak) {
			t.Errorf("log line leaks %q:\n%s", leak, out)
		}
	}
	if !strings.Contains(out, "tokens=1234") {
		t.Errorf("a token count was redacted:\n%s", out)
	}
}
