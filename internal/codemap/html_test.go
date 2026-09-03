package codemap

import "testing"

const htmlSource = `<!DOCTYPE html>
<html>
<head>
  <title>demo</title>
  <style type="text/css">
    .inside { color: red; }
  </style>
</head>
<body>
  <!-- primary navigation -->
  <nav id="main" class="nav wide">
    <a href="/" class="link">home</a>
    <img src="x.png" alt="logo" class="brand"/>
  </nav>
  <div class="card mt-4">just a card</div>
  <div>not captured</div>
  <script src="/app.js"></script>
  <script>
    var cmp = a < b;
  </script>
</body>
</html>
`

func outlineHtml(t *testing.T, name, src string) *FileMap {
	t.Helper()
	fm, err := Outline(write(t, name, src))
	if err != nil {
		t.Fatal(err)
	}
	if fm.Fallback {
		t.Fatalf("%s fell back to the heuristic scanner instead of using the HTML grammar", name)
	}
	if fm.ParseError {
		t.Errorf("%s reported a parse error on valid source", name)
	}
	if fm.Lang != "html" {
		t.Errorf("Lang = %q, want html", fm.Lang)
	}
	return fm
}

func TestOutlineHTMLKindsAndNames(t *testing.T) {
	fm := outlineHtml(t, "page.html", htmlSource)

	// The id names the element; the class is present but loses to it.
	nav := find(t, fm, "main")
	if nav.Kind != "element" {
		t.Errorf("main kind = %q, want element", nav.Kind)
	}
	if want := `<nav id="main" class="nav wide">`; nav.Signature != want {
		t.Errorf("main signature = %q, want %q", nav.Signature, want)
	}

	// A class-only element is named by the class.
	link := find(t, fm, "link")
	if link.Kind != "element" {
		t.Errorf("link kind = %q, want element", link.Kind)
	}
	brand := find(t, fm, "brand")
	if brand.Kind != "element" {
		t.Errorf("brand kind = %q, want element (self-closing tags are captured too)", brand.Kind)
	}
	// The first whitespace-separated class word is the name.
	card := find(t, fm, "card")
	if card.Kind != "element" {
		t.Errorf("card kind = %q, want element", card.Kind)
	}
	if card.Signature != `<div class="card mt-4">` {
		t.Errorf("card signature = %q, want the collapsed opening tag", card.Signature)
	}

	// script and style have neither attribute; the tag is the only name.
	if got := find(t, fm, "style").Kind; got != "style" {
		t.Errorf("style kind = %q, want style", got)
	}
	script := find(t, fm, "script")
	if script.Kind != "script" {
		t.Errorf("script kind = %q, want script", script.Kind)
	}
	if script.Signature != `<script src="/app.js">` {
		t.Errorf("script signature = %q, want the opening tag", script.Signature)
	}
}

func TestOutlineHTMLDocComment(t *testing.T) {
	fm := outlineHtml(t, "page.html", htmlSource)

	nav := find(t, fm, "main")
	if nav.Doc != "primary navigation" {
		t.Errorf("main doc = %q, want the comment text with markers stripped", nav.Doc)
	}
	// Line 10 is the comment, 11 the element, 14 the closing tag.
	if nav.StartLine != 10 || nav.EndLine != 14 {
		t.Errorf("main range = %d-%d, want 10-14 (doc comment included)", nav.StartLine, nav.EndLine)
	}
}

func TestOutlineHTMLNesting(t *testing.T) {
	fm := outlineHtml(t, "page.html", htmlSource)

	if d := find(t, fm, "main").Depth; d != 0 {
		t.Errorf("main depth = %d, want 0", d)
	}
	if d := find(t, fm, "link").Depth; d != 1 {
		t.Errorf("link depth = %d, want 1 (inside main)", d)
	}
	if d := find(t, fm, "brand").Depth; d != 1 {
		t.Errorf("brand depth = %d, want 1", d)
	}
}

func TestOutlineHTMLSkipsPlainElements(t *testing.T) {
	fm := outlineHtml(t, "page.html", htmlSource)

	for _, s := range fm.Symbols {
		switch s.Name {
		case "div", "title", "html", "head", "body":
			t.Errorf("element %q has no id or class and should not be outlined", s.Name)
		}
	}
	// style, main, link, brand, card, and two scripts.
	if len(fm.Symbols) != 7 {
		t.Errorf("symbol count = %d, want 7; got %v", len(fm.Symbols), names(fm))
	}
}

// An element carrying both attributes matches the query once per attribute.
// The outline must show it once.
func TestOutlineHTMLDeduplicatesIdAndClassMatch(t *testing.T) {
	fm := outlineHtml(t, "page.html", htmlSource)

	count := 0
	for _, s := range fm.Symbols {
		if s.Name == "main" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("nav with id and class outlined %d times, want 1", count)
	}
}

func TestOutlineHtmExtension(t *testing.T) {
	fm := outlineHtml(t, "page.htm", htmlSource)
	find(t, fm, "main")
}

func TestDedupSymbolsKeepsFirstOfAdjacentRun(t *testing.T) {
	in := []*Symbol{
		{Kind: "element", Name: "main", StartLine: 11, EndLine: 14, Signature: "<nav>"},
		{Kind: "element", Name: "main", StartLine: 11, EndLine: 14, Signature: "<nav>"},
		{Kind: "element", Name: "list", StartLine: 21, EndLine: 21, Signature: "<ul>"},
	}
	out := dedupSymbols(in)
	if len(out) != 2 {
		t.Fatalf("dedup kept %d of 3 symbols, want 2", len(out))
	}
	if out[0].Name != "main" || out[1].Name != "list" {
		t.Errorf("kept %q, %q — want main then list", out[0].Name, out[1].Name)
	}
}
