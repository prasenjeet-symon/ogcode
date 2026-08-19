package codemap

import (
	"strings"
	"testing"
)

func TestCheckCleanFile(t *testing.T) {
	path := write(t, "clean.go", "package demo\n\nfunc Add(a, b int) int { return a + b }\n")

	res, err := Check(path)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() {
		t.Errorf("OK() = false for a valid file; diagnostics: %v", res.Diagnostics)
	}
	if res.Lang != "go" {
		t.Errorf("Lang = %q, want go", res.Lang)
	}
}

func TestCheckReportsMissingToken(t *testing.T) {
	// The call is never closed, so the parser inserts a zero-width MISSING ")"
	// rather than an ERROR — the one case where it can name what it wanted.
	path := write(t, "broken.go", "package demo\n\nfunc main() {\n\tprintln(\"hi\"\n}\n")

	res, err := Check(path)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK() {
		t.Fatal("OK() = true for a file with an unclosed call")
	}
	d := res.Diagnostics[0]
	if d.Line != 4 {
		t.Errorf("Line = %d, want 4", d.Line)
	}
	if !d.Missing {
		t.Errorf("Missing = false, want true for an unclosed call")
	}
	if !strings.Contains(d.Message, `")"`) {
		t.Errorf("Message = %q, want it to name the expected token", d.Message)
	}
	if !strings.Contains(d.Source, "println") {
		t.Errorf("Source = %q, want the offending source line", d.Source)
	}
}

// Bytes that fit no rule at all arrive as an ERROR node rather than a MISSING
// one. Confined to a line, the offending source is short enough to quote, and
// quoting it saves the reader a lookup.
func TestCheckReportsUnparsableRegion(t *testing.T) {
	path := write(t, "broken.ts", "const x = ;\nconst y = 2\n")

	res, err := Check(path)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK() {
		t.Fatal("OK() = true for an assignment with no right-hand side")
	}
	d := res.Diagnostics[0]
	if d.Line != 1 || d.EndLine != 1 {
		t.Errorf("range = %d-%d, want 1-1 for a single-line error", d.Line, d.EndLine)
	}
	if d.Missing {
		t.Errorf("Missing = true, want an unparsable-region diagnostic")
	}
	if !strings.Contains(d.Message, `"="`) {
		t.Errorf("Message = %q, want it to quote the unparsable source", d.Message)
	}
}

// An unclosed construct produces one ERROR running from the mistake to wherever
// the parser gave up. Reporting that as a range is the point: the start alone
// reads as a one-line typo and the end alone points past the mistake, while
// "lines 2 to 3" tells the reader what to go look at.
func TestCheckReportsMultiLineRegionAsARange(t *testing.T) {
	path := write(t, "broken.py", "def f():\n    x = 1 ** ** 2\n    return x\n")

	res, err := Check(path)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK() {
		t.Fatal("OK() = true for a malformed expression")
	}
	d := res.Diagnostics[0]
	if d.EndLine <= d.Line {
		t.Fatalf("range = %d-%d, want a region spanning more than one line", d.Line, d.EndLine)
	}
	if !strings.Contains(d.Message, "runs to line 3") {
		t.Errorf("Message = %q, want it to name the end of the region", d.Message)
	}
}

// One damaged region must be announced once. Tree-sitter nests ERRORs inside
// ERRORs, and reporting every level would turn a single mistake into a stack of
// identical-looking complaints at different indents.
func TestCheckReportsEachRegionOnce(t *testing.T) {
	path := write(t, "nested.go", "package p\n\nfunc f() {\n\tif x {\n\t\tg(((\n\t}\n}\n")

	res, err := Check(path)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK() {
		t.Fatal("OK() = true for deeply unbalanced brackets")
	}
	if len(res.Diagnostics) > 3 {
		t.Errorf("got %d diagnostics for one damaged region: %v", len(res.Diagnostics), res.Diagnostics)
	}
}

// A diagnostic's line and column are the numbering file_map prints and read
// accepts, so the agent can go straight from an error to the code around it.
func TestCheckDiagnosticsAre1Based(t *testing.T) {
	path := write(t, "first.go", "func broken( {\n")

	res, err := Check(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Diagnostics) == 0 {
		t.Fatal("no diagnostics for a file that is broken on line 1")
	}
	for _, d := range res.Diagnostics {
		if d.Line < 1 || d.Column < 1 {
			t.Errorf("diagnostic at %d:%d is not 1-based", d.Line, d.Column)
		}
	}
}

// An unknown extension must never look like a pass. Checked stays false, and
// OK() reports unknown rather than clean, because an agent that reads silence
// as success has bought exactly the false confidence this tool exists to deny.
func TestCheckUnsupportedLanguageIsNotOK(t *testing.T) {
	path := write(t, "config.yaml", "key: [unclosed\n")

	res, err := Check(path)
	if err != nil {
		t.Fatal(err)
	}
	if res.Checked {
		t.Error("Checked = true for a file type with no grammar")
	}
	if res.OK() {
		t.Error("OK() = true for a file that was never parsed")
	}
	out := RenderCheck(res)
	if !strings.Contains(out, "NOT CHECKED") {
		t.Errorf("RenderCheck output does not say the file was unchecked:\n%s", out)
	}
}

// Every registered grammar has to work through Check, not just the one the
// tests happen to use most.
func TestCheckAcrossLanguages(t *testing.T) {
	cases := []struct{ name, clean, broken string }{
		{"a.go", "package p\n\nfunc f() {}\n", "package p\n\nfunc f( {}\n"},
		{"a.py", "def f():\n    return 1\n", "def f(:\n    return 1\n"},
		{"a.ts", "export function f(): number {\n  return 1\n}\n", "export function f(: number {\n  return 1\n}\n"},
		{"a.tsx", "export const A = () => <div />\n", "export const A = () => <div\n"},
		{"a.php", "<?php\nfunction f() { return 1; }\n", "<?php\nfunction f( { return 1; }\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clean, err := Check(write(t, c.name, c.clean))
			if err != nil {
				t.Fatal(err)
			}
			if !clean.OK() {
				t.Errorf("valid source reported as broken: %v", clean.Diagnostics)
			}

			broken, err := Check(write(t, c.name, c.broken))
			if err != nil {
				t.Fatal(err)
			}
			if broken.OK() {
				t.Error("invalid source reported as clean")
			}
		})
	}
}

// One mistake can cascade into a long tail of recovery noise. The cap keeps a
// badly damaged file from spending the agent's context on it.
func TestCheckCapsDiagnostics(t *testing.T) {
	path := write(t, "mess.go", "package p\n"+strings.Repeat("func a( {}\n", 200))

	res, err := Check(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Diagnostics) > MaxDiagnostics {
		t.Errorf("got %d diagnostics, want at most %d", len(res.Diagnostics), MaxDiagnostics)
	}
}

// The renderer's three verdicts have to be distinguishable at a glance, since
// acting on the wrong one is the failure mode that matters.
func TestRenderCheckVerdicts(t *testing.T) {
	clean, _ := Check(write(t, "ok.go", "package p\n"))
	if out := RenderCheck(clean); !strings.HasPrefix(out, "OK:") {
		t.Errorf("clean render does not lead with OK:\n%s", out)
	}

	broken, _ := Check(write(t, "bad.go", "package p\n\nfunc f( {\n"))
	out := RenderCheck(broken)
	if !strings.HasPrefix(out, "SYNTAX ERRORS:") {
		t.Errorf("broken render does not lead with SYNTAX ERRORS:\n%s", out)
	}
	if !strings.Contains(out, "3:") {
		t.Errorf("broken render omits the error position:\n%s", out)
	}
}
