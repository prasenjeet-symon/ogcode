package agent

import (
	"context"
	"strings"
	"testing"
)

func TestServerURLPrompt(t *testing.T) {
	if got := serverURLPrompt(""); got != "" {
		t.Errorf("empty origin should produce no section, got %q", got)
	}

	prompt := serverURLPrompt("http://127.0.0.1:9595")
	if !strings.Contains(prompt, "http://127.0.0.1:9595") {
		t.Error("expected the origin in the section")
	}
	if !strings.Contains(prompt, "/public/<name>") {
		t.Error("expected the /public/ path form")
	}
	// A live service is reached at its own hostname on the same origin, not on
	// a path of this server's host.
	if !strings.Contains(prompt, PreviewURL("http://127.0.0.1:9595", 4321)) {
		t.Error("expected a worked preview-hostname example")
	}
}

func TestServerURLContextRoundTrip(t *testing.T) {
	if got := ServerURLFromContext(context.Background()); got != "" {
		t.Errorf("unset context should yield empty origin, got %q", got)
	}

	ctx := WithServerURL(context.Background(), "https://panel.example:8443")
	if got := ServerURLFromContext(ctx); got != "https://panel.example:8443" {
		t.Errorf("expected the carried origin, got %q", got)
	}
}

// TestServerURLPrompt_RemoteNote pins the warning a remote user needs: reached
// at a non-loopback address while previews use the loopback-only *.localhost
// domain, every preview URL resolves to the user's OWN machine, so the agent is
// told to say so. Reached on loopback, or with a real preview domain, the note
// would be false and is left out.
func TestServerURLPrompt_RemoteNote(t *testing.T) {
	t.Setenv(previewDomainEnv, "")
	if p := serverURLPrompt("http://192.168.1.10:9595"); !strings.Contains(p, "OGCODE_PREVIEW_DOMAIN") {
		t.Error("a remote origin with the *.localhost domain should carry the remote note")
	}
	if p := serverURLPrompt("http://localhost:9595"); strings.Contains(p, "OGCODE_PREVIEW_DOMAIN") {
		t.Error("a loopback origin must not carry the remote note")
	}
	t.Setenv(previewDomainEnv, "preview.example.com")
	if p := serverURLPrompt("https://ogcode.example.com"); strings.Contains(p, "OGCODE_PREVIEW_DOMAIN") {
		t.Error("a real preview domain must not carry the remote note")
	}
}

// TestPreviewServingPrompt_PublishRule pins that the agent is told a preview
// hostname serves only published ports — so it writes the URL down, checks the
// service on loopback instead, and never offers this server's own port.
func TestPreviewServingPrompt_PublishRule(t *testing.T) {
	p := previewServingPrompt("preview.localhost")
	for _, want := range []string{"published", "403", "http://127.0.0.1:<port>/", "own port is never served"} {
		if !strings.Contains(p, want) {
			t.Errorf("preview section should mention %q", want)
		}
	}
}
