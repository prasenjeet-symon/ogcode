package codemap

import "testing"

const cssSource = `@import url("theme.css");
@charset "UTF-8";

/* base typography */
body {
  font-family: sans-serif;
  color: #222;
}

.card,
#hero > .title {
  padding: 1rem;
}

@media (max-width: 600px) {
  .card { padding: 0.5rem; }
}

@keyframes pulse {
  from { opacity: 0; }
  to { opacity: 1; }
}

@supports (display: grid) {
  .grid { display: grid; }
}

@font-face {
  font-family: "Open Sans";
}
`

func outlineCss(t *testing.T, name, src string) *FileMap {
	t.Helper()
	fm, err := Outline(write(t, name, src))
	if err != nil {
		t.Fatal(err)
	}
	if fm.Fallback {
		t.Fatalf("%s fell back to the heuristic scanner instead of using the CSS grammar", name)
	}
	if fm.ParseError {
		t.Errorf("%s reported a parse error on valid source", name)
	}
	if fm.Lang != "css" {
		t.Errorf("Lang = %q, want css", fm.Lang)
	}
	return fm
}

func TestOutlineCSSKindsAndNames(t *testing.T) {
	fm := outlineCss(t, "theme.css", cssSource)

	// Rules are named by their selectors text, collapsed to one line.
	if got := find(t, fm, "body").Kind; got != "rule" {
		t.Errorf("body kind = %q, want rule", got)
	}
	if got := find(t, fm, ".card, #hero > .title").Kind; got != "rule" {
		t.Errorf("multi-line selector list kind = %q, want rule", got)
	}
	if got := find(t, fm, "pulse").Kind; got != "keyframes" {
		t.Errorf("pulse kind = %q, want keyframes", got)
	}
	// The import is present but nameless, like every language's import block.
	var imports int
	for _, s := range fm.Symbols {
		if s.Kind == "import" {
			imports++
		}
	}
	if imports != 1 {
		t.Errorf("import statements in outline = %d, want 1", imports)
	}
}

func TestOutlineCSSSignatures(t *testing.T) {
	fm := outlineCss(t, "theme.css", cssSource)

	if s := find(t, fm, "body").Signature; s != "body" {
		t.Errorf("body signature = %q, want the selectors text", s)
	}
	// The multi-line selector list folds onto one line.
	if s := find(t, fm, ".card, #hero > .title").Signature; s != ".card, #hero > .title" {
		t.Errorf("card/hero signature = %q, want the collapsed selector list", s)
	}
	// keyframes is named by its name alias and keeps the default signature.
	if s := find(t, fm, "pulse").Signature; s != "@keyframes pulse" {
		t.Errorf("pulse signature = %q, want the at-rule line", s)
	}
	// The import block keeps the shared nameless-import signature.
	var importSig string
	for _, s := range fm.Symbols {
		if s.Kind == "import" {
			importSig = s.Signature
		}
	}
	if importSig != "import block" {
		t.Errorf("import signature = %q, want %q", importSig, "import block")
	}
	if s := find(t, fm, "@media").Signature; s != "@media (max-width: 600px)" {
		t.Errorf("media signature = %q, want the at-rule line", s)
	}
	if s := find(t, fm, "@supports").Signature; s != "@supports (display: grid)" {
		t.Errorf("supports signature = %q, want the at-rule line", s)
	}
	if s := find(t, fm, "@font-face").Signature; s != "@font-face" {
		t.Errorf("@font-face signature = %q, want the at-rule line", s)
	}
}

func TestOutlineCSSNesting(t *testing.T) {
	fm := outlineCss(t, "theme.css", cssSource)

	if d := find(t, fm, "@media").Depth; d != 0 {
		t.Errorf("@media depth = %d, want 0", d)
	}
	// The rule inside the media query nests under it.
	if d := find(t, fm, ".card").Depth; d != 1 {
		t.Errorf(".card depth = %d, want 1 (inside the media query)", d)
	}
	if d := find(t, fm, ".card, #hero > .title").Depth; d != 0 {
		t.Errorf("top-level rule depth = %d, want 0", d)
	}
}

func TestOutlineCSSDocComment(t *testing.T) {
	fm := outlineCss(t, "theme.css", cssSource)

	body := find(t, fm, "body")
	if body.Doc != "base typography" {
		t.Errorf("body doc = %q, want the comment text with markers stripped", body.Doc)
	}
	if body.StartLine != 4 {
		t.Errorf("body StartLine = %d, want 4 (doc comment included)", body.StartLine)
	}
}

// Declarations are the field-level noise every other language in this package
// also skips: the rule's range already covers them.
func TestOutlineCSSSkipsDeclarations(t *testing.T) {
	fm := outlineCss(t, "theme.css", cssSource)

	for _, s := range fm.Symbols {
		if s.Kind != "rule" && s.Kind != "media" && s.Kind != "keyframes" && s.Kind != "import" && s.Kind != "supports" && s.Kind != "at" {
			t.Errorf("unexpected kind %q on %q", s.Kind, s.Name)
		}
		for _, name := range []string{"font-family", "color", "padding", "opacity", "display", "max-width"} {
			if s.Name == name {
				t.Errorf("declaration %q leaked into the outline", name)
			}
		}
	}
}

func TestOutlineCSSCharsetIsNotCaptured(t *testing.T) {
	fm := outlineCss(t, "theme.css", cssSource)

	for _, s := range fm.Symbols {
		if s.Signature == `@charset "UTF-8"` {
			t.Errorf("@charset outlined; it is not a declaration a reader jumps to")
		}
	}
}
