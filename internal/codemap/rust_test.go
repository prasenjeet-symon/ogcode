package codemap

import "testing"

const rsSource = `//! Crate-level docs.

use std::fmt;
use std::collections::HashMap;

pub const MAX: usize = 32;
static NAME: &str = "x";

/// A widget on the board.
///
/// Second paragraph.
#[derive(Debug, Clone)]
#[serde(rename_all = "camelCase")]
pub struct Widget {
    pub id: u32,
    name: String,
}

/// What a widget can be.
#[derive(Debug)]
pub enum Kind { Small, Large }

pub trait Render {
    /// Render to a string.
    fn render(&self) -> String;

    fn label(&self) -> &str { "x" }
}

impl fmt::Display for Widget {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "w")
    }
}

impl Widget {
    /// Build a widget.
    pub fn new(id: u32) -> Self {
        Self { id, name: String::new() }
    }

    async fn load(&self) -> Result<(), Error> { Ok(()) }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn it_works() {
        assert!(true);
    }
}

pub type Pair = (u32, String);

macro_rules! shout {
    () => {};
}

/// Entry point.
pub fn main() {}
`

func outlineRs(t *testing.T, name, src string) *FileMap {
	t.Helper()
	fm, err := Outline(write(t, name, src))
	if err != nil {
		t.Fatal(err)
	}
	if fm.Fallback {
		t.Fatalf("%s fell back to the heuristic scanner instead of using the Rust grammar", name)
	}
	if fm.ParseError {
		t.Errorf("%s reported a parse error on valid source", name)
	}
	if fm.Lang != "rust" {
		t.Errorf("Lang = %q, want rust", fm.Lang)
	}
	return fm
}

func TestOutlineRustTopLevelDeclarations(t *testing.T) {
	fm := outlineRs(t, "w.rs", rsSource)

	for name, wantKind := range map[string]string{
		"MAX":    "const",
		"NAME":   "var",
		"Widget": "struct",
		"Kind":   "enum",
		"Render": "trait",
		"tests":  "mod",
		"Pair":   "type",
		"shout":  "macro",
		"main":   "func",
	} {
		if got := find(t, fm, name).Kind; got != wantKind {
			t.Errorf("%s kind = %q, want %q", name, got, wantKind)
		}
	}
}

// Rust's line_comment consumes its trailing newline, so it ends on the row the
// declaration starts on rather than the row above. Without normalising that,
// the adjacency check never matches and every documented item loses its doc.
//
// The marker is a third slash for an outer doc and a bang for an inner one;
// both are syntax, not text.
func TestOutlineRustDocComments(t *testing.T) {
	fm := outlineRs(t, "w.rs", rsSource)

	for _, tc := range []struct{ name, wantDoc string }{
		{"Widget", "A widget on the board. Second paragraph."},
		{"Kind", "What a widget can be."},
		{"render", "Render to a string."},
		{"new", "Build a widget."},
		{"main", "Entry point."},
	} {
		if got := find(t, fm, tc.name).Doc; got != tc.wantDoc {
			t.Errorf("%s doc = %q, want %q", tc.name, got, tc.wantDoc)
		}
	}
}

// Rust states an attribute beside the item rather than around it, so the doc
// comment, the attributes and the item are three runs of siblings. The range
// has to cover the attributes, and the doc walk has to start above them.
func TestOutlineRustAttributesInRange(t *testing.T) {
	fm := outlineRs(t, "w.rs", rsSource)

	// Line 9 opens the doc, 12 and 13 are the attributes, 14 the struct.
	widget := find(t, fm, "Widget")
	if widget.StartLine != 9 {
		t.Errorf("Widget StartLine = %d, want 9 (doc above two attributes)", widget.StartLine)
	}
	// Line 45 is #[cfg(test)], 46 the module.
	if got := find(t, fm, "tests").StartLine; got != 45 {
		t.Errorf("tests StartLine = %d, want 45 (attribute included)", got)
	}
	// Line 49 is #[test], 50 the fn.
	if got := find(t, fm, "it_works").StartLine; got != 49 {
		t.Errorf("it_works StartLine = %d, want 49 (attribute included)", got)
	}
}

