package codemap

import "testing"

const dartSource = `import 'dart:async';
import 'package:flutter/material.dart';

/// The app's entry point.
Future<void> main() async {
  runApp(const MyApp());
}

int add(int a, int b) => a + b;

/// A counter screen.
@immutable
class CounterPage extends StatefulWidget {
  const CounterPage({super.key, required this.title});

  CounterPage.named(this.title);

  factory CounterPage.build() => CounterPage(title: 'x');

  final String title;

  /// The current label.
  String get label => title;

  set label(String v) => title = v;

  @override
  State<CounterPage> createState() {
    return _CounterPageState();
  }
}

abstract class Repo<T> {
  Future<T> fetch(String id);
}

mixin Loggable on Object {
  void log(String m) => print(m);
}

extension StringX on String {
  String get shouted => toUpperCase();
}

enum Status {
  idle,
  busy;

  bool get isBusy => this == Status.busy;
}

typedef Callback = void Function(int);
`

func outlineDart(t *testing.T, name, src string) *FileMap {
	t.Helper()
	fm, err := Outline(write(t, name, src))
	if err != nil {
		t.Fatal(err)
	}
	if fm.Fallback {
		t.Fatalf("%s fell back to the heuristic scanner instead of using the Dart grammar", name)
	}
	if fm.ParseError {
		t.Errorf("%s reported a parse error on valid source", name)
	}
	if fm.Lang != "dart" {
		t.Errorf("Lang = %q, want dart", fm.Lang)
	}
	return fm
}

func TestOutlineDartTopLevelDeclarations(t *testing.T) {
	fm := outlineDart(t, "counter.dart", dartSource)

	for _, tc := range []struct{ name, kind string }{
		{"main", "func"},
		{"add", "func"},
		{"CounterPage", "class"},
		{"Repo", "class"},
		{"Loggable", "mixin"},
		{"StringX", "extension"},
		{"Status", "enum"},
		{"Callback", "type"},
	} {
		findKind(t, fm, tc.name, tc.kind)
	}
}

// The reason Dart needed machinery no other grammar did. A function's body is
// the sibling that follows its signature, not a child of it, so the captured
// node ends at the closing parenthesis. A range stopping there would send a
// reader to a signature with no code under it — the one thing the range exists
// to deliver.
func TestOutlineDartRangeCoversTheBody(t *testing.T) {
	fm := outlineDart(t, "counter.dart", dartSource)

	// Doc on 4, signature on 5, body closing on 7.
	if got := findKind(t, fm, "main", "func"); got.StartLine != 4 || got.EndLine != 7 {
		t.Errorf("main range = %d-%d, want 4-7 (doc comment and body)", got.StartLine, got.EndLine)
	}
	// An expression body on the signature's own line ends where it ends.
	if got := findKind(t, fm, "add", "func"); got.StartLine != 9 || got.EndLine != 9 {
		t.Errorf("add range = %d-%d, want 9-9", got.StartLine, got.EndLine)
	}
	// A method is captured at the inner function_signature, so finding the body
	// means climbing back out to the method_signature it sits in.
	if got := findKind(t, fm, "createState", "method"); got.EndLine != 30 {
		t.Errorf("createState EndLine = %d, want 30 (the body's closing brace)", got.EndLine)
	}
}

// An abstract method has no body, and what follows it is the next declaration.
// The kind check is what keeps the range from swallowing it.
func TestOutlineDartAbstractMethodStopsAtItsSignature(t *testing.T) {
	fm := outlineDart(t, "repo.dart", `abstract class Repo {
  Future<int> fetch(String id);
  void save(int item);
}
`)

	fetch := findKind(t, fm, "fetch", "method")
	if fetch.StartLine != 2 || fetch.EndLine != 2 {
		t.Errorf("fetch range = %d-%d, want 2-2 — it has no body to cover", fetch.StartLine, fetch.EndLine)
	}
	findKind(t, fm, "save", "method")
}

// Dart annotates a class member with a preceding sibling, as Rust does an item.
// The member is captured one level in, though, so the walk for the annotation
// has to start from the node it is actually a sibling of.
func TestOutlineDartAnnotationsWidenTheRange(t *testing.T) {
	fm := outlineDart(t, "counter.dart", dartSource)

	// @override on 27, signature on 28.
	if got := findKind(t, fm, "createState", "method").StartLine; got != 27 {
		t.Errorf("createState StartLine = %d, want 27 (@override included)", got)
	}
	// A class annotation is a child of the declaration rather than a sibling,
	// so it lands in the signature as well as the range.
	page := findKind(t, fm, "CounterPage", "class")
	if page.Signature != "@immutable class CounterPage extends StatefulWidget" {
		t.Errorf("CounterPage signature = %q, want the annotation kept", page.Signature)
	}
	if page.StartLine != 11 {
		t.Errorf("CounterPage StartLine = %d, want 11 (doc above the annotation)", page.StartLine)
	}
	if page.Doc != "A counter screen." {
		t.Errorf("CounterPage doc = %q", page.Doc)
	}
}

