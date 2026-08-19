package codemap

import "testing"

const swSource = `import Foundation
import UIKit

/// A widget on the board.
///
/// Second paragraph.
@objc public final class Widget: NSObject, Identifiable {
    public let id: UUID
    private var name: String = ""

    public init(id: UUID) { self.id = id }

    deinit { }

    /// Render it.
    public func render() -> String { name }

    @available(iOS 13, *)
    private static func make() -> Widget { Widget(id: UUID()) }

    enum State { case idle, busy }
}

public struct Point { let x: Int }

public protocol Renderable {
    associatedtype Output
    var title: String { get }
    func render() -> Output
}

extension Widget: CustomStringConvertible {
    public var description: String { name }
    func helper() {}
}

public enum Kind {
    case small
    case large
    func label() -> String { "x" }
}

public typealias Pair = (Int, String)

func topLevel(_ x: Int) -> Int { x }

actor Counter { func bump() {} }
`

func outlineSw(t *testing.T, name, src string) *FileMap {
	t.Helper()
	fm, err := Outline(write(t, name, src))
	if err != nil {
		t.Fatal(err)
	}
	if fm.Fallback {
		t.Fatalf("%s fell back to the heuristic scanner instead of using the Swift grammar", name)
	}
	if fm.ParseError {
		t.Errorf("%s reported a parse error on valid source", name)
	}
	if fm.Lang != "swift" {
		t.Errorf("Lang = %q, want swift", fm.Lang)
	}
	return fm
}

// findKind is find() narrowed by kind. Swift needs it: a type and its extension
// share a name, and a protocol requirement shares a name with the method that
// satisfies it.
func findKind(t *testing.T, fm *FileMap, name, kind string) *Symbol {
	t.Helper()
	for _, s := range fm.Symbols {
		if s.Name == name && s.Kind == kind {
			return s
		}
	}
	t.Fatalf("no %s named %q in outline; got %v", kind, name, names(fm))
	return nil
}

// class, struct, enum, actor and extension are one grammar node told apart by an
// anonymous declaration_kind token. Matching that token keeps them distinct.
func TestOutlineSwiftDeclarationKinds(t *testing.T) {
	fm := outlineSw(t, "W.swift", swSource)

	for _, tc := range []struct{ name, kind string }{
		{"Widget", "class"},
		{"Point", "struct"},
		{"Kind", "enum"},
		{"Counter", "actor"},
		{"Widget", "extension"},
		{"Renderable", "protocol"},
		{"Pair", "type"},
		{"topLevel", "func"},
	} {
		findKind(t, fm, tc.name, tc.kind)
	}
}

// @def.type routes through the branch of signatureFor written for Go's grouped
// `type ( ... )` block, which slices the whole node. A type declared on one line
// would swallow its own body, so the declaration kinds must stay off that path.
func TestOutlineSwiftSingleLineTypeKeepsBodyOut(t *testing.T) {
	fm := outlineSw(t, "W.swift", swSource)

	for _, tc := range []struct{ name, kind, wantSig string }{
		{"Point", "struct", "public struct Point"},
		{"Counter", "actor", "actor Counter"},
		{"State", "enum", "enum State"},
	} {
		if got := findKind(t, fm, tc.name, tc.kind).Signature; got != tc.wantSig {
			t.Errorf("%s signature = %q, want %q", tc.name, got, tc.wantSig)
		}
	}
}

// Swift marks a doc comment with a third slash, the same as Rust.
func TestOutlineSwiftDocComments(t *testing.T) {
	fm := outlineSw(t, "W.swift", swSource)

	if got := findKind(t, fm, "Widget", "class").Doc; got != "A widget on the board. Second paragraph." {
		t.Errorf("Widget doc = %q", got)
	}
	if got := findKind(t, fm, "render", "method").Doc; got != "Render it." {
		t.Errorf("render doc = %q", got)
	}
	// Line 4 opens the doc, 7 is the declaration.
	if got := findKind(t, fm, "Widget", "class").StartLine; got != 4 {
		t.Errorf("Widget StartLine = %d, want 4 (doc comment included)", got)
	}
}

