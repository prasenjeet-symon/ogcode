package codemap

import (
	"bytes"
	"regexp"

	ts "github.com/tree-sitter/go-tree-sitter"
)

// A grammar can lag its language, and every construct it predates reads as a
// syntax error in valid code. That is worse than silence: an agent told its
// edit broke the file will "fix" it, and the site's own stylesheet — named
// container queries, a 22.5% keyframe — would report fifteen errors on every
// edit. Each shim rewrites a known-valid construct its grammar cannot read into
// one it can, byte for byte the same length, so every offset still points into
// the original file and a real error elsewhere is still found where it is.
//
// Shims touch only syntax the language accepts. Anything they cannot rewrite
// safely is left to the caveat every error report carries: syntax newer than
// the parser is to be confirmed with the compiler, not rewritten.
var grammarShims = map[string]func([]byte) []byte{
	"css":        cssShim,
	"typescript": tsShim,
	"tsx":        tsShim,
	"rust":       rustShim,
	"java":       javaShim,
}

// blank overwrites src[start:end] with spaces.
func blank(src []byte, start, end int) {
	for i := start; i < end; i++ {
		src[i] = ' '
	}
}

// blankGroup applies re to src and blanks its first capture group in every
// match.
func blankGroup(src []byte, re *regexp.Regexp) []byte {
	out := bytes.Clone(src)
	for _, m := range re.FindAllSubmatchIndex(out, -1) {
		blank(out, m[2], m[3])
	}
	return out
}

var (
	// TypeScript 5.0: `export type * from "./types"`.
	tsExportTypeStar = regexp.MustCompile(`\bexport\s+(type)\s+\*`)
	// Rust 2024: `safe fn` and `safe static` inside an `unsafe extern` block.
	rustSafeItem = regexp.MustCompile(`\b(safe)\s+(?:fn|static)\b`)
	// Java 25: `import module java.base;`.
	javaImportModule = regexp.MustCompile(`(?m)^[ \t]*import\s+(module)\s+[A-Za-z_]`)
)

func tsShim(src []byte) []byte   { return blankGroup(src, tsExportTypeStar) }
func rustShim(src []byte) []byte { return blankGroup(src, rustSafeItem) }
func javaShim(src []byte) []byte { return blankGroup(src, javaImportModule) }

// cssKeyframeDecimal matches a decimal percentage used as a keyframe
// selector — "22.5% {" — where the grammar wants an integer.
var cssKeyframeDecimal = regexp.MustCompile(`\d*(\.)\d+%\s*[{,]`)

// cssShim rewrites what tree-sitter-css cannot read:
//
//   - the nesting selector "&", in any position ("&:hover", ".b &"), to an
//     ordinary type selector;
//   - an @container rule, which it cannot read when nested or named, to an
//     @supports rule, and an @media prelude using a style() query or range
//     syntax ("width > 400px"), to a plain "(a:b)" condition;
//   - a decimal keyframe selector, "22.5%", to an integer one;
//   - an unquoted url(#id), which references an SVG element, to url(_id).
func cssShim(src []byte) []byte {
	out := bytes.Clone(src)
	for i := 0; i < len(out); i++ {
		switch c := out[i]; {
		case c == '/' && at(out, i+1) == '*':
			end := bytes.Index(out[i+2:], []byte("*/"))
			if end < 0 {
				return out
			}
			i += end + 3
		case c == '"' || c == '\'':
			for i++; i < len(out) && out[i] != c; i++ {
				if out[i] == '\\' {
					i++
				}
			}
		case c == '&':
			out[i] = 'a'
		case c == '@':
			i = shimQueryPrelude(out, i)
		case (c == 'u' || c == 'U') && bytes.HasPrefix(bytes.ToLower(out[i:min(i+4, len(out))]), []byte("url(")):
			j := i + 4
			for j < len(out) && (out[j] == ' ' || out[j] == '\t') {
				j++
			}
			if at(out, j) == '#' {
				out[j] = '_'
			}
		}
	}
	for _, m := range cssKeyframeDecimal.FindAllSubmatchIndex(out, -1) {
		out[m[2]] = '0'
	}
	return out
}

// shimQueryPrelude rewrites an @container rule, and an @media rule using
// syntax the grammar lacks, at src[at], and returns the offset to resume
// scanning from.
//
// The grammar cannot read @container at all once it is nested — inside
// @media or a rule — and cannot read a container name, a style() or
// scroll-state() query, or range syntax anywhere. @supports it reads in every
// position, and "@container" and "@supports " are the same length, so every
// @container becomes an @supports with a plain "(a:b)" condition. An @media
// prelude is rewritten the same way only when it needs it.
func shimQueryPrelude(src []byte, at int) int {
	j := at + 1
	for j < len(src) && (src[j] == '-' || src[j] == '_' || (src[j]|0x20 >= 'a' && src[j]|0x20 <= 'z')) {
		j++
	}
	name := string(bytes.ToLower(src[at+1 : j]))
	if name != "container" && name != "media" {
		return j - 1
	}
	end := j
	for end < len(src) && src[end] != '{' && src[end] != ';' {
		end++
	}
	prelude := src[j:end]
	lower := bytes.ToLower(prelude)
	needs := name == "container" ||
		bytes.ContainsAny(prelude, "<>=") ||
		bytes.Contains(lower, []byte("style(")) ||
		bytes.Contains(lower, []byte("scroll-state("))
	const plain = " (a:b)"
	if !needs || len(prelude) < len(plain) {
		return j - 1
	}
	if name == "container" {
		copy(src[at+1:], "supports ")
	}
	blank(src, j, end)
	copy(src[j:], plain)
	return end - 1
}

// A repair is a shim that needs the parse to find its target: a construct a
// regular expression cannot tell from its look-alikes. It runs only when the
// first parse has errors, and returns the rewritten bytes (the same length,
// like a shim's) and whether it changed anything; the file is then parsed
// again, and only that second parse's errors are reported.
var grammarRepairs = map[string]func(root *ts.Node, src []byte) ([]byte, bool){
	"swift": swiftRepair,
}

// swiftRepair rewrites the empty tuple expression — `.success(())`,
// `send(())`, `return ()` — which the grammar cannot read: it parses `()` as a
// tuple_expression whose only element is a zero-width `bang` node around a
// MISSING `!`. The same two
// characters are a call's argument list in `f()` and a type in `() -> Void`,
// both of which parse, so only the tree can say which `()` to touch. Each one
// becomes `0 `: an expression wherever an empty tuple is one.
func swiftRepair(root *ts.Node, src []byte) ([]byte, bool) {
	var out []byte
	var walk func(n *ts.Node)
	walk = func(n *ts.Node) {
		if !n.HasError() {
			return
		}
		if n.Kind() == "tuple_expression" && n.ChildCount() == 3 &&
			n.Child(0).Kind() == "(" && n.Child(2).Kind() == ")" &&
			n.Child(1).StartByte() == n.Child(1).EndByte() && n.Child(1).HasError() {
			if out == nil {
				out = bytes.Clone(src)
			}
			start, end := int(n.StartByte()), int(n.EndByte())
			out[start] = '0'
			for i := start + 1; i < end; i++ {
				if out[i] != '\n' {
					out[i] = ' '
				}
			}
			return
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return out, out != nil
}
