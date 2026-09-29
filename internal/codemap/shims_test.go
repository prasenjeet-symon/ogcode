package codemap

import (
	"strings"
	"testing"
)

// Valid syntax the grammars predate must not read as an error: an agent told
// its correct edit broke the file will rewrite correct code.
func TestCheckAcceptsSyntaxTheGrammarsPredate(t *testing.T) {
	for name, c := range map[string]struct{ path, src string }{
		"css nesting selector":        {"a.css", ".a { color: red; &:hover { color: blue; } .b & { margin: 0; } }\n"},
		"css named container":         {"a.css", "@container card (min-width: 400px) { .x { display: grid; } }\n"},
		"css container in media":      {"a.css", "@media (min-width: 901px) {\n  @container pr-d (min-width: 700px) {\n    .x { display: grid; }\n  }\n}\n"},
		"css container in a rule":     {"a.css", ".a { @container (min-width: 1px) { color: red; } }\n"},
		"css style query":             {"a.css", "@container style(--x: 1) { .a { color: red; } }\n"},
		"css range syntax":            {"a.css", "@container not (width > 400px) { .a { color: red; } }\n@media (width >= 600px) { .b { color: red; } }\n"},
		"css decimal keyframes":       {"a.css", "@keyframes k { 22.5% { opacity: 0; } 57.5%, 60% { opacity: 1; } }\n"},
		"css url to svg element":      {"a.css", ".a { fill: url(#grad); mask: url( #m ); }\n"},
		"ts export type star":         {"a.ts", "export type * from './types';\nexport type * as T from './t';\n"},
		"rust safe items":             {"a.rs", "unsafe extern \"C\" {\n    pub safe fn abs(x: i32) -> i32;\n    safe static N: i32;\n}\n"},
		"java import module":          {"A.java", "import module java.base;\nclass A {}\n"},
		"go without a final newline":  {"a.go", "package p\n\nvar X int"},
		"html page with modern style": {"a.html", "<style>\n.a { &:hover { color: red; } }\n@container c (min-width: 1px) { .x { color: red; } }\n@keyframes k { 22.5% { opacity: 0; } }\n</style>\n"},
	} {
		t.Run(name, func(t *testing.T) {
			wantClean(t, check(t, c.path, c.src))
		})
	}
}

// A shim rewrites only the construct it targets: a real error beside it is
// still reported, at its own position, quoting the file's own text.
func TestShimsLeaveRealErrorsVisible(t *testing.T) {
	res := check(t, "a.css", "@container card (min-width: 400px) { .x { display: grid; } }\n.b { color: red\n")
	if res.OK() {
		t.Fatal("unclosed rule next to a shimmed container query reported clean")
	}
	if d := res.Diagnostics[0]; d.Line != 2 {
		t.Errorf("error reported on line %d (%s), want line 2", d.Line, d.Message)
	}

	res = check(t, "a.ts", "export type * from './types';\nconst x = ;\n")
	if res.OK() || res.Diagnostics[0].Line != 2 {
		t.Errorf("diagnostics = %+v, want the error on line 2", res.Diagnostics)
	}

	// Positions and quoted text come from the file, not from the shim.
	res = check(t, "a.css", ".a { @container named (min-width: 1px) { color: red; } }\n.b { color: red; ]\n")
	for _, d := range res.Diagnostics {
		if strings.Contains(d.Message, "supports") || strings.Contains(d.Source, "supports") {
			t.Errorf("diagnostic shows the shim's rewrite instead of the file: %+v", d)
		}
	}
}

// Every shim keeps the length, so offsets into the shimmed bytes are offsets
// into the file.
func TestShimsPreserveLength(t *testing.T) {
	for lang, shim := range grammarShims {
		src := []byte("@container a (width > 1px) { .b & { fill: url(#x); } } @keyframes k { 22.5% {} }\n" +
			"export type * from 'x'\nsafe fn f();\nimport module java.base;\n")
		if got := shim(src); len(got) != len(src) {
			t.Errorf("%s shim changed the length: %d -> %d", lang, len(src), len(got))
		}
	}
}