// The signature stays the declaration itself, so an attribute never crowds out
// the name and parameters the way a long #[derive(...)] list would.
func TestOutlineRustSignatures(t *testing.T) {
	fm := outlineRs(t, "w.rs", rsSource)

	for _, tc := range []struct{ name, wantSig string }{
		{"Widget", "pub struct Widget"},
		{"Kind", "pub enum Kind"},
		{"Render", "pub trait Render"},
		{"render", "fn render(&self) -> String;"},
		{"label", "fn label(&self) -> &str"},
		{"new", "pub fn new(id: u32) -> Self"},
		{"load", "async fn load(&self) -> Result<(), Error>"},
		{"MAX", "pub const MAX: usize = 32;"},
		{"shout", "macro_rules! shout"},
		{"tests", "mod tests"},
	} {
		if got := find(t, fm, tc.name).Signature; got != tc.wantSig {
			t.Errorf("%s signature = %q, want %q", tc.name, got, tc.wantSig)
		}
	}
}

// An impl block declares no identifier of its own, so it is named by the type
// it is written for. Naming it from the trait path instead would leave trait
// impls and inherent impls on the same type answering to different names.
func TestOutlineRustImplNamedByType(t *testing.T) {
	fm := outlineRs(t, "impls.rs", `use std::fmt;

impl fmt::Display for Widget {
    fn fmt(&self) -> fmt::Result { Ok(()) }
}

impl<T: Clone> Widget<T> {
    pub fn new() -> Self {}
}
`)

	var impls []*Symbol
	for _, s := range fm.Symbols {
		if s.Kind == "impl" {
			impls = append(impls, s)
		}
	}
	if len(impls) != 2 {
		t.Fatalf("got %d impl symbols, want 2", len(impls))
	}
	if impls[0].Name != "Widget" || impls[0].Signature != "impl fmt::Display for Widget" {
		t.Errorf("trait impl = %q / %q, want Widget / impl fmt::Display for Widget", impls[0].Name, impls[0].Signature)
	}
	if impls[1].Name != "Widget<T>" || impls[1].Signature != "impl<T: Clone> Widget<T>" {
		t.Errorf("inherent impl = %q / %q", impls[1].Name, impls[1].Signature)
	}
}

// A trait's required methods have no body, and that signature is the whole
// point of the trait, so they earn a line beside the defaulted ones.
func TestOutlineRustTraitAndImplMembers(t *testing.T) {
	fm := outlineRs(t, "w.rs", rsSource)

	for _, want := range []string{"render", "label", "fmt", "new", "load"} {
		s := find(t, fm, want)
		if s.Kind != "method" {
			t.Errorf("%s kind = %q, want method", want, s.Kind)
		}
		if s.Depth != 1 {
			t.Errorf("%s depth = %d, want 1 (inside its trait or impl)", want, s.Depth)
		}
	}
}

// A module body is a declaration_list too, so patterns anchored on that alone
// would call a free function in a module a method.
func TestOutlineRustModuleContentsAreNotMethods(t *testing.T) {
	fm := outlineRs(t, "w.rs", rsSource)

	it := find(t, fm, "it_works")
	if it.Kind != "func" {
		t.Errorf("it_works kind = %q, want func — it is a free function in a module", it.Kind)
	}
	if it.Depth != 1 {
		t.Errorf("it_works depth = %d, want 1 (inside mod tests)", it.Depth)
	}
}

// Struct fields and enum variants are not declarations worth a line: the item's
// own range already covers them.
func TestOutlineRustSkipsFieldsAndVariants(t *testing.T) {
	fm := outlineRs(t, "w.rs", rsSource)

	for _, unwanted := range []string{"id", "Small", "Large"} {
		for _, s := range fm.Symbols {
			if s.Name == unwanted {
				t.Errorf("outline lists %q (%s); fields and variants are not structure", unwanted, s.Kind)
			}
		}
	}
}

// Rust states each use on its own line, so without merging a module with twenty
// of them spends twenty outline lines saying nothing.
func TestOutlineRustMergesImports(t *testing.T) {
	fm := outlineRs(t, "w.rs", rsSource)

	top := 0
	for _, s := range fm.Symbols {
		if s.Kind == "import" && s.Depth == 0 {
			top++
			if s.StartLine != 3 || s.EndLine != 4 {
				t.Errorf("import block = %d-%d, want 3-4", s.StartLine, s.EndLine)
			}
		}
	}
	if top != 1 {
		t.Errorf("got %d top-level import symbols, want 1 merged block", top)
	}
}
