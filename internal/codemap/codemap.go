// Package codemap produces a compact structural outline of a single source
// file — every top-level declaration and the line range it occupies — so an
// agent can jump straight to the region it needs with a bounded read instead of
// pulling the whole file into context.
//
// Outlines are computed on demand, not indexed. Tree-sitter parses a typical
// source file in single-digit milliseconds, which is far below the cost of the
// tool call that asks for it, and parsing fresh buys the property that matters
// most here: the line ranges always describe the file as it is right now. There
// is no store to migrate, nothing to invalidate when a file is edited, and no
// way for a stale range to send a reader to the wrong code.
package codemap

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"
)

const (
	// MaxFileSize bounds what Outline will parse. Files past this are almost
	// always generated or minified — bundles under web/dist, vendored blobs —
	// where parsing costs real time and the outline is unusable anyway.
	MaxFileSize = 2 << 20 // 2 MiB

	// MaxSymbols caps a single outline. A file with more declarations than this
	// is not one an agent should be navigating symbol-by-symbol, and an
	// unbounded outline would reintroduce the context flooding this package
	// exists to prevent. The overflow count is reported rather than dropped
	// silently.
	MaxSymbols = 400

	// maxSigLen caps a rendered signature. Long generic parameter lists and
	// multi-return signatures otherwise wrap and cost more than they inform.
	maxSigLen = 110

	// maxGroupNames caps how many names a grouped declaration lists inline.
	maxGroupNames = 8
)

// ErrBinary is returned for files holding NUL bytes.
var ErrBinary = errors.New("binary file")

// TooLargeError reports a file above MaxFileSize.
type TooLargeError struct {
	Size int64
}

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("file is %d bytes, above the %d byte outline limit", e.Size, MaxFileSize)
}

// Symbol is one declaration in a file.
//
// StartLine and EndLine are 1-based and inclusive, and StartLine includes any
// doc comment attached to the declaration: the comment is the part a reader
// most needs and excluding it would make every jump a two-step operation.
type Symbol struct {
	Kind      string // func, method, type, const, var, import, and fallback kinds
	Name      string // primary identifier; empty for import blocks
	Signature string // rendered display form, already collapsed and capped
	Doc       string // first line of the doc comment, markers stripped
	StartLine int
	EndLine   int
	// Depth is how many symbols enclose this one — 0 at file scope, 1 for a
	// class member. Rendered as indentation.
	Depth int
}

// FileMap is the outline of one file.
type FileMap struct {
	Path       string
	Lang       string
	TotalLines int
	Symbols    []*Symbol
	// Omitted counts symbols dropped by the MaxSymbols cap.
	Omitted int
	// Fallback is true when no grammar covered this extension and the
	// heuristic scanner produced the outline.
	Fallback bool
	// ParseError is true when tree-sitter hit a syntax error. The outline is
	// still usable — tree-sitter recovers and keeps going — but it may be
	// missing declarations after the damaged region, and a reader deserves to
	// know that before trusting a gap.
	ParseError bool
}

// Outline parses path and returns its structural map.
func Outline(path string) (*FileMap, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory", path)
	}
	if info.Size() > MaxFileSize {
		return nil, &TooLargeError{Size: info.Size()}
	}

	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if bytes.IndexByte(src, 0) >= 0 {
		return nil, ErrBinary
	}

	fm := &FileMap{Path: path, TotalLines: countLines(src)}

	lang := lookup(path)
	if lang == nil {
		fm.Lang = "text"
		fm.Symbols = fallbackSymbols(path, src)
		fm.Fallback = true
	} else {
		fm.Lang = lang.name
		syms, parseErr, err := parseSymbols(src, lang)
		if err != nil {
			return nil, err
		}
		fm.Symbols = syms
		fm.ParseError = parseErr
	}

	if len(fm.Symbols) > MaxSymbols {
		fm.Omitted = len(fm.Symbols) - MaxSymbols
		fm.Symbols = fm.Symbols[:MaxSymbols]
	}
	return fm, nil
}

// parseSymbols runs the language's outline query over src.
func parseSymbols(src []byte, lang *language) ([]*Symbol, bool, error) {
	tsLang, query, err := lang.load()
	if err != nil {
		return nil, false, err
	}

	parser := ts.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(tsLang); err != nil {
		return nil, false, fmt.Errorf("set language: %w", err)
	}

	tree := parser.Parse(src, nil)
	if tree == nil {
		return nil, false, fmt.Errorf("parse produced no tree")
	}
	defer tree.Close()

	root := tree.RootNode()
	cursor := ts.NewQueryCursor()
	defer cursor.Close()

	captureNames := query.CaptureNames()
	var symbols []*Symbol

	matches := cursor.Matches(query, root, src)
	for match := matches.Next(); match != nil; match = matches.Next() {
		for _, capture := range match.Captures {
			name := captureNames[capture.Index]
			kind, ok := strings.CutPrefix(name, "def.")
			if !ok {
				continue
			}
			node := capture.Node
			symbols = append(symbols, buildSymbol(&node, src, kind, lang))
		}
	}

	sortByPosition(symbols)
	symbols = mergeImports(symbols)
	assignDepth(symbols)
	return symbols, root.HasError(), nil
}

