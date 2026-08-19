package codemap

import "testing"

const javaSource = `package com.example.app;

import java.util.List;
import java.util.Map;

/**
 * A widget on the board.
 *
 * Second paragraph.
 */
@Entity
public final class Widget extends Base implements Comparable<Widget> {
    private static final int MAX = 32;
    private String name;

    public Widget(String name) { this.name = name; }

    /** Renders it. */
    @Override
    public String render() { return name; }

    private static Widget make() { return new Widget("x"); }

    static { System.out.println("init"); }

    static class Builder { Widget build() { return null; } }
}

interface Renderable {
    String render();
    default String label() { return "x"; }
}

enum Kind {
    SMALL, LARGE;
    String label() { return name(); }
}

record Point(int x, int y) {
    Point { }
    int sum() { return x + y; }
}

@interface Marker { String value(); }
`

func outlineJava(t *testing.T, name, src string) *FileMap {
	t.Helper()
	fm, err := Outline(write(t, name, src))
	if err != nil {
		t.Fatal(err)
	}
	if fm.Fallback {
		t.Fatalf("%s fell back to the heuristic scanner instead of using the Java grammar", name)
	}
	if fm.ParseError {
		t.Errorf("%s reported a parse error on valid source", name)
	}
	if fm.Lang != "java" {
		t.Errorf("Lang = %q, want java", fm.Lang)
	}
	return fm
}

// atLine returns the symbol starting on the given line. Java repeats names
// freely across types — a constructor shares its class's name, and render()
// appears on both the class and the interface it satisfies.
func atLine(t *testing.T, fm *FileMap, line int) *Symbol {
	t.Helper()
	for _, s := range fm.Symbols {
		if s.StartLine == line {
			return s
		}
	}
	t.Fatalf("no symbol starting on line %d; got %v", line, names(fm))
	return nil
}

func TestOutlineJavaTopLevelDeclarations(t *testing.T) {
	fm := outlineJava(t, "Widget.java", javaSource)

	for _, tc := range []struct{ name, kind string }{
		{"Widget", "class"},
		{"Renderable", "interface"},
		{"Kind", "enum"},
		{"Point", "record"},
		{"Marker", "annotation"},
	} {
		findKind(t, fm, tc.name, tc.kind)
	}

	pkg := atLine(t, fm, 1)
	if pkg.Kind != "package" || pkg.Signature != "package com.example.app;" {
		t.Errorf("package = %q / %q", pkg.Kind, pkg.Signature)
	}
}

// Java parses `@Entity public final` into a modifiers node inside the
// declaration, so an annotation lands in the range and the signature without
// the walking that Python's decorators and Rust's attributes need.
func TestOutlineJavaAnnotationsAreInsideTheDeclaration(t *testing.T) {
	fm := outlineJava(t, "Widget.java", javaSource)

	widget := findKind(t, fm, "Widget", "class")
	if widget.Signature != "@Entity public final class Widget extends Base implements Comparable<Widget>" {
		t.Errorf("Widget signature = %q, want the annotation and modifiers kept", widget.Signature)
	}

	// Line 18 is the Javadoc, 19 the @Override, 20 the method.
	render := atLine(t, fm, 18)
	if render.Name != "render" {
		t.Fatalf("symbol at line 18 = %q, want render", render.Name)
	}
	if render.Signature != "@Override public String render()" {
		t.Errorf("render signature = %q", render.Signature)
	}
	if render.Doc != "Renders it." {
		t.Errorf("render doc = %q", render.Doc)
	}
}

// Javadoc is a block comment, so the doc walk has to strip the leading star on
// each line and the closing marker without leaving a stray slash behind.
func TestOutlineJavaJavadoc(t *testing.T) {
	fm := outlineJava(t, "Widget.java", javaSource)

	widget := findKind(t, fm, "Widget", "class")
	if widget.Doc != "A widget on the board. Second paragraph." {
		t.Errorf("Widget doc = %q", widget.Doc)
	}
	// Line 6 opens the Javadoc, 11 the annotation, 12 the class.
	if widget.StartLine != 6 {
		t.Errorf("Widget StartLine = %d, want 6 (Javadoc included)", widget.StartLine)
	}
	for _, s := range fm.Symbols {
		if len(s.Doc) > 0 && s.Doc[len(s.Doc)-1] == '/' {
			t.Errorf("%s doc = %q, ends with a stray comment marker", s.Name, s.Doc)
		}
	}
}

