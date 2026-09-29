package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func checkSyntax(t *testing.T, dir, rel string) Result {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"path": rel})
	res, err := CheckSyntaxTool{}.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return res
}

// The whole point of the tool is the round trip: edit a file, check it, and be
// told the truth about what the edit left behind.
func TestCheckSyntaxTool_CatchesABrokenEdit(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "demo.go"), `package demo

func Target() error {
	return nil
}
`)

	if res := checkSyntax(t, dir, "demo.go"); !strings.HasPrefix(res.Output, "OK:") {
		t.Fatalf("clean file did not pass:\n%s", res.Output)
	}

	// An edit that drops the closing brace of the body — the classic damage.
	args, _ := json.Marshal(map[string]any{
		"path": "demo.go",
		"edits": []map[string]any{
			{"old_string": "\treturn nil\n}", "new_string": "\treturn nil"},
		},
	})
	if _, err := (EditTool{}).Execute(context.Background(), args, Context{SessionDir: dir}); err != nil {
		t.Fatalf("edit: %v", err)
	}

	res := checkSyntax(t, dir, "demo.go")
	if !strings.HasPrefix(res.Output, "SYNTAX ERRORS:") {
		t.Fatalf("broken file reported as fine:\n%s", res.Output)
	}
	if res.Metadata["ok"] != false {
		t.Errorf("metadata ok = %v, want false", res.Metadata["ok"])
	}
	if !strings.Contains(res.Title, "error") {
		t.Errorf("Title = %q, want the verdict visible without opening the result", res.Title)
	}
}

// A file type with no grammar must not read as a pass — not in the body, and
// not in the title either, which is all the agent sees when the call is
// collapsed.
func TestCheckSyntaxTool_UnknownTypeDoesNotReadAsPass(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "deploy.sh"), "if [ -z \"$X\" ]; then\n")

	res := checkSyntax(t, dir, "deploy.sh")
	if strings.Contains(res.Title, "OK") {
		t.Errorf("Title = %q claims OK for an unchecked file", res.Title)
	}
	if res.Metadata["ok"] != false {
		t.Errorf("metadata ok = %v, want false", res.Metadata["ok"])
	}
	if !strings.Contains(res.Output, "not a passing result") {
		t.Errorf("output does not warn that nothing was verified:\n%s", res.Output)
	}
}

// A missing path is an ordinary thing to hit right after an edit went to the
// wrong place. It comes back as output the agent can act on, not a tool error.
func TestCheckSyntaxTool_MissingFileIsOutputNotError(t *testing.T) {
	res := checkSyntax(t, t.TempDir(), "nope.go")
	if !strings.Contains(res.Output, "does not exist") {
		t.Errorf("output = %q, want it to say the file is missing", res.Output)
	}
}

// A directory is an ordinary wrong turn, answered as output with what to do
// instead, not as a tool failure.
func TestCheckSyntaxTool_DirectoryIsOutputNotError(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := checkSyntax(t, dir, "pkg")
	if !strings.Contains(res.Output, "is a directory") {
		t.Errorf("output = %q, want it to say the path is a directory", res.Output)
	}
}

// A capped count reads as capped in the title the agent sees collapsed, and
// the metadata says the list stops short.
func TestCheckSyntaxTool_CappedCountSaysSo(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "mess.go"), "package p\n"+strings.Repeat("func a( {}\n", 60))

	res := checkSyntax(t, dir, "mess.go")
	if !strings.Contains(res.Title, "20+ syntax errors") {
		t.Errorf("Title = %q, want the capped count marked with +", res.Title)
	}
	if res.Metadata["errorsTruncated"] != true {
		t.Errorf("metadata errorsTruncated = %v, want true", res.Metadata["errorsTruncated"])
	}

	mustWriteFile(t, filepath.Join(dir, "one.go"), "package p\n\nfunc f( {}\n")
	if res := checkSyntax(t, dir, "one.go"); !strings.HasSuffix(res.Title, "— 1 syntax error") {
		t.Errorf("Title = %q, want the singular for one error", res.Title)
	}
}