// buildSymbol renders one captured declaration node into a Symbol.
func buildSymbol(node *ts.Node, src []byte, kind string, lang *language) *Symbol {
	names := namesOf(node, src, kind)

	// The queries capture the declaration itself, but a decorated Python
	// declaration begins above it: @property changes what a method is, and a
	// route decorator is the only place the URL appears, so both belong to the
	// range a reader jumps to.
	anchor := node
	if lang.wrapperKind != "" {
		if parent := node.Parent(); parent != nil && parent.Kind() == lang.wrapperKind {
			anchor = parent
		}
	}

	// Rust states an attribute beside the item rather than around it, so the
	// #[derive(...)] lines are preceding siblings. They annotate the item and
	// belong to its range, and the doc comment sits above them — so the comment
	// walk has to start from the topmost attribute, not from the item.
	if lang.attrKind != "" {
		for {
			prev := anchor.PrevNamedSibling()
			if prev == nil || prev.Kind() != lang.attrKind {
				break
			}
			if lastContentRow(prev) != int(anchor.StartPosition().Row)-1 {
				break
			}
			anchor = prev
		}
	}

	startLine := int(anchor.StartPosition().Row) + 1
	endLine := int(node.EndPosition().Row) + 1
	// A node whose end lands in column 0 stops at the very start of the next
	// line, so its last line of content is the one before.
	if node.EndPosition().Column == 0 && endLine > startLine {
		endLine--
	}

	// A docstring is inside the body, so it never moves the start line the way
	// a comment above the declaration does. Where a language has both, the
	// docstring wins: it is the one the language itself treats as the doc.
	docText := ""
	if lang.docstrings {
		docText = docstringOf(node, src)
	}
	if docText == "" {
		docLine, fromComment := docStart(anchor, src, lang)
		if docLine > 0 && docLine < startLine {
			startLine = docLine
		}
		docText = fromComment
	}

	sym := &Symbol{
		Kind:      kind,
		Signature: signatureFor(node, src, kind, names, lang),
		Doc:       docText,
		StartLine: startLine,
		EndLine:   endLine,
	}
	if len(names) > 0 {
		sym.Name = names[0]
	}
	return sym
}

// docstringOf returns the string literal a declaration opens its body with.
//
// Python documents a declaration from the inside — the first statement of the
// body is the doc — where Go, TypeScript and PHP put a comment above it.
// docStart finds nothing in a Python file, so without this every class and
// function in the outline would render bare.
//
// Only the string's content is taken, so the quote style and any prefix stay
// out of the text, and it is collapsed to one line: a docstring's summary is
// written to stand alone, and the renderer cuts it at the first sentence.
func docstringOf(node *ts.Node, src []byte) string {
	body := node.ChildByFieldName("body")
	if body == nil {
		return ""
	}
	first := body.NamedChild(0)
	if first == nil || first.Kind() != "expression_statement" {
		return ""
	}
	str := first.NamedChild(0)
	if str == nil || str.Kind() != "string" {
		return ""
	}
	for i := uint(0); i < str.NamedChildCount(); i++ {
		if part := str.NamedChild(i); part != nil && part.Kind() == "string_content" {
			return collapse(part.Utf8Text(src))
		}
	}
	return ""
}

