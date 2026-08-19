package codemap

import "testing"

const pySource = `"""Module docstring for the service."""

from __future__ import annotations

import os
from typing import Optional

LOG_LEVEL = "info"

# Cache of resolved ids.
_cache: dict[str, int] = {}

SENTINEL = object()  # a note about SENTINEL, not about DEBUG
DEBUG = False

type UserId = int


@dataclass(frozen=True)
class User:
    """A user record.

    Spans multiple lines.
    """

    name: str
    age: int = 0

    @property
    def display(self) -> str:
        """Return the display name."""
        return self.name

    async def save(self, *, force: bool = False) -> None:
        pass

    class Meta:
        """Inner configuration."""

        ordering = ["name"]


def main(argv: list[str]) -> int:
    """Entry point."""
    def inner() -> None:
        pass

    return 0


async def fetch(url: str) -> bytes: ...
`

func outlinePy(t *testing.T, name, src string) *FileMap {
	t.Helper()
	fm, err := Outline(write(t, name, src))
	if err != nil {
		t.Fatal(err)
	}
	if fm.Fallback {
		t.Fatalf("%s fell back to the heuristic scanner instead of using the Python grammar", name)
	}
	if fm.ParseError {
		t.Errorf("%s reported a parse error on valid source", name)
	}
	if fm.Lang != "python" {
		t.Errorf("Lang = %q, want python", fm.Lang)
	}
	return fm
}

func TestOutlinePythonTopLevelDeclarations(t *testing.T) {
	fm := outlinePy(t, "svc.py", pySource)

	for name, wantKind := range map[string]string{
		"LOG_LEVEL": "var",
		"_cache":    "var",
		"SENTINEL":  "var",
		"DEBUG":     "var",
		"UserId":    "type",
		"User":      "class",
		"main":      "func",
		"fetch":     "func",
	} {
		if got := find(t, fm, name).Kind; got != wantKind {
			t.Errorf("%s kind = %q, want %q", name, got, wantKind)
		}
	}
}

// Python documents from the inside: the doc is a string at the top of the body,
// not a comment above the declaration. docStart finds nothing in a Python file,
// so without docstringOf every symbol here would render bare.
func TestOutlinePythonDocstrings(t *testing.T) {
	fm := outlinePy(t, "svc.py", pySource)

	for _, tc := range []struct{ name, wantDoc string }{
		{"User", "A user record. Spans multiple lines."},
		{"display", "Return the display name."},
		{"Meta", "Inner configuration."},
		{"main", "Entry point."},
	} {
		if got := find(t, fm, tc.name).Doc; got != tc.wantDoc {
			t.Errorf("%s doc = %q, want %q", tc.name, got, tc.wantDoc)
		}
	}
}

// A binding has no body to hold a docstring, so a comment above it is the only
// doc it can carry — and it still widens the range the way it does elsewhere.
func TestOutlinePythonHashCommentIsDoc(t *testing.T) {
	fm := outlinePy(t, "svc.py", pySource)

	cache := find(t, fm, "_cache")
	if cache.Doc != "Cache of resolved ids." {
		t.Errorf("_cache doc = %q, want the comment above it with its # stripped", cache.Doc)
	}
	// Line 10 is the comment, 11 the binding.
	if cache.StartLine != 10 {
		t.Errorf("_cache StartLine = %d, want 10 (comment included)", cache.StartLine)
	}
}

// A trailing comment documents the code beside it, not the declaration on the
// next line. It ends on the row directly above that declaration, so adjacency
// alone would hand it over. Every language admits this shape; Python meets it
// most often, because a trailing # note is idiomatic there.
func TestOutlinePythonTrailingCommentNotAdopted(t *testing.T) {
	fm := outlinePy(t, "svc.py", pySource)

	debug := find(t, fm, "DEBUG")
	if debug.Doc != "" {
		t.Errorf("DEBUG doc = %q, want empty — the comment belongs to SENTINEL's line", debug.Doc)
	}
	if debug.StartLine != 14 {
		t.Errorf("DEBUG StartLine = %d, want 14 (not widened onto SENTINEL's line)", debug.StartLine)
	}
}

// A decorator sits above the declaration it modifies, on its own line, so the
// captured definition starts below it. @property changes what a method is and a
// route decorator is the only place a URL appears — both belong to the range a
// reader jumps to.
func TestOutlinePythonDecoratorsInRange(t *testing.T) {
	fm := outlinePy(t, "svc.py", pySource)

	// Line 19 is @dataclass, 20 the class.
	if got := find(t, fm, "User").StartLine; got != 19 {
		t.Errorf("User StartLine = %d, want 19 (decorator included)", got)
	}
	// Line 29 is @property, 30 the def.
	if got := find(t, fm, "display").StartLine; got != 29 {
		t.Errorf("display StartLine = %d, want 29 (decorator included)", got)
	}
}

