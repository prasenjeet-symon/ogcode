// Package swift provides the tree-sitter grammar for Swift.
//
// The grammar is vendored rather than pulled through go.mod. Upstream
// (alex-pinkus/tree-sitter-swift) generates src/parser.c at build time and
// gitignores it, and publishes no tagged versions, so no module version the Go
// proxy can serve is buildable — its own Go binding #includes a file that is
// never in the tree. Vendoring the generated parser is the only way to depend
// on this grammar from Go.
//
// parser.c and scanner.c are generated output, not source. Do not edit them.
// To move to a newer grammar, re-run the steps in README.md in this directory.
//
// They are compiled as two translation units rather than #included into one,
// which is what upstream's binding does: both define TOKEN_COUNT, and folding
// them together makes the compiler warn on every build.
package swift

// #cgo CFLAGS: -I${SRCDIR} -std=c11 -fPIC
// #include <tree_sitter/parser.h>
// const TSLanguage *tree_sitter_swift(void);
import "C"

import "unsafe"

// Language returns the tree-sitter Language for Swift.
func Language() unsafe.Pointer {
	return unsafe.Pointer(C.tree_sitter_swift())
}
