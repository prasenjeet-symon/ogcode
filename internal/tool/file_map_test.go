package tool

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/codemap"
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
	mustWriteFile(t, filepath.Join(dir, "pkg", "a.go"), "package pkg\n")

	cases := []struct {
		name, rel, want string
	}{
		{"missing", "nope.go", "does not exist"},
		{"binary", "blob.bin", "binary file"},
		{"directory", "pkg", "is a directory"},
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

// The description tells the model which file types get an exact range and which
// get an approximate one. That claim is only true while it matches the grammar
// registry, and a hand-maintained list silently stops matching the moment a
// grammar is added — which is exactly how the same list in AGENT.md came to
// claim Go and TypeScript were the only parsed languages long after Python,
// Rust, Java, PHP and Swift had landed.
func TestFileMapDescription_NamesEveryParsedLanguage(t *testing.T) {
	desc := strings.ToLower(FileMapTool{}.Description())
	for _, name := range codemap.LanguageNames() {
		if !strings.Contains(desc, strings.ToLower(name)) {
			t.Errorf("file_map description does not mention %q, a language with a real grammar; "+
				"the model will treat its exact ranges as heuristic", name)
		}
	}
}

// The map cannot show what it never captures, so the omission has to be stated.
func TestFileMapDescription_StatesWhatIsNotListed(t *testing.T) {
	desc := FileMapTool{}.Description()
	for _, want := range []string{"struct fields", "class properties", "local variables"} {
		if !strings.Contains(desc, want) {
			t.Errorf("file_map description does not say %q are omitted", want)
		}
	}
}

// start_line/end_line map one region: the declarations reaching into it, with
// the ones enclosing it, and nothing else.
func TestFileMapTool_RangeMapsOneRegion(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "demo.go"), `package demo

func First() {}

type Server struct{}

func (s *Server) Handle() {
	println("handle")
}

func Last() {}
`)
	args, _ := json.Marshal(map[string]any{"path": "demo.go", "start_line": 7, "end_line": 8})
	res, err := FileMapTool{}.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res.Output, "map of lines 7-8") || !strings.Contains(res.Output, "Handle()") {
		t.Fatalf("region map missing its header or its declaration:\n%s", res.Output)
	}
	for _, outside := range []string{"First", "Last", "type Server"} {
		if strings.Contains(res.Output, outside) {
			t.Errorf("region map lists %q, which lies outside lines 7-8:\n%s", outside, res.Output)
		}
	}
}

// An inverted range is a caller slip, refused the way read refuses one.
func TestFileMapTool_InvertedRangeIsRefused(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "demo.go"), "package demo\n")
	args, _ := json.Marshal(map[string]any{"path": "demo.go", "start_line": 9, "end_line": 3})
	if _, err := (FileMapTool{}).Execute(context.Background(), args, Context{SessionDir: dir}); err == nil {
		t.Fatal("an inverted range was accepted")
	}
}
