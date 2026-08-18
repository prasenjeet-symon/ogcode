package tool

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func fileMap(t *testing.T, dir, rel string) Result {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"path": rel})
	res, err := FileMapTool{}.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return res
}

// The ranges file_map prints must be the ranges read accepts, with no
// arithmetic in between. This is the contract the two tools share.
func TestFileMapTool_RangesFeedReadDirectly(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "demo.go"), `package demo

// Target does the thing.
func Target() error {
	return nil
}
`)

	out := fileMap(t, dir, "demo.go").Output
	if !strings.Contains(out, "3-6") {
		t.Fatalf("expected Target at 3-6 (doc comment included):\n%s", out)
	}

	// Feed that range straight back to read.
	args, _ := json.Marshal(map[string]any{"path": "demo.go", "start_line": 3, "end_line": 6})
	res, err := ReadTool{}.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(res.Output, "// Target does the thing.") {
		t.Errorf("range lost the doc comment:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "return nil") {
		t.Errorf("range lost the body:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "package demo") {
		t.Errorf("range reached above the declaration:\n%s", res.Output)
	}
}

// These are properties of the file, not failures of the call, so they come back
// as guidance in the output rather than as tool errors.
func TestFileMapTool_ReportsUnmappableFilesAsOutput(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "blob.bin"), "abc\x00def")

	cases := []struct {
		name, rel, want string
	}{
		{"missing", "nope.go", "does not exist"},
		{"binary", "blob.bin", "binary file"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := fileMap(t, dir, c.rel)
			if !strings.Contains(res.Output, c.want) {
				t.Errorf("output = %q, want it to mention %q", res.Output, c.want)
			}
		})
	}
}