// An interface method carries no body, and that signature is the point of the
// interface, so it is listed beside the default methods next to it.
func TestOutlineJavaInterfaceMembers(t *testing.T) {
	fm := outlineJava(t, "Widget.java", javaSource)

	abstract := atLine(t, fm, 30)
	if abstract.Signature != "String render();" {
		t.Errorf("abstract method signature = %q, want the trailing semicolon", abstract.Signature)
	}
	def := atLine(t, fm, 31)
	if def.Signature != "default String label()" {
		t.Errorf("default method signature = %q", def.Signature)
	}
	for _, s := range []*Symbol{abstract, def} {
		if s.Kind != "method" || s.Depth != 1 {
			t.Errorf("%s kind/depth = %q/%d, want method/1", s.Name, s.Kind, s.Depth)
		}
	}
}

// An enum states its methods after the constants, inside a nested
// enum_body_declarations rather than directly in the body — so a pattern
// anchored on enum_body alone would find none of them.
func TestOutlineJavaEnumMethods(t *testing.T) {
	fm := outlineJava(t, "Widget.java", javaSource)

	label := atLine(t, fm, 36)
	if label.Name != "label" || label.Kind != "method" || label.Depth != 1 {
		t.Errorf("enum method = %q / %q / depth %d, want label / method / 1", label.Name, label.Kind, label.Depth)
	}
}

// A record body is a class_body, so it needs no patterns of its own — but its
// compact constructor is a node kind that appears nowhere else.
func TestOutlineJavaRecordMembers(t *testing.T) {
	fm := outlineJava(t, "Widget.java", javaSource)

	point := findKind(t, fm, "Point", "record")
	if point.Signature != "record Point(int x, int y)" {
		t.Errorf("record signature = %q", point.Signature)
	}
	compact := atLine(t, fm, 40)
	if compact.Name != "Point" || compact.Kind != "method" {
		t.Errorf("compact constructor = %q / %q", compact.Name, compact.Kind)
	}
	sum := atLine(t, fm, 41)
	if sum.Name != "sum" || sum.Depth != 1 {
		t.Errorf("sum = %q depth %d, want sum depth 1", sum.Name, sum.Depth)
	}
}

// A static nested class is structure, and Java leans on the shape heavily —
// a Builder is the canonical case.
func TestOutlineJavaNestedType(t *testing.T) {
	fm := outlineJava(t, "Widget.java", javaSource)

	builder := findKind(t, fm, "Builder", "class")
	if builder.Depth != 1 {
		t.Errorf("Builder depth = %d, want 1 (nested in Widget)", builder.Depth)
	}
	build := findKind(t, fm, "build", "method")
	if build.Depth != 2 {
		t.Errorf("build depth = %d, want 2 (inside Builder, inside Widget)", build.Depth)
	}
}

// An annotation's elements are its contract, the same way an interface's
// methods are.
func TestOutlineJavaAnnotationType(t *testing.T) {
	fm := outlineJava(t, "Widget.java", javaSource)

	marker := findKind(t, fm, "Marker", "annotation")
	if marker.Signature != "@interface Marker" {
		t.Errorf("annotation signature = %q", marker.Signature)
	}
	value := findKind(t, fm, "value", "method")
	if value.Signature != "String value();" || value.Depth != 1 {
		t.Errorf("annotation element = %q depth %d", value.Signature, value.Depth)
	}
}

// Fields and enum constants are not declarations worth a line: the type's own
// range already covers them. A static initialiser binds no name at all, so a
// line for it would read as a blank entry.
func TestOutlineJavaSkipsFieldsConstantsAndInitialisers(t *testing.T) {
	fm := outlineJava(t, "Widget.java", javaSource)

	for _, unwanted := range []string{"MAX", "name", "SMALL", "LARGE"} {
		for _, s := range fm.Symbols {
			if s.Name == unwanted {
				t.Errorf("outline lists %q (%s); fields and enum constants are not structure", unwanted, s.Kind)
			}
		}
	}
	for _, s := range fm.Symbols {
		if s.StartLine == 24 {
			t.Errorf("outline lists the static initialiser at line 24 as %q/%q", s.Kind, s.Signature)
		}
	}
}

// Java states each import on its own line, so without merging a file with
// twenty of them spends twenty outline lines saying nothing.
func TestOutlineJavaMergesImports(t *testing.T) {
	fm := outlineJava(t, "Widget.java", javaSource)

	imports := 0
	for _, s := range fm.Symbols {
		if s.Kind == "import" {
			imports++
			if s.StartLine != 3 || s.EndLine != 4 {
				t.Errorf("import block = %d-%d, want 3-4", s.StartLine, s.EndLine)
			}
		}
	}
	if imports != 1 {
		t.Errorf("got %d import symbols, want 1 merged block", imports)
	}
}
