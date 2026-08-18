package codemap

import (
	"fmt"
	"strings"
	"unicode/utf8"

	ts "github.com/tree-sitter/go-tree-sitter"
)

// signatureFor renders the display form of a declaration.
//
// Signatures are sliced out of the source rather than reconstructed from the
// tree. Reconstruction means re-implementing each language's declaration syntax
// and getting it subtly wrong on generics, variadics and multiple returns; the
// source already says exactly what the declaration is, so the work is choosing
// where to stop reading.
func signatureFor(node *ts.Node, src []byte, kind string, names []string) string {
	switch kind {
	case "import":
		return "import block"

	case "package":
		return capSig(collapse(firstLine(sliceOf(node, src, node.EndByte()))))

	case "type", "const", "var":
		// A grouped declaration's first line is just `const (`, which tells a
		// reader nothing. List what it binds instead.
		if len(names) > 1 {
			return fmt.Sprintf("%s ( %s )", kind, joinCapped(names, maxGroupNames))
		}
		return capSig(trimOpenBrace(collapse(firstLine(sliceOf(node, src, node.EndByte())))))

	default:
		if end, ok := bodyStart(node); ok {
			// The body is where the slice stops, so the whole signature is safe
			// to collapse — that keeps a parameter list broken across lines
			// readable instead of cutting it at the first line break.
			return capSig(trimOpenBrace(collapse(sliceOf(node, src, end))))
		}
		// No body to stop at, so the slice runs to the end of the declaration.
		// Take one line before collapsing, or the whole construct would fold
		// into the signature.
		return capSig(trimOpenBrace(collapse(firstLine(sliceOf(node, src, node.EndByte())))))
	}
}

// bodyStart locates where a declaration's body begins, so the signature can
// stop there.
//
// Most declarations carry the body on a `body` field. Function-valued bindings
// do not: in `const F = () => {...}` the body belongs to the arrow function
// nested inside the declarator, and without this second lookup the signature
// would swallow the entire function.
func bodyStart(n *ts.Node) (uint, bool) {
	if body := n.ChildByFieldName("body"); body != nil {
		return body.StartByte(), true
	}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		declarator := n.NamedChild(i)
		if declarator == nil {
			continue
		}
		value := declarator.ChildByFieldName("value")
		if value == nil {
			continue
		}
		if body := value.ChildByFieldName("body"); body != nil {
			return body.StartByte(), true
		}
	}
	return 0, false
}

// sliceOf returns src between node's start and end, guarding the bounds — a
// malformed parse can hand back ranges that do not line up with the buffer.
func sliceOf(node *ts.Node, src []byte, end uint) string {
	start := node.StartByte()
	if start > uint(len(src)) {
		return ""
	}
	if end > uint(len(src)) || end < start {
		end = uint(len(src))
	}
	return string(src[start:end])
}

// trimOpenBrace drops the brace that opens a body or a composite literal. It
// carries no information once the range on the same row already says how far
// the declaration runs.
func trimOpenBrace(s string) string {
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "{"))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// collapse folds runs of whitespace into single spaces so a signature broken
// across lines in the source still renders as one readable line.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// capSig truncates on a rune boundary — signatures carry non-ASCII in string
// literals and identifiers, and slicing mid-rune would emit replacement chars.
func capSig(s string) string {
	if len(s) <= maxSigLen {
		return s
	}
	cut := maxSigLen
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut]) + "…"
}

func joinCapped(names []string, limit int) string {
	if len(names) <= limit {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s, … +%d more", strings.Join(names[:limit], ", "), len(names)-limit)
}
