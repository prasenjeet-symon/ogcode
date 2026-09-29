package codemap

import (
	"strings"
	"testing"
)

const validPage = `<!doctype html>
<html>
<head>
  <style>
    .a { color: red; }
    @media (max-width: 600px) { .a { color: blue; } }
  </style>
  <script type="application/ld+json">{"@context": "https://schema.org", "@type": "Thing"}</script>
  <script type="importmap">{"imports": {"x": "/x.js"}}</script>
</head>
<body>
  <script src="/app.js"></script>
  <script>
    const el = document.querySelector('.a');
    el.addEventListener('click', () => { el.textContent = '<b>hi</b>'; });
  </script>
  <script type="module">import { x } from 'x'; x();</script>
  <script type="text/template"><div>{ this is not code</script>
</body>
</html>
`

// The HTML grammar reads a script or style body as opaque text, so a page with
// broken JavaScript used to check as clean. Each body is now checked as its own
// language, and a valid page stays clean.
func TestCheckHTMLEmbeddedCode(t *testing.T) {
	wantClean(t, check(t, "index.html", validPage))

	// Positions are the file's, not the body's: line 4 of the page, and on
	// the first line the column counts the markup before the body too.
	cases := []struct {
		name, page string
		line, col  int
	}{
		{"broken script", "<html>\n<body>\n<script>\n  const x = ;\n</script>\n</body>\n</html>\n", 4, 11},
		{"broken module", "<script type=\"module\">\nimport { a from 'a';\n</script>\n", 2, 0},
		{"broken style", "<style>\n.a { color: red;\n.b { }\n</style>\n", 3, 7},
		{"broken JSON-LD", "<script type=\"application/ld+json\">\n{\"a\": 1,, \"b\": 2}\n</script>\n", 2, 9},
		{"first-line offset", "<div></div><script>const = 1;</script>\n", 1, -20},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := check(t, "page.html", c.page)
			if res.OK() {
				t.Fatalf("broken embedded code reported clean")
			}
			d := res.Diagnostics[0]
			// A negative col is a lower bound: past the markup before the body.
			colOK := c.col == 0 || d.Column == c.col || (c.col < 0 && d.Column >= -c.col)
			if d.Line != c.line || !colOK {
				t.Errorf("first diagnostic at %d:%d (%s), want %d:%d", d.Line, d.Column, d.Message, c.line, c.col)
			}
			if d.Source == "" {
				t.Error("diagnostic carries no source line")
			}
		})
	}
}

// A page that is really a server template holds syntax only the template
// engine understands. Its scripts are left alone rather than flagged.
func TestCheckHTMLSkipsTemplatedScripts(t *testing.T) {
	for _, page := range []string{
		"<script>\n  const user = {{ user|tojson }};\n</script>\n",
		"<script>\n  {% if debug %}console.log(1);{% endif %}\n</script>\n",
		"<script>\n  var id = <%= @id %>;\n</script>\n",
		"<style>\n  .a { color: {{ theme.color }}; }\n</style>\n",
	} {
		if res := check(t, "view.html", page); !res.OK() {
			t.Errorf("templated block flagged: %+v\n%s", res.Diagnostics, page)
		}
	}
}

func TestEmbeddedKind(t *testing.T) {
	for _, c := range []struct{ el, typ, want string }{
		{"script_element", "", "js"},
		{"script_element", "text/javascript; charset=utf-8", "js"},
		{"script_element", "MODULE", "js"},
		{"script_element", "application/ld+json", "json"},
		{"script_element", "text/x-handlebars-template", ""},
		{"style_element", "", "css"},
		{"style_element", "text/less", ""},
	} {
		if got := embeddedKind(c.el, c.typ); got != c.want {
			t.Errorf("embeddedKind(%s, %q) = %q, want %q", c.el, c.typ, got, c.want)
		}
	}
	if !strings.Contains(validPage, "text/template") {
		t.Fatal("fixture lost its non-code script")
	}
}
