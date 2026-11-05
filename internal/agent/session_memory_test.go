package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/memfile"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

func TestSessionMemoryKey_TeammatesShareTheRootFile(t *testing.T) {
	root := session.SessionID("ses_root")
	teammate := session.SessionID("ses_child")

	resolver := func(id session.SessionID) (session.SessionID, string) {
		if id == teammate {
			return root, "parent"
		}
		return "", ""
	}

	if got := sessionMemoryKey(teammate, resolver); got != string(root) {
		t.Errorf("a teammate must resolve to its root conversation's key; got %q, want %q", got, string(root))
	}
	if got := sessionMemoryKey(teammate, nil); got != string(teammate) {
		t.Errorf("a nil resolver falls back to the session's own id; got %q", got)
	}
	// A resolver that knows the session but reports no root (already the root)
	// must not produce an empty key.
	if got := sessionMemoryKey(root, resolver); got != string(root) {
		t.Errorf("a root session's key is its own id; got %q", got)
	}
}

func TestSessionMemoryPrompt_PointsAtTheFile(t *testing.T) {
	rel := ".ogcode/memory/sessions/ses123.md"
	written := sessionMemoryPrompt(true, rel)

	if !strings.Contains(written, "`"+rel+"`") {
		t.Errorf("the file's path must be named:\n%s", written)
	}
	for _, want := range []string{
		"Check it again whenever a turn continues earlier work",
		"create it with the write tool",
		"### How to maintain session memory",
		"reads it on demand",
		memfile.DeepSearchHeading,
	} {
		if !strings.Contains(written, want) {
			t.Errorf("writable session memory section missing %q", want)
		}
	}
}

// The split decides which of the two stores a fact lands in. Without it every
// fact defaults to whichever file was written last.
func TestSessionMemoryPrompt_TeachesTheProjectSessionSplit(t *testing.T) {
	rel := ".ogcode/memory/sessions/ses123.md"
	for _, canWrite := range []bool{true, false} {
		p := sessionMemoryPrompt(canWrite, rel)
		for _, want := range []string{"THIS conversation", "Project level", "Session level",
			"promote", "exactly one place", "recall question"} {
			if !strings.Contains(p, want) {
				t.Errorf("sessionMemoryPrompt(canWrite=%v) must teach the split rule %q", canWrite, want)
			}
		}
		// The note agent is project-scoped but holds no recall tool: the tool
		// name stays in its own HasTool-gated block, never in this section.
		if strings.Contains(p, "memory_recall") {
			t.Errorf("sessionMemoryPrompt names memory_recall, which the note agent does not hold:\n%s", p)
		}
	}
}

// A read-only agent cannot create or edit the file, so it must not be handed
// write instructions it cannot follow.
func TestSessionMemoryPrompt_ReadOnlyForbidsWriting(t *testing.T) {
	rel := ".ogcode/memory/sessions/ses123.md"
	readonly := sessionMemoryPrompt(false, rel)
	if !strings.Contains(readonly, "### How to use session memory") ||
		!strings.Contains(readonly, "Do not modify the session memory file") {
		t.Errorf("read-only branch must forbid editing:\n%s", readonly)
	}
	if strings.Contains(readonly, "create it with the write tool") {
		t.Errorf("read-only branch tells the agent to create a file it cannot write:\n%s", readonly)
	}
	if !strings.Contains(readonly, "a future turn with write access can create it") {
		t.Errorf("read-only branch must still say how the file comes into existence:\n%s", readonly)
	}
}

// The file is a pointer: whatever the agent wrote into it must never be pasted
// into the prompt, or every step re-sends a file that changes turn by turn.
func TestStaticSystemPrompt_SessionMemoryIsAPointer(t *testing.T) {
	dir := t.TempDir()
	const id = "ses_scope"
	path := memfile.SessionMemoryPath(dir, id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("## Task — SENTINEL-SESSION-BODY\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	build := staticSystemPrompt(BuildAgent, dir, false, discoverMemoryFiles(dir, id), "anthropic", true, true)
	if !strings.Contains(build, "## Session memory — this conversation") {
		t.Error("a project-scoped agent must receive the session memory pointer")
	}
	if want := memfile.SessionMemoryRelPath(dir, id); !strings.Contains(build, want) {
		t.Errorf("the prompt must name the file at %q", want)
	}
	if strings.Contains(build, "SENTINEL-SESSION-BODY") {
		t.Error("static system prompt inlined the body of the session memory file")
	}

	utility := staticSystemPrompt(SearchAgent, dir, false, discoverMemoryFiles(dir, id), "", true, true)
	if strings.Contains(utility, "Session memory") {
		t.Error("a non-project-scoped utility agent must not receive session memory")
	}
}
