package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func edit(t *testing.T, dir, rel, old, new string) Result {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"path": rel, "old_string": old, "new_string": new})
	res, err := EditTool{}.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	return res
}

func writeFile(t *testing.T, dir, rel, content string) Result {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"path": rel, "content": content})
	res, err := WriteTool{}.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	return res
}

const validGo = `package demo

func Target() error {
	return nil
}
`

// The case the wiring exists for: an edit that drops a brace reports itself,
// with no separate call needed.
func TestEdit_ReportsDamageItCaused(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "demo.go"), validGo)

	res := edit(t, dir, "demo.go", "\treturn nil\n}", "\treturn nil")

	if !strings.Contains(res.Output, "SYNTAX ERROR") {
		t.Fatalf("edit did not report the damage it caused:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "parsed cleanly before") {
		t.Errorf("note does not pin the damage on this edit:\n%s", res.Output)
	}
	if res.Metadata["syntaxOK"] != false {
		t.Errorf("metadata syntaxOK = %v, want false", res.Metadata["syntaxOK"])
	}
	// The edit itself still succeeded — the file on disk holds the new content.
	// Reporting the breakage must not look like the write was rolled back.
	if !strings.HasPrefix(res.Output, "Edited ") {
		t.Errorf("output no longer leads with the edit result:\n%s", res.Output)
	}
}

// The common case has to stay silent. A note on every successful edit is noise
// the agent would learn to skip, which would cost the warning its weight.
func TestEdit_SilentOnACleanChange(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "demo.go"), validGo)

	res := edit(t, dir, "demo.go", "return nil", "return os.ErrClosed")

	if strings.Contains(res.Output, "SYNTAX") {
		t.Errorf("clean edit produced a syntax note:\n%s", res.Output)
	}
	if res.Metadata["syntaxOK"] != true {
		t.Errorf("metadata syntaxOK = %v, want true", res.Metadata["syntaxOK"])
	}
}

// An edit to a file that was already broken must not be blamed for it. An agent
// that sees the warning fire on damage it did not cause stops reading it.
func TestEdit_DoesNotBlameItselfForPriorDamage(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "broken.go"), "package demo\n\nfunc Target() error {\n\treturn nil\n")

	res := edit(t, dir, "broken.go", "return nil", "return os.ErrClosed")

	if !strings.Contains(res.Output, "SYNTAX NOTE") {
		t.Fatalf("expected the softer note for pre-existing damage:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "parsed cleanly before") {
		t.Errorf("note blames this edit for damage that predates it:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "also had errors before") {
		t.Errorf("note does not say the damage predates the edit:\n%s", res.Output)
	}
}

// A new file has no baseline, so every error in it belongs to the write.
func TestWrite_ReportsErrorsInANewFile(t *testing.T) {
	dir := t.TempDir()

	res := writeFile(t, dir, "fresh.py", "def handler(req:\n    return None\n")

	if !strings.Contains(res.Output, "SYNTAX ERROR") {
		t.Fatalf("write did not report a broken new file:\n%s", res.Output)
	}
	if !strings.HasPrefix(res.Output, "Created ") {
		t.Errorf("output no longer leads with the write result:\n%s", res.Output)
	}
	// The metadata the UI already depends on must survive the append.
	if res.Metadata["created"] != true {
		t.Errorf("metadata created = %v, want true", res.Metadata["created"])
	}
}

func TestWrite_SilentOnAValidFile(t *testing.T) {
	dir := t.TempDir()

	res := writeFile(t, dir, "fresh.go", validGo)

	if strings.Contains(res.Output, "SYNTAX") {
		t.Errorf("valid new file produced a syntax note:\n%s", res.Output)
	}
}

// Writing prose, config, or any file type with no grammar must never produce a
// note — there is nothing to check, and a warning would be a lie either way.
//
// None of these extensions may be a registered grammar: style.css was here
// until CSS gained one, and the write tool grew a syntax verdict it cannot
// honestly give.
func TestWrite_SilentOnUncheckableTypes(t *testing.T) {
	dir := t.TempDir()

	for _, name := range []string{"notes.md", "conf.yaml", "data.json", "config.toml"} {
		res := writeFile(t, dir, name, "key: [unclosed\n{{{ not valid anything\n")
		if strings.Contains(res.Output, "SYNTAX") {
			t.Errorf("%s: unparseable-by-no-grammar file produced a note:\n%s", name, res.Output)
		}
		if _, ok := res.Metadata["syntaxOK"]; ok {
			t.Errorf("%s: metadata claims a syntax verdict for an unchecked file", name)
		}
	}
}

// The note is a signal to look, not the report. A file broken in many places
// must not push its whole diagnostic list into the edit result.
func TestEdit_NoteCapsItsDiagnosticList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mess.go")
	mustWriteFile(t, path, "package demo\n\nfunc Target() error {\n\treturn nil\n}\n")

	res := edit(t, dir, "mess.go", "return nil", "}\n"+strings.Repeat("func a( {}\n", 30))

	if !strings.Contains(res.Output, "SYNTAX ERROR") {
		t.Fatalf("expected a syntax note:\n%s", res.Output)
	}
	if n := strings.Count(res.Output, " | "); n > noteDiagnostics {
		t.Errorf("note listed %d source lines, want at most %d", n, noteDiagnostics)
	}
	if !strings.Contains(res.Output, "check_syntax") {
		t.Errorf("truncated note does not point at the full report:\n%s", res.Output)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file missing after edit: %v", err)
	}
}