// The signature stays the definition itself rather than the decorators above
// it: capSig truncates, and a long route decorator would push the name and
// parameters past the cap — leaving the reader the URL and not the function.
func TestOutlinePythonSignatures(t *testing.T) {
	fm := outlinePy(t, "svc.py", pySource)

	for _, tc := range []struct{ name, wantSig string }{
		{"User", "class User:"},
		{"display", "def display(self) -> str:"},
		{"save", "async def save(self, *, force: bool = False) -> None:"},
		{"fetch", "async def fetch(url: str) -> bytes:"},
		{"UserId", "type UserId = int"},
		{"main", "def main(argv: list[str]) -> int:"},
	} {
		if got := find(t, fm, tc.name).Signature; got != tc.wantSig {
			t.Errorf("%s signature = %q, want %q", tc.name, got, tc.wantSig)
		}
	}
}

// Methods and nested classes are structure; attributes and closures are not.
// A dataclass with twenty annotated fields would bury its methods, and a def
// inside a def is a factory's helper.
func TestOutlinePythonSkipsAttributesAndClosures(t *testing.T) {
	fm := outlinePy(t, "svc.py", pySource)

	meta := find(t, fm, "Meta")
	if meta.Kind != "class" || meta.Depth != 1 {
		t.Errorf("Meta kind/depth = %q/%d, want class/1", meta.Kind, meta.Depth)
	}
	for _, want := range []string{"display", "save"} {
		if got := find(t, fm, want).Depth; got != 1 {
			t.Errorf("%s depth = %d, want 1 (inside User)", want, got)
		}
	}
	for _, unwanted := range []string{"name", "age", "ordering", "inner"} {
		for _, s := range fm.Symbols {
			if s.Name == unwanted {
				t.Errorf("outline lists %q (%s); attributes and closures are not structure", unwanted, s.Kind)
			}
		}
	}
}

// Python states each import as its own statement, so a module with twenty would
// otherwise spend twenty outline lines saying "import block".
func TestOutlinePythonMergesImports(t *testing.T) {
	fm := outlinePy(t, "svc.py", pySource)

	imports := 0
	for _, s := range fm.Symbols {
		if s.Kind == "import" {
			imports++
			if s.StartLine != 3 || s.EndLine != 6 {
				t.Errorf("import block = %d-%d, want 3-6 (future, plain and from forms merged)", s.StartLine, s.EndLine)
			}
		}
	}
	if imports != 1 {
		t.Errorf("got %d import symbols, want 1 merged block", imports)
	}
}

// A settings module is nothing but bindings. Skipping them the way class
// attributes are skipped would leave such a file outlining to an empty list.
func TestOutlinePythonSettingsModule(t *testing.T) {
	fm := outlinePy(t, "settings.py", `DEBUG = True
ALLOWED_HOSTS = ["localhost"]
DATABASES = {
    "default": {"ENGINE": "django.db.backends.sqlite3"},
}
`)

	for _, want := range []string{"DEBUG", "ALLOWED_HOSTS", "DATABASES"} {
		if got := find(t, fm, want).Kind; got != "var" {
			t.Errorf("%s kind = %q, want var", want, got)
		}
	}
}

// Type stubs are the same grammar and carry the signatures a caller needs.
func TestOutlinePythonStubFile(t *testing.T) {
	fm := outlinePy(t, "svc.pyi", `class User:
    name: str

    def display(self) -> str: ...

def fetch(url: str) -> bytes: ...
`)

	if got := find(t, fm, "display").Kind; got != "method" {
		t.Errorf("display kind = %q, want method", got)
	}
	find(t, fm, "fetch")
}

// Python parses a note after the colon as a child of the definition rather than
// of the block, so slicing to the body pulls it into the signature and spends
// the length cap on it — which can truncate the parameters it was there to show.
func TestOutlinePythonCommentNotInSignature(t *testing.T) {
	fm := outlinePy(t, "trailing.py", `def close(self) -> None:  # Write RECORD
    pass


def build(name: str, *, strict: bool = False) -> int:  # some long aside here
    return 0
`)

	for _, tc := range []struct{ name, wantSig string }{
		{"close", "def close(self) -> None:"},
		{"build", "def build(name: str, *, strict: bool = False) -> int:"},
	} {
		if got := find(t, fm, tc.name).Signature; got != tc.wantSig {
			t.Errorf("%s signature = %q, want %q", tc.name, got, tc.wantSig)
		}
	}
}
