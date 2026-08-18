package codemap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write drops content into a temp file with the given name and returns its path.
func write(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// find returns the symbol with the given name, failing the test if absent.
func find(t *testing.T, fm *FileMap, name string) *Symbol {
	t.Helper()
	for _, s := range fm.Symbols {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("symbol %q not in outline; got %v", name, names(fm))
	return nil
}

func names(fm *FileMap) []string {
	var out []string
	for _, s := range fm.Symbols {
		out = append(out, s.Name)
	}
	return out
}

const goSource = `package demo

import (
	"fmt"
	"os"
)

// Greeter greets.
type Greeter struct {
	name string
}

// Hello writes a greeting.
// It wraps across two comment lines.
func (g *Greeter) Hello(w os.File) error {
	local := 1
	var inner = "not a top-level declaration"
	const alsoInner = 2
	type innerType struct{}
	fmt.Println(local, inner, alsoInner)
	return nil
}

// Detached comment.

func Orphan() {}

const (
	First  = 1
	Second = 2
)

var lonely = 3
`

func TestOutlineGoKindsAndRanges(t *testing.T) {
	fm, err := Outline(write(t, "demo.go", goSource))
	if err != nil {
		t.Fatal(err)
	}
	if fm.Lang != "go" {
		t.Errorf("Lang = %q, want go", fm.Lang)
	}
	if fm.ParseError {
		t.Error("valid source reported a parse error")
	}

	greeter := find(t, fm, "Greeter")
	if greeter.Kind != "type" {
		t.Errorf("Greeter kind = %q, want type", greeter.Kind)
	}
	// Line 8 is the doc comment, 9 the declaration, 11 the closing brace.
	if greeter.StartLine != 8 || greeter.EndLine != 11 {
		t.Errorf("Greeter range = %d-%d, want 8-11", greeter.StartLine, greeter.EndLine)
	}

	hello := find(t, fm, "Hello")
	if hello.Kind != "method" {
		t.Errorf("Hello kind = %q, want method", hello.Kind)
	}
	if !strings.Contains(hello.Signature, "(g *Greeter) Hello") {
		t.Errorf("Hello signature lost its receiver: %q", hello.Signature)
	}
	// The doc block starts two lines above the func, and is joined into one line.
	if hello.StartLine != 13 {
		t.Errorf("Hello StartLine = %d, want 13 (doc comment included)", hello.StartLine)
	}
	if want := "Hello writes a greeting. It wraps across two comment lines."; hello.Doc != want {
		t.Errorf("Hello doc = %q, want %q", hello.Doc, want)
	}
}

// Declarations inside a function body are not part of a file's outline. Go's
// const/var/type declarations match at any depth, so the queries anchor each
// pattern under (source_file); without that anchor every local in the file
// would be listed.
func TestOutlineSkipsFunctionLocals(t *testing.T) {
	fm, err := Outline(write(t, "demo.go", goSource))
	if err != nil {
		t.Fatal(err)
	}
	for _, local := range []string{"inner", "alsoInner", "innerType"} {
		for _, s := range fm.Symbols {
			if s.Name == local {
				t.Errorf("local declaration %q leaked into the outline", local)
			}
		}
	}
}

// A comment separated from a declaration by a blank line documents whatever
// preceded it, so it must not be pulled into the declaration's range.
func TestOutlineDocRequiresAdjacency(t *testing.T) {
	fm, err := Outline(write(t, "demo.go", goSource))
	if err != nil {
		t.Fatal(err)
	}
	orphan := find(t, fm, "Orphan")
	if orphan.Doc != "" {
		t.Errorf("detached comment attached as doc: %q", orphan.Doc)
	}
	if orphan.StartLine != 26 {
		t.Errorf("Orphan StartLine = %d, want 26 (comment excluded)", orphan.StartLine)
	}
}

// A grouped declaration binds several names; listing them is what makes the
// block worth a line, since its first source line is only `const (`.
func TestOutlineGroupedDeclaration(t *testing.T) {
	fm, err := Outline(write(t, "demo.go", goSource))
	if err != nil {
		t.Fatal(err)
	}
	first := find(t, fm, "First")
	if !strings.Contains(first.Signature, "First") || !strings.Contains(first.Signature, "Second") {
		t.Errorf("grouped const signature = %q, want both names", first.Signature)
	}
	lonely := find(t, fm, "lonely")
	if lonely.Signature != "var lonely = 3" {
		t.Errorf("single var signature = %q", lonely.Signature)
	}
}

// Tree-sitter recovers from syntax errors, so a broken file still yields most
// of its outline — but the caller is told the map may be incomplete.
func TestOutlineReportsParseError(t *testing.T) {
	fm, err := Outline(write(t, "broken.go", "package demo\n\nfunc Good() {}\n\nfunc Bad( {\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !fm.ParseError {
		t.Error("ParseError = false for a file with a syntax error")
	}
	find(t, fm, "Good")
}

func TestOutlineFallbackMarkdown(t *testing.T) {
	md := "# Title\n\ntext\n\n## Section\n\n```\n# not a heading, inside a fence\n```\n\n### Deep\n"
	fm, err := Outline(write(t, "doc.md", md))
	if err != nil {
		t.Fatal(err)
	}
	if !fm.Fallback {
		t.Error("Fallback = false for markdown")
	}
	if got := names(fm); len(got) != 3 {
		t.Fatalf("headings = %v, want 3 (fenced heading must be ignored)", got)
	}
	if fm.Symbols[0].Kind != "h1" || fm.Symbols[2].Kind != "h3" {
		t.Errorf("heading levels = %q/%q", fm.Symbols[0].Kind, fm.Symbols[2].Kind)
	}
	// Each heading runs until the next one begins.
	if fm.Symbols[0].EndLine != 4 {
		t.Errorf("first heading EndLine = %d, want 4", fm.Symbols[0].EndLine)
	}
}

// .vue has no grammar, so it exercises the heuristic scanner.
func TestOutlineFallbackUnknownExtension(t *testing.T) {
	src := "<script>\n" +
		"export function alpha() {}\n" +
		"export const beta = async (x) => {}\n" +
		"export class Gamma {}\n" +
		"</script>\n"
	fm, err := Outline(write(t, "x.vue", src))
	if err != nil {
		t.Fatal(err)
	}
	if !fm.Fallback {
		t.Error("Fallback = false for .vue (no grammar registered)")
	}
	for _, want := range []string{"alpha", "beta", "Gamma"} {
		find(t, fm, want)
	}
}

func TestOutlineRejectsBinaryAndOversize(t *testing.T) {
	if _, err := Outline(write(t, "blob.go", "package a\x00\x00")); err != ErrBinary {
		t.Errorf("binary file err = %v, want ErrBinary", err)
	}

	big := write(t, "big.go", "")
	if err := os.WriteFile(big, make([]byte, MaxFileSize+1), 0o644); err != nil {
		t.Fatal(err)
	}
	var tooLarge *TooLargeError
	if _, err := Outline(big); err == nil || !asTooLarge(err, &tooLarge) {
		t.Errorf("oversize err = %v, want *TooLargeError", err)
	}
}

func TestRenderShapes(t *testing.T) {
	fm, err := Outline(write(t, "demo.go", goSource))
	if err != nil {
		t.Fatal(err)
	}
	out := Render(fm)
	if !strings.Contains(out, "start_line=N, end_line=M") {
		t.Error("render omits the read() usage hint")
	}
	// The doc convention repeats the identifier; the signature above already
	// carries it, so the excerpt drops the prefix.
	if strings.Contains(out, "└ Hello writes") {
		t.Error("render repeated the symbol name in its doc excerpt")
	}
	if !strings.Contains(out, "└ writes a greeting.") {
		t.Errorf("render lost the doc excerpt:\n%s", out)
	}
}

func TestRenderEmpty(t *testing.T) {
	fm, err := Outline(write(t, "empty.go", "package demo\n"))
	if err != nil {
		t.Fatal(err)
	}
	// The package clause alone still counts as a declaration.
	if len(fm.Symbols) == 0 {
		t.Fatal("package clause missing from outline")
	}
	if !strings.Contains(Render(fm), "package demo") {
		t.Error("render omits the package clause")
	}
}

func asTooLarge(err error, target **TooLargeError) bool {
	t, ok := err.(*TooLargeError)
	if ok {
		*target = t
	}
	return ok
}
