package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The filename is the untrusted string here, and the guard runs before
// anything compiles: a name whose cleaned form escapes the session directory,
// an absolute one, and a whitespace-only one are all refused; the default and
// the ".pdf"-suffix append still work.
func TestLatexPDFPathShapesAndGuardsTheName(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "out.pdf")
	if got, ok := latexPDFPath(dir, "out.pdf"); !ok || got != want {
		t.Errorf("latexPDFPath(dir, \"out.pdf\") = %q, %v; want %q", got, ok, want)
	}
	if got, ok := latexPDFPath(dir, "report"); !ok || filepath.Base(got) != "report.pdf" {
		t.Errorf("latexPDFPath(dir, \"report\") = %q, %v; want the .pdf suffix appended", got, ok)
	}
	if got, ok := latexPDFPath(dir, ""); !ok || filepath.Base(got) != "output.pdf" {
		t.Errorf("empty filename must default to output.pdf, got %q, %v", got, ok)
	}
	for _, bad := range []string{"../evil.pdf", "../evil", "/tmp/escape.pdf"} {
		if got, ok := latexPDFPath(dir, bad); ok {
			t.Errorf("latexPDFPath(dir, %q) = %q, ok; want the escape refused", bad, got)
		}
	}
	if _, ok := latexPDFPath(dir, "  "); ok {
		t.Error("a whitespace-only filename must be refused")
	}
}

// The tool-level refusal: an escaping filename is turned away where it enters,
// before any compile, and nothing is written outside the session directory.
func TestLatexToPdfRefusesAFilenameThatEscapes(t *testing.T) {
	dir := t.TempDir()
	args, _ := json.Marshal(map[string]any{"source": "\\documentclass{article}", "filename": "../evil.pdf"})
	res, err := LatexToPdfTool{}.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res.Output, "not allowed") {
		t.Errorf("expected a refusal for ../evil.pdf, got:\n%s", res.Output)
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "evil.pdf")); err == nil {
		t.Error("../evil.pdf was written outside the session directory")
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.pdf")); err == nil {
		t.Error("the escaped name must not be folded into the session directory either")
	}
}

// A plain name still resolves inside the session directory, end to end.
func TestLatexToPdfPlainFilenameStillCompiles(t *testing.T) {
	if !pdflatexAvailable() {
		t.Skip("pdflatex is not installed on this system")
	}
	dir := t.TempDir()
	args, _ := json.Marshal(map[string]any{
		"source":   "\\documentclass{article}\\begin{document}hello\\end{document}",
		"filename": "out",
	})
	res, err := LatexToPdfTool{}.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res.Output, "PDF generated successfully") ||
		!strings.Contains(res.Output, filepath.Join(dir, "out.pdf")) {
		t.Errorf("plain filename did not produce out.pdf in the session dir:\n%s", res.Output)
	}
}
