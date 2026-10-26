package nativehost

import (
	"crypto/sha256"
	"testing"
)

// TestExtensionIDMatchesTheChromiumDerivation builds the expected id in the
// test from the raw digest bytes — the literal hashed as spelled, first 16
// digest bytes, high nibble then low nibble mapped onto the a–p alphabet — so
// the golden is computed independently rather than by calling the code under
// test. The literal is deliberately mixed-case: Chrome 156.0.8078.48 on macOS
// 15.5 recorded the sha256 of the load path AS SPELLED (no lowercasing), and
// the lowercased spelling hashed here until 2026-10-25 produced an id Chrome
// never assigned.
func TestExtensionIDMatchesTheChromiumDerivation(t *testing.T) {
	const literal = "/Users/someone/Dev/ogcode/browser-extension"

	digest := sha256.Sum256([]byte(literal))
	want := make([]byte, 0, 32)
	for _, b := range digest[:16] {
		want = append(want, byte('a')+(b>>4), byte('a')+(b&0x0f))
	}

	if got := ExtensionIDForPath(literal); got != string(want) {
		t.Fatalf("ExtensionIDForPath(%q) = %q, want %q", literal, got, want)
	}
}

func TestExtensionIDShape(t *testing.T) {
	id := ExtensionIDForPath("/some/absolute/path")
	if len(id) != 32 {
		t.Fatalf("id = %q (%d chars), want 32 chars", id, len(id))
	}
	for _, c := range []byte(id) {
		if c < 'a' || c > 'p' {
			t.Fatalf("id %q has %q outside the a–p alphabet", id, c)
		}
	}
	if ExtensionIDForPath("/one/path") == ExtensionIDForPath("/other/path") {
		t.Fatal("two distinct paths produced the same id")
	}
}

// TestExtensionIDPreservesPathCase pins the Chrome behavior the old lowercase
// assumption broke: the id depends on the path's spelling, case included.
// The lowercased derivation produced an id Chrome never assigned, which left
// the manifest allowlisting an origin no installed extension could launch the
// native host from — the extension's silent "paste token to pair" fallback.
func TestExtensionIDPreservesPathCase(t *testing.T) {
	upper := ExtensionIDForPath("/Users/Echo/browser-extension")
	lower := ExtensionIDForPath("/users/echo/browser-extension")
	if upper == lower {
		t.Fatalf("path casing was folded away: %q == %q", upper, lower)
	}
}
