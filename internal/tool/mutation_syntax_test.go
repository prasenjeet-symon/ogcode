package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func edit(t *testing.T, dir, rel, old, new string) Result {
	t.Helper()
	args, _ := json.Marshal(map[string]any{
		"path":  rel,
		"edits": []map[string]any{{"old_string": old, "new_string": new}},
	})
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
	if _, ok := res.Metadata["syntaxPreexisting"]; ok {
		t.Error("metadata marks damage this edit caused as pre-existing")
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
	if res.Metadata["syntaxPreexisting"] != true {
		t.Errorf("metadata syntaxPreexisting = %v, want true so the UI does not blame the edit", res.Metadata["syntaxPreexisting"])
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

// Writing prose, config, or any file type with no parser must never produce a
// note — there is nothing to check, and a warning would be a lie either way.
//
// None of these extensions may have a parser: style.css was here until CSS
// gained one, and data.json, conf.yaml and config.toml until their validators
// landed — each time the write tool grew a verdict it could not honestly give.
func TestWrite_SilentOnUncheckableTypes(t *testing.T) {
	dir := t.TempDir()

	for _, name := range []string{"notes.md", "deploy.sh", "query.sql", "settings.ini"} {
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

// The case the check most needed and missed: an edit that breaks Python's
// indentation. The grammar parses it, so this rests on codemap's own pass.
func TestEdit_ReportsBrokenPythonIndentation(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "app.py"), "def handler(req):\n    if req:\n        return 1\n    return 0\n")

	res := edit(t, dir, "app.py", "    return 0\n", "   return 0\n")

	if !strings.Contains(res.Output, "SYNTAX ERROR") || !strings.Contains(res.Output, "unindent does not match") {
		t.Fatalf("edit that broke the indentation was not reported:\n%s", res.Output)
	}
	if res.Metadata["syntaxOK"] != false {
		t.Errorf("metadata syntaxOK = %v, want false", res.Metadata["syntaxOK"])
	}
}

// Config files are checked too: a JSON edit that drops a comma is damage.
func TestWrite_ReportsBrokenDataFiles(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"package.json":   "{\n  \"name\": \"x\"\n  \"version\": \"1.0.0\"\n}\n",
		"conf.yaml":      "a:\n  b: 1\n c: 2\n",
		"pyproject.toml": "[project]\nname = \"x\n",
	} {
		res := writeFile(t, dir, name, content)
		if !strings.Contains(res.Output, "SYNTAX ERROR") {
			t.Errorf("%s: broken file written without a note:\n%s", name, res.Output)
		}
	}
}

// When a note truncates its list, it points at check_syntax with the path as
// the caller gave it — a bare file name would resolve against the project
// root and check the wrong file, or none.
func TestEdit_NoteHintUsesTheCallersPath(t *testing.T) {
	dir := t.TempDir()
	rel := filepath.Join("internal", "pkg", "mess.go")
	mustWriteFile(t, filepath.Join(dir, rel), "package demo\n\nfunc Target() error {\n\treturn nil\n}\n")

	res := edit(t, dir, rel, "return nil", "}\n"+strings.Repeat("func a( {}\n", 30))

	if want := "check_syntax(" + strconv.Quote(rel) + ")"; !strings.Contains(res.Output, want) {
		t.Errorf("note does not point at %s:\n%s", want, res.Output)
	}
	if !strings.Contains(res.Output, "20+ syntax errors") {
		t.Errorf("note does not mark its count as capped:\n%s", res.Output)
	}
}
