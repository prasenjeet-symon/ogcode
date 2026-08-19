package codemap

import (
	"strings"
	"testing"
)

const tsSource = `import { createSignal } from "solid-js";
import type { Foo } from "./foo";

// Config describes the thing.
export interface Config {
	name: string;
}

export type Alias = string | number;

export enum Level { Low, High }

// Helper does a thing.
export function Helper(a: string): number {
	return 1;
}

function notExported() {}

export const Component = (props: Config) => {
	const untouched = 42;
	const inner = "also not a function";

	// handle responds to clicks.
	const handle = (e: Event) => {
		return e;
	};

	function nested() {
		return 2;
	}

	return null;
};

export class Widget {
	private id = 1;

	// render draws it.
	render(): string {
		return "";
	}

	update() {}
}
`

func outlineTS(t *testing.T, name, src string) *FileMap {
	t.Helper()
	fm, err := Outline(write(t, name, src))
	if err != nil {
		t.Fatal(err)
	}
	if fm.Fallback {
		t.Fatalf("%s fell back to the heuristic scanner instead of using a grammar", name)
	}
	if fm.ParseError {
		t.Errorf("%s reported a parse error on valid source", name)
	}
	return fm
}

// Most declarations in a TypeScript module are exported, and `export` wraps the
// declaration in another node. Without patterns for the wrapped form the
// outline would miss nearly the whole file.
func TestOutlineTypeScriptExportedDeclarations(t *testing.T) {
	fm := outlineTS(t, "mod.ts", tsSource)

	for name, kind := range map[string]string{
		"Config":      "interface",
		"Alias":       "type",
		"Level":       "enum",
		"Helper":      "func",
		"Component":   "const",
		"Widget":      "class",
		"notExported": "func",
	} {
		if got := find(t, fm, name).Kind; got != kind {
			t.Errorf("%s kind = %q, want %q", name, got, kind)
		}
	}
}

// The doc comment above `export function f()` is a sibling of the export
// wrapper, not of the declaration the query captured, so docStart has to climb
// to it. Without that climb every exported declaration would lose its doc.
func TestOutlineTypeScriptDocThroughExport(t *testing.T) {
	fm := outlineTS(t, "mod.ts", tsSource)

	helper := find(t, fm, "Helper")
	if helper.Doc != "Helper does a thing." {
		t.Errorf("Helper doc = %q, want the comment above its export", helper.Doc)
	}
	// Line 13 is the comment, 14 the export.
	if helper.StartLine != 13 {
		t.Errorf("Helper StartLine = %d, want 13 (doc comment included)", helper.StartLine)
	}

	if got := find(t, fm, "Config").Doc; got != "Config describes the thing." {
		t.Errorf("Config doc = %q", got)
	}
}

// JSDoc closes on a line holding only "*/", which must not survive into the doc
// text. Every block comment ends that way, so getting it wrong marks every
// documented symbol in the file.
func TestOutlineTypeScriptJSDocDropsClosingMarker(t *testing.T) {
	fm := outlineTS(t, "doc.ts", `/**
 * Resolves the display name.
 * Falls back to the id.
 */
export function getName(id: string): string {
	return id;
}
`)

	if got := find(t, fm, "getName").Doc; got != "Resolves the display name. Falls back to the id." {
		t.Errorf("getName doc = %q", got)
	}
}

// A component is one top-level arrow function holding the whole module, so its
// inner functions are the only structure worth having. Locals are not: listing
// them is the noise an outline exists to avoid.
func TestOutlineTypeScriptNestedFunctionsNotLocals(t *testing.T) {
	fm := outlineTS(t, "mod.ts", tsSource)

	for _, want := range []string{"handle", "nested"} {
		s := find(t, fm, want)
		if s.Depth != 1 {
			t.Errorf("%s Depth = %d, want 1 (nested inside Component)", want, s.Depth)
		}
	}
	for _, local := range []string{"untouched", "inner"} {
		for _, s := range fm.Symbols {
			if s.Name == local {
				t.Errorf("non-function local %q leaked into the outline", local)
			}
		}
	}
	if got := find(t, fm, "handle").Doc; got != "handle responds to clicks." {
		t.Errorf("nested function lost its doc: %q", got)
	}
}