// docStart walks the comment siblings immediately above node and returns the
// line the comment block starts on, plus the block's text joined into one line.
//
// Adjacency is required: a comment separated from the declaration by a blank
// line belongs to whatever came before it, not to this declaration. Upstream
// tags.scm expresses this with the #set-adjacent! directive, which the Go
// bindings do not implement, so it is checked here against row numbers.
//
// The whole block is joined rather than just its opening line. Go doc comments
// wrap at around 77 columns, so the first physical line almost always ends
// mid-sentence — joining lets the caller cut at a sentence boundary instead of
// wherever the author happened to hit the margin.
func docStart(node *ts.Node, src []byte, lang *language) (int, string) {
	if len(lang.commentKinds) == 0 {
		return 0, ""
	}
	anchor := docAnchor(node)
	var block []*ts.Node
	expectedRow := int(anchor.StartPosition().Row) - 1

	for prev := anchor.PrevNamedSibling(); prev != nil; prev = prev.PrevNamedSibling() {
		if !lang.isComment(prev.Kind()) {
			break
		}
		if lastContentRow(prev) != expectedRow {
			break
		}
		if !startsItsLine(prev, src) {
			break
		}
		block = append(block, prev)
		expectedRow = int(prev.StartPosition().Row) - 1
	}

	if len(block) == 0 {
		return 0, ""
	}

	// block was collected bottom-up; read it back in source order.
	var parts []string
	for i := len(block) - 1; i >= 0; i-- {
		for _, line := range strings.Split(block[i].Utf8Text(src), "\n") {
			if stripped := firstDocLine(line); stripped != "" {
				parts = append(parts, stripped)
			}
		}
	}

	topLine := int(block[len(block)-1].StartPosition().Row) + 1
	return topLine, strings.Join(parts, " ")
}

// docAnchor climbs to the outermost ancestor starting on the same line as node.
//
// A doc comment sits above the whole declaration, and in TypeScript the whole
// declaration is usually an export wrapper: the comment above
// `export function f()` is a sibling of the export_statement, not of the
// function_declaration the query captured, so walking siblings from the
// captured node alone would find nothing. Climbing only while the start row is
// unchanged reaches the wrapper without ever stepping off the declaration's own
// line, and stopping short of the root keeps a first-line declaration from
// climbing into the file node.
func docAnchor(node *ts.Node) *ts.Node {
	anchor := node
	row := node.StartPosition().Row
	for p := anchor.Parent(); p != nil && p.Parent() != nil && p.StartPosition().Row == row; p = p.Parent() {
		anchor = p
	}
	return anchor
}

// firstDocLine strips comment markers from one line of a doc comment.
//
// The line-comment markers are mutually exclusive and end the work, so they
// return early: stripping "#" from a language that does not use it would eat
// the first word of a "//#nosec"-style directive.
//
// For the block form the closing "*/" is trimmed before the leading "*", and
// the order matters: on a line holding only "*/", stripping the leading star
// first leaves a bare "/" that survives the suffix trim and lands in the doc
// text. Every block comment ends with such a line, so the wrong order puts a
// stray slash on the end of every docblock — which PHP and JSDoc use
// throughout.
func firstDocLine(raw string) string {
	s := strings.TrimSpace(raw)
	if rest, ok := strings.CutPrefix(s, "//"); ok {
		// Rust marks an outer doc with a third slash and an inner doc with a
		// bang. Both are markers rather than text, and leaving them in puts a
		// stray "/" at the head of every Rust doc.
		rest = strings.TrimPrefix(rest, "/")
		rest = strings.TrimPrefix(rest, "!")
		return strings.TrimSpace(rest)
	}
	if rest, ok := strings.CutPrefix(s, "#"); ok {
		return strings.TrimSpace(rest)
	}
	s = strings.TrimPrefix(s, "/*")
	s = strings.TrimPrefix(s, "!")
	s = strings.TrimSuffix(s, "*/")
	s = strings.TrimPrefix(s, "*")
	return strings.TrimSpace(s)
}

// lastContentRow returns the 0-based row where node's text actually ends.
//
// A node ending in column 0 stops at the very start of the next line, so its
// last line of content is the one before. Rust's line_comment consumes its
// trailing newline and lands exactly there, which without this normalisation
// puts every Rust doc comment one row below where the adjacency check looks for
// it — and so drops the doc from every documented item in the file. Grammars
// whose comments stop at the last character are unaffected.
func lastContentRow(node *ts.Node) int {
	row := int(node.EndPosition().Row)
	if node.EndPosition().Column == 0 && row > int(node.StartPosition().Row) {
		row--
	}
	return row
}

// startsItsLine reports whether nothing but whitespace precedes node on its own
// line.
//
// A trailing comment documents the code beside it, not the declaration that
// follows. In
//
//	RawConfigParser = configparser.RawConfigParser  # Shorthand
//	Kind = NewType("Kind", str)
//
// the note belongs to the binding it sits on, yet it ends on the row directly
// above Kind and so passes the adjacency check on its own. Every language
// admits the same shape; Python meets it most often, because a trailing "#"
// note is idiomatic there.
func startsItsLine(node *ts.Node, src []byte) bool {
	start := int(node.StartByte())
	if start > len(src) {
		return false
	}
	for i := start - 1; i >= 0; i-- {
		if src[i] == '\n' {
			return true
		}
		if src[i] != ' ' && src[i] != '\t' {
			return false
		}
	}
	return true
}

