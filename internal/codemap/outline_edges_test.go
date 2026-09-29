package codemap

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// Go wraps a var group's specs in a var_spec_list the const group does not
// have, and the names inside have to be found through it.
func TestOutlineGoVarGroupNames(t *testing.T) {
	src := "package x\n\nvar (\n\tVersion = \"v1\"\n\tCommit  = \"none\"\n)\n\nvar (\n\tonly = 1\n)\n"
	fm, err := Outline(write(t, "v.go", src))
	if err != nil {
		t.Fatal(err)
	}
	if s := find(t, fm, "Version"); s.Signature != "var ( Version, Commit )" || s.StartLine != 3 || s.EndLine != 6 {
		t.Errorf("group = %d-%d %q, want 3-6 %q", s.StartLine, s.EndLine, s.Signature, "var ( Version, Commit )")
	}
	if s := find(t, fm, "only"); s.Signature != "var ( only )" {
		t.Errorf("group of one = %q, want %q", s.Signature, "var ( only )")
	}
}

// Siblings sharing a line are siblings. Judged by lines, the second read as
// nested inside the first, and a run of them chained deeper and deeper.
func TestOutlineSameLineSiblingsDoNotNest(t *testing.T) {
	css, err := Outline(write(t, "a.css", ".a{color:red}.b{color:blue}.c{color:green}\n.d{color:black}\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range css.Symbols {
		if s.Depth != 0 {
			t.Errorf("rule %s depth = %d, want 0", s.Name, s.Depth)
		}
	}

	html, err := Outline(write(t, "a.html", "<ul class=\"list\">\n<li class=\"a\">x</li><li class=\"b\">y</li><li class=\"c\">z</li>\n</ul>\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c"} {
		if d := find(t, html, name).Depth; d != 1 {
			t.Errorf("<li class=%q> depth = %d, want 1 (inside the list, beside its siblings)", name, d)
		}
	}
}

// capPage builds an HTML page of sections, each holding items, deep enough that
// the whole outline overflows the cap.
func capPage(sections, items int) string {
	var b strings.Builder
	b.WriteString("<body class=\"page\">\n")
	for i := 0; i < sections; i++ {
		fmt.Fprintf(&b, "<section id=\"s%d\">\n", i)
		for j := 0; j < items; j++ {
			fmt.Fprintf(&b, "  <div class=\"item%d\">x</div>\n", j)
		}
		b.WriteString("</section>\n")
	}
	b.WriteString("</body>\n")
	return b.String()
}

// The cap drops the deepest entries first, so the map still reaches the end of
// the file. Cutting by position kept the top of a long page in full and gave
// the rest of it no map at all.
func TestOutlineCapDropsDeepestFirst(t *testing.T) {
	fm, err := Outline(write(t, "page.html", capPage(150, 3)))
	if err != nil {
		t.Fatal(err)
	}
	if len(fm.Symbols) != 151 || fm.Omitted != 450 || fm.OmittedDepth != 2 || fm.StopLine != 0 {
		t.Fatalf("kept %d, omitted %d at depth %d, stop %d; want 151 kept, 450 omitted at depth 2, no stop line",
			len(fm.Symbols), fm.Omitted, fm.OmittedDepth, fm.StopLine)
	}
	find(t, fm, "s149") // the last section, at the bottom of the file
	out := Render(fm)
	if !strings.Contains(out, "450 declarations nested 2 or more levels deep") || !strings.Contains(out, "file_map(path, start_line=N, end_line=M)") {
		t.Errorf("render does not explain the cap:\n%s", out[len(out)-300:])
	}
}

// When even the file-scope entries overflow, the map is cut by position and
// says where it stops.
func TestOutlineCapStopsWhenFileScopeOverflows(t *testing.T) {
	var b strings.Builder
	b.WriteString("package x\n")
	for i := 0; i < MaxSymbols+50; i++ {
		fmt.Fprintf(&b, "\nfunc F%d() {}\n", i)
	}
	fm, err := Outline(write(t, "many.go", b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(fm.Symbols) != MaxSymbols || fm.Omitted != 51 || fm.StopLine == 0 {
		t.Fatalf("kept %d, omitted %d, stop line %d; want %d kept, 51 omitted, a stop line", len(fm.Symbols), fm.Omitted, fm.StopLine, MaxSymbols)
	}
	last := fm.Symbols[len(fm.Symbols)-1]
	if fm.StopLine != last.EndLine {
		t.Errorf("stop line %d, want the last entry's end %d", fm.StopLine, last.EndLine)
	}
	if out := Render(fm); !strings.Contains(out, fmt.Sprintf("stops at line %d", fm.StopLine)) {
		t.Errorf("render does not say where the map stops:\n%s", out[len(out)-300:])
	}
}

// A region is mapped in full, enclosing declarations included, and the cap
// applies to the region rather than to the file.
func TestOutlineRangeMapsARegionInFull(t *testing.T) {
	path := write(t, "page.html", capPage(150, 3))
	whole, err := Outline(path)
	if err != nil {
		t.Fatal(err)
	}
	s := find(t, whole, "s100")
	fm, err := OutlineRange(path, s.StartLine, s.EndLine)
	if err != nil {
		t.Fatal(err)
	}
	if fm.Omitted != 0 {
		t.Fatalf("region map omitted %d", fm.Omitted)
	}
	got := names(fm)
	want := []string{"page", "s100", "item0", "item1", "item2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("region map = %v, want %v", got, want)
	}
	if out := Render(fm); !strings.Contains(out, fmt.Sprintf("map of lines %d-%d", s.StartLine, s.EndLine)) {
		t.Errorf("header does not name the region:\n%s", out)
	}
}

// A file whose last line has no newline is not damaged. tree-sitter-go ends
// such a file with a hidden MISSING terminator, which HasError reports.
func TestOutlineNoFinalNewlineIsNotAParseError(t *testing.T) {
	fm, err := Outline(write(t, "embed.go", "package web\n\nimport \"embed\"\n\n//go:embed all:dist\nvar DistFS embed.FS"))
	if err != nil {
		t.Fatal(err)
	}
	if fm.ParseError {
		t.Error("a valid file without a final newline is flagged as having syntax errors")
	}
	broken, err := Outline(write(t, "broken.go", "package x\n\nfunc F() {"))
	if err != nil {
		t.Fatal(err)
	}
	if !broken.ParseError {
		t.Error("a truncated file without a final newline is no longer flagged")
	}
}

// Anonymous default exports have nothing a declaration pattern can name, so
// they are captured whole — and what they declare nests under them.
func TestOutlineTSAnonymousDefaultExport(t *testing.T) {
	fm, err := Outline(write(t, "page.tsx", "export default () => {\n  const onClick = () => {}\n  return <div onClick={onClick} />\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	def := find(t, fm, "default")
	if def.Signature != "export default () =>" || def.Depth != 0 || def.StartLine != 1 || def.EndLine != 4 {
		t.Errorf("default = %d-%d depth %d %q", def.StartLine, def.EndLine, def.Depth, def.Signature)
	}
	if d := find(t, fm, "onClick").Depth; d != 1 {
		t.Errorf("onClick depth = %d, want 1 (inside the default export)", d)
	}

	for src, want := range map[string]string{
		"export default function () {\n  return 1\n}\n": "export default function ()",
		"export default class extends Base {}\n":        "export default class extends Base",
	} {
		fm, err := Outline(write(t, "d.ts", src))
		if err != nil {
			t.Fatal(err)
		}
		if got := find(t, fm, "default").Signature; got != want {
			t.Errorf("signature = %q, want %q", got, want)
		}
	}
}

// An object-literal module lists its function-valued members; a plain
// property is data and stays out.
func TestOutlineTSObjectLiteralMethods(t *testing.T) {
	src := "export const api = {\n  get(id: string) {\n    return id\n  },\n  post: async (body: string) => {},\n  base: '/v1',\n}\n\nexport default {\n  data() { return {} },\n}\n"
	fm, err := Outline(write(t, "api.ts", src))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, sig string }{
		{"get", "get(id: string)"},
		{"post", "post: async (body: string) =>"},
		{"data", "data()"},
	}
	for _, c := range cases {
		s := find(t, fm, c.name)
		if s.Signature != c.sig || s.Depth != 1 {
			t.Errorf("%s = depth %d %q, want depth 1 %q", c.name, s.Depth, s.Signature, c.sig)
		}
	}
	for _, s := range fm.Symbols {
		if s.Name == "base" {
			t.Error("a plain data property is listed")
		}
	}
}

// A directive annotates the declaration but is not its doc; it stays in the
// range and out of the text.
func TestOutlineDocSkipsDirectives(t *testing.T) {
	fm, err := Outline(write(t, "d.go", "package x\n\n//go:embed all:dist\nvar DistFS embed.FS\n\n// Handler serves the page.\n//nolint:errcheck\nfunc Handler() {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s := find(t, fm, "DistFS"); s.Doc != "" || s.StartLine != 3 {
		t.Errorf("DistFS = start %d doc %q, want start 3 (directive kept in range) and no doc", s.StartLine, s.Doc)
	}
	if s := find(t, fm, "Handler"); s.Doc != "Handler serves the page." || s.StartLine != 6 {
		t.Errorf("Handler = start %d doc %q", s.StartLine, s.Doc)
	}

	ts, err := Outline(write(t, "log.ts", "// eslint-disable-next-line no-console\nexport function log(m: string) { console.log(m) }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if doc := find(t, ts, "log").Doc; doc != "" {
		t.Errorf("log doc = %q, want none", doc)
	}
}

// Banner rules around a section comment are decoration, not text.
func TestOutlineDocStripsBannerRules(t *testing.T) {
	css, err := Outline(write(t, "a.css", "/* ============================================================\n   Typefaces — self-hosted\n   ============================================================ */\n.a { color: red; }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if doc := find(t, css, ".a").Doc; doc != "Typefaces — self-hosted" {
		t.Errorf("css doc = %q", doc)
	}
	html, err := Outline(write(t, "a.html", "<!-- ══ Nav ══════════════════ -->\n<nav id=\"nav\"></nav>\n"))
	if err != nil {
		t.Fatal(err)
	}
	if doc := find(t, html, "nav").Doc; doc != "Nav" {
		t.Errorf("html doc = %q, want %q", doc, "Nav")
	}
	py, err := Outline(write(t, "a.py", "# ------------------------------------------------ model\ndef load():\n    pass\n"))
	if err != nil {
		t.Fatal(err)
	}
	if doc := find(t, py, "load").Doc; doc != "model" {
		t.Errorf("python doc = %q, want %q", doc, "model")
	}
}

// A doc with no space to break at is cut on a character boundary.
func TestDocExcerptCutsOnCharacterBoundary(t *testing.T) {
	s := &Symbol{Name: "F", Doc: strings.Repeat("文档注释", 20)}
	got := docExcerpt(s)
	if !utf8.ValidString(got) {
		t.Fatalf("excerpt is not valid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "…") || len(got) > maxDocLen+len("…") {
		t.Errorf("excerpt = %q", got)
	}
}

// A byte-order mark is not text before the first comment.
func TestOutlineBOMKeepsFirstDoc(t *testing.T) {
	fm, err := Outline(write(t, "b.ts", "\xef\xbb\xbf// Adds two numbers.\nexport function add(a: number, b: number) {\n  return a + b\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s := find(t, fm, "add"); s.StartLine != 1 || s.Doc != "Adds two numbers." {
		t.Errorf("add = start %d doc %q, want start 1 with its doc", s.StartLine, s.Doc)
	}
}