// A getter and a setter are how Dart spells a property. `get` and `set` on one
// name are two declarations, so they keep distinct kinds — an outline calling
// both "method" would read as a duplicate entry.
func TestOutlineDartGettersAndSettersKeepTheirKinds(t *testing.T) {
	fm := outlineDart(t, "counter.dart", dartSource)

	var getter, setter *Symbol
	for _, s := range fm.Symbols {
		if s.Name != "label" {
			continue
		}
		switch s.Kind {
		case "getter":
			getter = s
		case "setter":
			setter = s
		}
	}
	if getter == nil || setter == nil {
		t.Fatalf("expected a getter and a setter named label; got %v", names(fm))
	}
	if getter.Doc != "The current label." {
		t.Errorf("label getter doc = %q", getter.Doc)
	}
	if setter.Signature != "set label(String v)" {
		t.Errorf("label setter signature = %q", setter.Signature)
	}
}

// Constructors come in three spellings the grammar parses three ways, and two
// of them bind no name field at all.
func TestOutlineDartConstructors(t *testing.T) {
	fm := outlineDart(t, "counter.dart", dartSource)

	var ctors []*Symbol
	for _, s := range fm.Symbols {
		if s.Kind == "method" && s.Name == "CounterPage" {
			ctors = append(ctors, s)
		}
	}
	if len(ctors) != 3 {
		t.Fatalf("found %d constructors, want 3 (const, named, factory); got %v", len(ctors), names(fm))
	}
	// Each is named for its class rather than for the part after the dot, so a
	// named constructor and the plain one answer to the same name.
	for _, want := range []string{
		"const CounterPage({super.key, required this.title})",
		"CounterPage.named(this.title)",
		"factory CounterPage.build()",
	} {
		found := false
		for _, c := range ctors {
			if c.Signature == want {
				found = true
			}
		}
		if !found {
			t.Errorf("no constructor with signature %q", want)
		}
	}
}

// class_body is the body of a mixin as well as of a class, and an extension and
// an enhanced enum each have a body kind of their own.
func TestOutlineDartMembersOfEveryBodyKind(t *testing.T) {
	fm := outlineDart(t, "counter.dart", dartSource)

	for _, tc := range []struct {
		name, kind string
		depth      int
	}{
		{"log", "method", 1},     // mixin, in a class_body
		{"shouted", "getter", 1}, // extension, in an extension_body
		{"isBusy", "getter", 1},  // enhanced enum, in an enum_body
		{"fetch", "method", 1},   // abstract class
	} {
		s := findKind(t, fm, tc.name, tc.kind)
		if s.Depth != tc.depth {
			t.Errorf("%s Depth = %d, want %d", tc.name, s.Depth, tc.depth)
		}
	}
}

// Fields stay out, enum constants stay out, and a function local to another
// function's body stays out.
func TestOutlineDartSkipsFieldsAndLocals(t *testing.T) {
	fm := outlineDart(t, "counter.dart", dartSource)
	for _, s := range fm.Symbols {
		if s.Name == "title" || s.Name == "idle" || s.Name == "busy" {
			t.Errorf("%s is a field or enum constant and should stay out of the outline", s.Name)
		}
	}

	local := outlineDart(t, "local.dart", `void outer() {
  void inner() {}
  var x = 1;
}
`)
	for _, s := range local.Symbols {
		if s.Name == "inner" || s.Name == "x" {
			t.Errorf("%s is local to a function body and should stay out of the outline", s.Name)
		}
	}
}

// Dart splits documentation from ordinary comment at the grammar level rather
// than line from block. Both kinds have to be listed, or a declaration
// documented with a plain // loses it.
func TestOutlineDartBothCommentKinds(t *testing.T) {
	fm := outlineDart(t, "docs.dart", `/// A doc comment.
void documented() {}

// An ordinary comment, which the analyzer does not treat as a doc.
void plain() {}

/** A block doc. */
void blocky() {}
`)

	for _, tc := range []struct{ name, doc string }{
		{"documented", "A doc comment."},
		{"plain", "An ordinary comment, which the analyzer does not treat as a doc."},
		{"blocky", "A block doc."},
	} {
		if got := findKind(t, fm, tc.name, "func").Doc; got != tc.doc {
			t.Errorf("%s doc = %q, want %q", tc.name, got, tc.doc)
		}
	}
}

// The trailing-body rule is opt-in per grammar. Every other language states a
// body inside the declaration, and a stray sibling of some other kind must
// never be pulled into a range — so the field stays empty everywhere else, and
// buildSymbol's extension cannot fire for them.
func TestTrailingBodyIsDartOnly(t *testing.T) {
	for ext, lang := range registry {
		if lang.name == "dart" {
			if lang.trailingBodyKind != "function_body" {
				t.Errorf("%s: trailingBodyKind = %q, want function_body", ext, lang.trailingBodyKind)
			}
			continue
		}
		if lang.trailingBodyKind != "" {
			t.Errorf("%s (%s): trailingBodyKind = %q, but this grammar nests its bodies",
				ext, lang.name, lang.trailingBodyKind)
		}
	}
}