// namesOf extracts the identifiers a declaration binds.
//
// Functions and methods carry a single name field. Grouped declarations —
// `const ( A = 1; B = 2 )` and its type/var equivalents — bind several, and
// listing them is what makes a grouped block worth a line in the outline.
func namesOf(node *ts.Node, src []byte, kind string) []string {
	if kind == "import" || kind == "package" {
		return nil
	}

	// Most declarations name themselves directly: Go funcs and types,
	// TypeScript classes, interfaces, enums and methods.
	if n := node.ChildByFieldName("name"); n != nil {
		return []string{n.Utf8Text(src)}
	}

	// A binding names itself on the left of the `=` rather than in a name
	// field: Python's module-level assignment and its type alias both do. No
	// declaration captured for Go, TypeScript or PHP carries a left field, so
	// this only fires where it is meant to.
	if n := node.ChildByFieldName("left"); n != nil {
		return []string{n.Utf8Text(src)}
	}

	// A Rust impl block declares no identifier of its own; it is known by the
	// type it is written for. This has to be resolved before the walk below,
	// which would descend into the trait path of `impl fmt::Display for Widget`
	// and name the block "Display" — leaving trait impls named after the trait
	// and inherent impls named after the type. It cannot be a last resort
	// either: that would shadow it behind the same walk.
	if node.Kind() == "impl_item" {
		if n := node.ChildByFieldName("type"); n != nil {
			return []string{n.Utf8Text(src)}
		}
	}

	// The rest bind their names one level down, through specs or declarators —
	// Go's `const ( A = 1; B = 2 )`, TypeScript's `const a = 1, b = 2`. Listing
	// them is what makes a grouped declaration worth a line, since its own
	// first line is only `const (`.
	var names []string
	for i := uint(0); i < node.NamedChildCount(); i++ {
		spec := node.NamedChild(i)
		if spec == nil {
			continue
		}
		for j := uint(0); j < spec.NamedChildCount(); j++ {
			if spec.FieldNameForNamedChild(uint32(j)) != "name" {
				continue
			}
			if child := spec.NamedChild(j); child != nil {
				names = append(names, child.Utf8Text(src))
			}
		}
	}
	if len(names) == 0 {
		names = namesByKind(node, src)
	}
	return names
}

// namesByKind is the fallback for grammars that hang a spec's identifier off an
// unnamed-field child. PHP's (const_element (name) (expression)) declares no
// fields at all, so the field walk in namesOf sees nothing and a top-level
// `const VERSION = '1.2.0'` would go unnamed.
//
// It runs only when the field walk came back empty, so Go's const_spec and
// TypeScript's variable_declarator — both of which do name the field — never
// reach it.
func namesByKind(node *ts.Node, src []byte) []string {
	var names []string
	for i := uint(0); i < node.NamedChildCount(); i++ {
		spec := node.NamedChild(i)
		if spec == nil {
			continue
		}
		for j := uint(0); j < spec.NamedChildCount(); j++ {
			child := spec.NamedChild(j)
			if child != nil && child.Kind() == "name" {
				names = append(names, child.Utf8Text(src))
				break
			}
		}
	}
	return names
}

func countLines(src []byte) int {
	if len(src) == 0 {
		return 0
	}
	n := bytes.Count(src, []byte{'\n'})
	if src[len(src)-1] != '\n' {
		n++
	}
	return n
}

// mergeImports collapses a run of adjacent import statements into one entry.
//
// Go states its imports in a single declaration, but TypeScript makes each one
// its own statement, so a module with twenty imports would otherwise spend
// twenty lines of the outline saying "import block".
func mergeImports(symbols []*Symbol) []*Symbol {
	out := symbols[:0]
	for _, s := range symbols {
		if s.Kind == "import" && len(out) > 0 && out[len(out)-1].Kind == "import" {
			if prev := out[len(out)-1]; s.EndLine > prev.EndLine {
				prev.EndLine = s.EndLine
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

// assignDepth marks how deeply each symbol nests, by containment over the
// line-sorted list: a symbol starting before the previous one ends is inside it.
// This keeps class members visibly attached to their class without the renderer
// needing to know anything about class syntax.
func assignDepth(symbols []*Symbol) {
	var stack []*Symbol
	for _, s := range symbols {
		for len(stack) > 0 && s.StartLine > stack[len(stack)-1].EndLine {
			stack = stack[:len(stack)-1]
		}
		s.Depth = len(stack)
		stack = append(stack, s)
	}
}

func sortByPosition(symbols []*Symbol) {
	for i := 1; i < len(symbols); i++ {
		for j := i; j > 0 && symbols[j].StartLine < symbols[j-1].StartLine; j-- {
			symbols[j], symbols[j-1] = symbols[j-1], symbols[j]
		}
	}
}
