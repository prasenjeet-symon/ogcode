package codemap

import "testing"

// C and C++ are outlined but never syntax-checked: tree-sitter parses them
// without running the preprocessor. These pin what the outline covers.

const cSource = `#include <stdio.h>
#include "util.h"

#ifndef DEMO_H
#define DEMO_H

#define MAX_ITEMS 64
#define SQUARE(x) \
	((x) * (x))

/* Adds two numbers. */
int add(int a, int b);

/* Counts the calls. */
static int counter = 0, other;

struct point {
	int x;
	int y;
};

typedef struct {
	int a;
} pair_t;

enum color { RED, GREEN };

/* Names the thing. */
static char *name(void)
{
	int local = 1;
#ifdef DEBUG
	int debug_only = 2;
#endif
	return 0;
}

#ifdef __linux__
void linux_only(void) {}
#endif

#endif
`

func TestOutlineC(t *testing.T) {
	fm, err := Outline(write(t, "demo.c", cSource))
	if err != nil {
		t.Fatal(err)
	}
	if fm.Lang != "c" || fm.Fallback || fm.ParseError {
		t.Fatalf("lang=%q fallback=%v parseError=%v, want c, parsed cleanly", fm.Lang, fm.Fallback, fm.ParseError)
	}

	cases := []struct {
		name       string
		start, end int
		sig, doc   string
	}{
		{"MAX_ITEMS", 7, 7, "#define MAX_ITEMS 64", ""},
		// The continuation backslash is not part of the signature.
		{"SQUARE", 8, 9, "#define SQUARE(x)", ""},
		{"add", 11, 12, "int add(int a, int b);", "Adds two numbers."},
		{"counter", 14, 15, "static int counter = 0, other;", "Counts the calls."},
		{"point", 17, 20, "struct point", ""},
		// A typedef names its type last, so the name is carried up.
		{"pair_t", 22, 24, "typedef struct … pair_t", ""},
		{"color", 26, 26, "enum color", ""},
		{"name", 28, 36, "static char *name(void)", "Names the thing."},
		// Inside an #ifdef is still file scope.
		{"linux_only", 39, 39, "void linux_only(void)", ""},
	}
	for _, c := range cases {
		s := find(t, fm, c.name)
		if s.StartLine != c.start || s.EndLine != c.end || s.Signature != c.sig || s.Doc != c.doc {
			t.Errorf("%s = %d-%d %q doc %q, want %d-%d %q doc %q",
				c.name, s.StartLine, s.EndLine, s.Signature, s.Doc, c.start, c.end, c.sig, c.doc)
		}
	}

	for _, s := range fm.Symbols {
		switch s.Name {
		case "DEMO_H":
			t.Error("the include guard's own #define is listed")
		case "local", "debug_only":
			t.Errorf("function-local %q is listed — a conditional inside a function body is not file scope", s.Name)
		}
	}
}

const cppSource = `#include <string>

namespace app {

/// A widget on screen.
class Widget : public Base {
public:
	explicit Widget(int n);
	~Widget();
	/// Draws the widget.
	void draw() const override;
	int size() const { return n_; }
	static Widget* make();
	Widget& operator=(Widget&&) noexcept;

	struct Options {
		bool visible;
	};

private:
	int n_;
};

template <typename T>
T maxOf(T a, T b) {
	return a > b ? a : b;
}

void Widget::draw() const {}

using Id = int;

}  // namespace app

extern "C" {
int c_entry(void);
}
`

func TestOutlineCPP(t *testing.T) {
	fm, err := Outline(write(t, "demo.cpp", cppSource))
	if err != nil {
		t.Fatal(err)
	}
	if fm.Lang != "c++" || fm.ParseError {
		t.Fatalf("lang=%q parseError=%v, want c++ parsed cleanly", fm.Lang, fm.ParseError)
	}

	cases := []struct {
		name       string
		start, end int
		depth      int
		sig        string
	}{
		{"app", 3, 33, 0, "namespace app"},
		{"Widget", 5, 22, 1, "class Widget : public Base"},
		{"~Widget", 9, 9, 2, "~Widget();"},
		{"draw", 10, 11, 2, "void draw() const override;"},
		{"size", 12, 12, 2, "int size() const"},
		{"make", 13, 13, 2, "static Widget* make();"},
		{"operator=", 14, 14, 2, "Widget& operator=(Widget&&) noexcept;"},
		{"Options", 16, 18, 2, "struct Options"},
		// The range starts at the template line, above the name.
		{"maxOf", 24, 27, 1, "T maxOf(T a, T b)"},
		{"Widget::draw", 29, 29, 1, "void Widget::draw() const"},
		{"Id", 31, 31, 1, "using Id = int;"},
		{"c_entry", 36, 36, 0, "int c_entry(void);"},
	}
	for _, c := range cases {
		s := find(t, fm, c.name)
		if s.StartLine != c.start || s.EndLine != c.end || s.Depth != c.depth || s.Signature != c.sig {
			t.Errorf("%s = %d-%d depth %d %q, want %d-%d depth %d %q",
				c.name, s.StartLine, s.EndLine, s.Depth, s.Signature, c.start, c.end, c.depth, c.sig)
		}
	}
	if doc := find(t, fm, "draw").Doc; doc != "Draws the widget." {
		t.Errorf("draw doc = %q", doc)
	}
	for _, s := range fm.Symbols {
		if s.Name == "n_" {
			t.Error("a data member is listed; the class's range covers fields")
		}
	}
}

// A header is read as C++, which also parses C headers.
func TestOutlineHeaderIsCPP(t *testing.T) {
	fm, err := Outline(write(t, "demo.h", "#pragma once\nstruct S { int a; };\nint f(struct S *s);\n"))
	if err != nil {
		t.Fatal(err)
	}
	if fm.Lang != "c++" {
		t.Fatalf("lang = %q, want c++", fm.Lang)
	}
	find(t, fm, "S")
	find(t, fm, "f")
}

// C and C++ are for the outline only: check_syntax must leave them unchecked
// rather than report the preprocessor's constructs as syntax errors.
func TestCFamilyIsNotSyntaxChecked(t *testing.T) {
	for _, name := range []string{"x.c", "x.cpp", "x.h"} {
		if lookup(name) != nil {
			t.Errorf("%s resolves to a checked grammar", name)
		}
		if lookupForOutline(name) == nil {
			t.Errorf("%s has no outline grammar", name)
		}
	}
}
