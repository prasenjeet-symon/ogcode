package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/session"
)

func digestTextPart(text string) session.Part {
	data, _ := json.Marshal(session.TextPartData{Text: text})
	return session.Part{Type: session.PartText, Data: data}
}

func digestToolPart(name, input, output string) session.Part {
	out := output
	data, _ := json.Marshal(session.ToolPartData{
		Tool: name,
		State: session.ToolState{
			Status: session.ToolCompleted,
			Input:  json.RawMessage(input),
			Output: &out,
		},
	})
	return session.Part{Type: session.PartTool, Data: data}
}

// buildTurnDigest must carry tool-call inputs and the final response, but never
// tool results — that omission is the whole point of the feature.
func TestBuildTurnDigestExcludesResults(t *testing.T) {
	messages := []*session.MessageWithParts{
		{Info: session.MessageInfo{Role: session.RoleUser}, Parts: []session.Part{digestTextPart("Add a logout button")}},
		{Info: session.MessageInfo{Role: session.RoleAssistant}, Parts: []session.Part{
			digestToolPart("read", `{"path":"app/nav.tsx"}`, "SECRET_FILE_CONTENTS_SHOULD_NOT_APPEAR"),
		}},
		{Info: session.MessageInfo{Role: session.RoleAssistant}, Parts: []session.Part{
			digestTextPart("Added the logout button to app/nav.tsx."),
		}},
	}

	digest := buildTurnDigest(messages, 0, "Add a logout button")

	if !strings.Contains(digest, "Add a logout button") {
		t.Errorf("digest missing user request:\n%s", digest)
	}
	if !strings.Contains(digest, "read") || !strings.Contains(digest, `"path":"app/nav.tsx"`) {
		t.Errorf("digest missing tool call name/input:\n%s", digest)
	}
	if !strings.Contains(digest, "Added the logout button to app/nav.tsx.") {
		t.Errorf("digest missing final response:\n%s", digest)
	}
	if strings.Contains(digest, "SECRET_FILE_CONTENTS_SHOULD_NOT_APPEAR") {
		t.Errorf("digest leaked a tool RESULT — results must be excluded:\n%s", digest)
	}
}

func TestBuildTurnDigestNoToolCalls(t *testing.T) {
	messages := []*session.MessageWithParts{
		{Info: session.MessageInfo{Role: session.RoleUser}, Parts: []session.Part{digestTextPart("What is 2+2?")}},
		{Info: session.MessageInfo{Role: session.RoleAssistant}, Parts: []session.Part{digestTextPart("4.")}},
	}
	digest := buildTurnDigest(messages, 0, "What is 2+2?")
	if !strings.Contains(digest, "(no tool calls this turn)") {
		t.Errorf("expected no-tool-calls marker:\n%s", digest)
	}
	if !strings.Contains(digest, "4.") {
		t.Errorf("digest missing final response:\n%s", digest)
	}
}

func TestTruncateForDigest(t *testing.T) {
	long := strings.Repeat("x", digestInputCap+500)
	got := truncateForDigest(long)
	if len(got) >= len(long) {
		t.Fatalf("expected truncation, got len %d", len(got))
	}
	if !strings.Contains(got, "+500 chars") {
		t.Errorf("expected truncation note, got tail %q", got[len(got)-20:])
	}
	short := "short input"
	if truncateForDigest(short) != short {
		t.Errorf("short input should be unchanged")
	}
}

func TestTurnMemorableSession(t *testing.T) {
	memorable := []string{"", "build", "plan", "task"}
	for _, s := range memorable {
		if !turnMemorableSession(s) {
			t.Errorf("session type %q should be memorable", s)
		}
	}
	skip := []string{"memory-recall", "subagent", "note", "index", "search", "breakdown"}
	for _, s := range skip {
		if turnMemorableSession(s) {
			t.Errorf("session type %q should be skipped", s)
		}
	}
}

func TestFirstMarkdownH1(t *testing.T) {
	md := "---\ntitle: x\n---\n\n#Notquite\n\n# Real Title\n\n## Sub\n"
	if got := firstMarkdownH1(md); got != "Real Title" {
		t.Errorf("firstMarkdownH1 = %q, want %q", got, "Real Title")
	}
	if got := firstMarkdownH1("no heading here"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}