// Methods are where a class's code lives; one-line fields are not.
func TestOutlineTypeScriptClassMembers(t *testing.T) {
	fm := outlineTS(t, "mod.ts", tsSource)

	for _, want := range []string{"render", "update"} {
		s := find(t, fm, want)
		if s.Kind != "method" {
			t.Errorf("%s kind = %q, want method", want, s.Kind)
		}
		if s.Depth != 1 {
			t.Errorf("%s Depth = %d, want 1 (inside Widget)", want, s.Depth)
		}
	}
	for _, s := range fm.Symbols {
		if s.Name == "id" {
			t.Error("class field leaked into the outline")
		}
	}
}

// TypeScript states each import separately, so without merging a module with
// twenty imports spends twenty outline lines saying nothing.
func TestOutlineTypeScriptMergesImports(t *testing.T) {
	fm := outlineTS(t, "mod.ts", tsSource)

	var imports []*Symbol
	for _, s := range fm.Symbols {
		if s.Kind == "import" {
			imports = append(imports, s)
		}
	}
	if len(imports) != 1 {
		t.Fatalf("import entries = %d, want 1 merged block", len(imports))
	}
	if imports[0].StartLine != 1 || imports[0].EndLine != 2 {
		t.Errorf("merged import range = %d-%d, want 1-2", imports[0].StartLine, imports[0].EndLine)
	}
}

// A parameter list broken across lines must still render as one signature —
// cutting at the first newline would leave `function CommandMenu(props:`.
func TestOutlineTypeScriptMultiLineSignature(t *testing.T) {
	src := "export function widget(\n\ta: string,\n\tb: number,\n) {\n\treturn 1;\n}\n"
	fm := outlineTS(t, "sig.ts", src)

	sig := find(t, fm, "widget").Signature
	for _, want := range []string{"a: string", "b: number"} {
		if !strings.Contains(sig, want) {
			t.Errorf("signature %q lost %q", sig, want)
		}
	}
	if strings.Contains(sig, "return 1") {
		t.Errorf("signature swallowed the body: %q", sig)
	}
}

// The body of `const F = () => {}` hangs off the arrow function inside the
// declarator, not off the declaration, so the signature needs the deeper lookup
// or it absorbs the whole function.
func TestOutlineTypeScriptArrowSignatureStopsAtBody(t *testing.T) {
	src := "export const F = (props: string) => {\n\tconst secret = 1;\n\treturn secret;\n};\n"
	fm := outlineTS(t, "arrow.ts", src)

	sig := find(t, fm, "F").Signature
	if strings.Contains(sig, "secret") {
		t.Errorf("arrow signature swallowed its body: %q", sig)
	}
	if !strings.Contains(sig, "props: string") {
		t.Errorf("arrow signature lost its parameters: %q", sig)
	}
}

// TSX and TypeScript are different parsers: neither reads the other's files
// cleanly, which is why both are registered.
func TestOutlineTSXParsesJSX(t *testing.T) {
	src := "export function View() {\n\treturn <div className=\"a\">hi</div>;\n}\n"

	fm := outlineTS(t, "view.tsx", src)
	if fm.Lang != "tsx" {
		t.Errorf("Lang = %q, want tsx", fm.Lang)
	}
	find(t, fm, "View")

	// The same source in a .ts file goes to the TypeScript parser, which has no
	// JSX. Recovery still yields the declaration, but the error is reported.
	plain, err := Outline(write(t, "view.ts", src))
	if err != nil {
		t.Fatal(err)
	}
	if !plain.ParseError {
		t.Error("TypeScript parser accepted JSX; the tsx registration would be pointless")
	}
}

// Plain JavaScript is routed to the TSX parser rather than adding a third
// grammar, including .js files that carry JSX.
func TestOutlineJavaScriptUsesTSX(t *testing.T) {
	fm := outlineTS(t, "app.js", "export function Alpha() {\n\treturn <div>x</div>;\n}\nconst beta = () => 1;\n")

	if fm.Lang != "tsx" {
		t.Errorf("Lang = %q, want tsx", fm.Lang)
	}
	find(t, fm, "Alpha")
	find(t, fm, "beta")
}
