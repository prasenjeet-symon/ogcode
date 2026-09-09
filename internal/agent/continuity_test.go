package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
)

func userMsg(text string) provider.ModelMessage {
	c, _ := json.Marshal(text)
	return provider.ModelMessage{Role: "user", Content: c}
}

func msgText(t *testing.T, m provider.ModelMessage) string {
	t.Helper()
	var s string
	if m.Content != nil {
		if err := json.Unmarshal(m.Content, &s); err != nil {
			t.Fatalf("unmarshal content: %v", err)
		}
	}
	return s
}

// prependPreviousResponse wraps the previous turn's response and attaches it to
// the first user message, so the turn-memory route keeps cross-turn continuity.
func TestPrependPreviousResponse(t *testing.T) {
	msgs := []provider.ModelMessage{userMsg("do the other file too")}
	out := prependPreviousResponse(msgs, "I edited app/nav.tsx and added a logout button.")

	got := msgText(t, out[0])
	if !strings.Contains(got, "<previous_response>") || !strings.Contains(got, "</previous_response>") {
		t.Errorf("expected the response wrapped in <previous_response>, got:\n%s", got)
	}
	if !strings.Contains(got, "I edited app/nav.tsx and added a logout button.") {
		t.Errorf("previous response text missing:\n%s", got)
	}
	if !strings.HasSuffix(got, "do the other file too") {
		t.Errorf("original user message must be preserved after the block:\n%s", got)
	}
	// A distinct tag from the removed legacy graph context.
	if strings.Contains(got, "<prior_context>") {
		t.Errorf("must not reuse the legacy <prior_context> tag:\n%s", got)
	}
}

func TestPrependPreviousResponse_Noops(t *testing.T) {
	// Empty prev → unchanged.
	msgs := []provider.ModelMessage{userMsg("hi")}
	if got := msgText(t, prependPreviousResponse(msgs, "")[0]); got != "hi" {
		t.Errorf("empty prev should be a no-op, got %q", got)
	}

	// Targets the FIRST user message, and only it.
	assistant := provider.ModelMessage{Role: "assistant"}
	two := []provider.ModelMessage{userMsg("first"), assistant, userMsg("second")}
	out := prependPreviousResponse(two, "PREV")
	if !strings.Contains(msgText(t, out[0]), "PREV") {
		t.Error("first user message should carry the previous response")
	}
	if strings.Contains(msgText(t, out[2]), "PREV") {
		t.Error("only the first user message should be modified")
	}
}
