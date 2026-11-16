package agent

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestDumpFullSystemPrompt is an inspection harness, not an assertion: it writes
// the fully assembled system-prompt entries — entry [0] plus every dynamic
// section buildSystemPromptEntries appends — to a file, so the prompt actually
// wired to a model can be read. It skips unless OGCODE_DUMP_PROMPT is set,
// because the dump depends on the project directory and would otherwise differ
// on every machine.
//
//	OGCODE_DUMP_PROMPT=1 OGCODE_DUMP_DIR=$PWD \
//	OGCODE_DUMP_SESSION=<session id> OGCODE_DUMP_OUT=$PWD/public/system-prompt-full.md \
//	  go test ./internal/agent -run TestDumpFullSystemPrompt -v
//
// It lives in the package rather than in .ogcode/scripts because the builders
// it calls are unexported; BuildAgent.System alone (no dynamic entries) is
// available to a standalone program through .ogcode/scripts/dump_prompt.go.
func TestDumpFullSystemPrompt(t *testing.T) {
	if os.Getenv("OGCODE_DUMP_PROMPT") == "" {
		t.Skip("set OGCODE_DUMP_PROMPT=1 to dump the assembled system prompt")
	}
	dir := os.Getenv("OGCODE_DUMP_DIR")
	out := os.Getenv("OGCODE_DUMP_OUT")
	if dir == "" || out == "" {
		t.Fatal("OGCODE_DUMP_DIR and OGCODE_DUMP_OUT are required")
	}

	agents := []Agent{BuildAgent, NoteAgent, SubagentAgent}
	var b strings.Builder
	for i, a := range agents {
		if i > 0 {
			b.WriteString("\n\n<!-- ===== agent: " + a.ID + " ===== -->\n\n")
		}
		b.WriteString("# agent: " + a.ID + "\n\n")
		entries := buildSystemPromptEntries(a, dir, true,
			discoverMemoryFiles(dir, os.Getenv("OGCODE_DUMP_SESSION")), 1710, 989, "anthropic", 1703, true, true)
		for j, e := range entries {
			if j > 0 {
				b.WriteString(fmt.Sprintf("\n\n<!-- system entry %d -->\n\n", j))
			}
			b.WriteString(e)
		}
	}
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s (%d bytes)", out, b.Len())
}