// Swift parses `@objc public final` into a modifiers node inside the
// declaration, so an attribute lands in the range and the signature without the
// walking that Python's decorators and Rust's attributes need.
func TestOutlineSwiftAttributesAreInsideTheDeclaration(t *testing.T) {
	fm := outlineSw(t, "W.swift", swSource)

	widget := findKind(t, fm, "Widget", "class")
	if widget.Signature != "@objc public final class Widget: NSObject, Identifiable" {
		t.Errorf("Widget signature = %q, want the attribute and modifiers kept", widget.Signature)
	}

	make := findKind(t, fm, "make", "method")
	// Line 18 is @available, 19 the func.
	if make.StartLine != 18 {
		t.Errorf("make StartLine = %d, want 18 (attribute on its own line)", make.StartLine)
	}
	if make.Signature != "@available(iOS 13, *) private static func make() -> Widget" {
		t.Errorf("make signature = %q", make.Signature)
	}
}

// Methods are structure and so is an initialiser; a deinit is how the type is
// torn down. Stored properties and enum cases are not.
func TestOutlineSwiftMembers(t *testing.T) {
	fm := outlineSw(t, "W.swift", swSource)

	for _, want := range []string{"init", "render", "make", "helper", "label", "bump"} {
		if got := findKind(t, fm, want, "method").Depth; got != 1 {
			t.Errorf("%s depth = %d, want 1 (inside its type)", want, got)
		}
	}
	// deinit binds no name of its own.
	found := false
	for _, s := range fm.Symbols {
		if s.Signature == "deinit" && s.Kind == "method" {
			found = true
		}
	}
	if !found {
		t.Error("deinit missing from the outline")
	}

	for _, unwanted := range []string{"id", "name", "description", "small", "large", "x"} {
		for _, s := range fm.Symbols {
			if s.Name == unwanted {
				t.Errorf("outline lists %q (%s); stored properties and enum cases are not structure", unwanted, s.Kind)
			}
		}
	}
}

// A protocol body states requirements rather than implementation, so a property
// requirement is as much a part of the contract as a method. A Swift protocol is
// often mostly `var x: T { get }`, and dropping those would gut the outline.
func TestOutlineSwiftProtocolRequirements(t *testing.T) {
	fm := outlineSw(t, "W.swift", swSource)

	if got := findKind(t, fm, "Output", "type").Signature; got != "associatedtype Output" {
		t.Errorf("associatedtype signature = %q", got)
	}
	if got := findKind(t, fm, "var title", "property").Signature; got != "var title: String { get }" {
		t.Errorf("property requirement signature = %q", got)
	}
	var reqs int
	for _, s := range fm.Symbols {
		if s.Depth == 1 && s.Signature == "func render() -> Output" {
			reqs++
		}
	}
	if reqs != 1 {
		t.Errorf("got %d method requirements matching render, want 1", reqs)
	}
}

// A type declared inside another is structure, not noise — `enum State` inside a
// view model is exactly the shape a reader is looking for.
func TestOutlineSwiftNestedType(t *testing.T) {
	fm := outlineSw(t, "W.swift", swSource)

	state := findKind(t, fm, "State", "enum")
	if state.Depth != 1 {
		t.Errorf("State depth = %d, want 1 (nested in Widget)", state.Depth)
	}
}

// An extension is its own entry, distinct from the type it extends, and carries
// the methods declared in it.
func TestOutlineSwiftExtension(t *testing.T) {
	fm := outlineSw(t, "W.swift", swSource)

	ext := findKind(t, fm, "Widget", "extension")
	if ext.Signature != "extension Widget: CustomStringConvertible" {
		t.Errorf("extension signature = %q", ext.Signature)
	}
	if got := findKind(t, fm, "helper", "method").Depth; got != 1 {
		t.Errorf("helper depth = %d, want 1 (inside the extension)", got)
	}
}

// Swift states each import on its own line, so without merging a file with
// twenty of them spends twenty outline lines saying nothing.
func TestOutlineSwiftMergesImports(t *testing.T) {
	fm := outlineSw(t, "W.swift", swSource)

	imports := 0
	for _, s := range fm.Symbols {
		if s.Kind == "import" {
			imports++
			if s.StartLine != 1 || s.EndLine != 2 {
				t.Errorf("import block = %d-%d, want 1-2", s.StartLine, s.EndLine)
			}
		}
	}
	if imports != 1 {
		t.Errorf("got %d import symbols, want 1 merged block", imports)
	}
}
