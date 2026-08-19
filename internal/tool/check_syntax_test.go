package tool

import (
	"context"
	"encoding/json"
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
	args, _ := json.Marshal(map[string]string{
		"path":       "demo.go",
		"old_string": "\treturn nil\n}",
		"new_string": "\treturn nil",
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
	mustWriteFile(t, filepath.Join(dir, "conf.yaml"), "key: [unclosed\n")

	res := checkSyntax(t, dir, "conf.yaml")
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
