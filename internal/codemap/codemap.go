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
	"regexp"
	"sort"
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

// LooksBinary reports whether data holds a NUL byte — the heuristic this
// package uses to tell binary content from source text. Exported so other
// packages (e.g. the read tool) can apply the same rule before treating a
// file's bytes as text.
func LooksBinary(data []byte) bool {
	return bytes.IndexByte(data, 0) >= 0
}

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

	// startByte and endByte span the same text as StartLine and EndLine, in
	// bytes. Nesting is decided on these rather than on lines: two siblings
	// can share a line — `</li><li class="b">`, `.a{}.b{}` — and judged by
	// lines alone the second reads as nested inside the first.
	startByte, endByte int
}

// FileMap is the outline of one file.
type FileMap struct {
	Path       string
	Lang       string
	TotalLines int
	Symbols    []*Symbol
	// FromLine and ToLine bound a map of one region (see OutlineRange); both
	// are zero for a map of the whole file.
	FromLine, ToLine int
	// Omitted counts symbols dropped by the MaxSymbols cap.
	Omitted int
	// OmittedDepth, when the cap dropped anything, is the shallowest nesting
	// level it dropped: every entry that deep or deeper is left out, all
	// through the file, so what remains still covers the file end to end. Zero
	// when even the file-scope entries did not fit, and the map ends at
	// StopLine instead.
	OmittedDepth int
	// StopLine is the last line a map cut short by the cap still covers; zero
	// when it runs to the end.
	StopLine int
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
	return OutlineRange(path, 0, 0)
}

// OutlineRange maps only the declarations that reach into lines from through to
// (1-based, inclusive; to 0 runs to the end of the file), together with the
// ones enclosing them, so a region the whole-file map had to leave out can be
// seen in full. from 0 maps the whole file.
//
// The MaxSymbols cap applies to what the region holds, not to the file, which
// is what makes zooming in on a region of a capped map useful.
func OutlineRange(path string, from, to int) (*FileMap, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s: %w", path, ErrDirectory)
	}
	if info.Size() > MaxFileSize {
		return nil, &TooLargeError{Size: info.Size()}
	}

	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if LooksBinary(src) {
		return nil, ErrBinary
	}

	fm := &FileMap{Path: path, TotalLines: countLines(src)}

	lang := lookupForOutline(path)
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

	if from > 0 {
		fm.FromLine, fm.ToLine = from, to
		if to <= 0 || to > fm.TotalLines {
			fm.ToLine = fm.TotalLines
		}
		fm.Symbols = within(fm.Symbols, fm.FromLine, fm.ToLine)
	}
	capSymbols(fm)
	return fm, nil
}

// within keeps the symbols whose range reaches into lines from..to. A
// declaration that encloses the region stays too: it is what the region's
// entries are nested in, and without it their indentation would hang from
// nothing.
func within(symbols []*Symbol, from, to int) []*Symbol {
	out := symbols[:0]
	for _, s := range symbols {
		if s.EndLine >= from && s.StartLine <= to {
			out = append(out, s)
		}
	}
	return out
}

// capSymbols holds the outline to MaxSymbols entries.
//
// The deepest entries go first. Cutting by position instead kept the top of
// the file in full and dropped the rest outright: a long HTML page, where every
// element carrying a class earns an entry, mapped its header and decorations
// and then stopped halfway down, leaving the lower half of the page with no
// map at all. Keeping every level that fits keeps the whole file covered —
// coarser where it has to be — and a region can still be mapped in full with
// OutlineRange.
//
// Only when the file-scope entries alone overflow the cap is the map cut by
// position, and then it records where it stops.
func capSymbols(fm *FileMap) {
	total := len(fm.Symbols)
	if total <= MaxSymbols {
		return
	}

	perDepth := map[int]int{}
	for _, s := range fm.Symbols {
		perDepth[s.Depth]++
	}
	keepDepth, kept := -1, 0
	for d := 0; kept+perDepth[d] <= MaxSymbols && perDepth[d] > 0; d++ {
		kept += perDepth[d]
		keepDepth = d
	}

	out := fm.Symbols[:0]
	if keepDepth >= 0 {
		for _, s := range fm.Symbols {
			if s.Depth <= keepDepth {
				out = append(out, s)
			}
		}
		fm.OmittedDepth = keepDepth + 1
	} else {
		for _, s := range fm.Symbols {
			if s.Depth == 0 && len(out) < MaxSymbols {
				out = append(out, s)
			}
		}
		fm.StopLine = out[len(out)-1].EndLine
	}
	fm.Symbols = out
	fm.Omitted = total - len(out)
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
			if lang.localScopeKind != "" && hasAncestor(&node, lang.localScopeKind) {
				continue
			}
			symbols = append(symbols, buildSymbol(&node, src, kind, lang))
		}
	}

	sortByPosition(symbols)
	symbols = dedupSymbols(symbols)
	symbols = mergeImports(symbols)
	assignDepth(symbols)

	parseErr := root.HasError()
	if parseErr && len(src) > 0 && src[len(src)-1] != '\n' {
		parseErr = stillBrokenWithFinalNewline(parser, src)
	}
	return symbols, parseErr, nil
}

// hasAncestor reports whether any node enclosing node is of the given kind.
func hasAncestor(node *ts.Node, kind string) bool {
	for p := node.Parent(); p != nil; p = p.Parent() {
		if p.Kind() == kind {
			return true
		}
	}
	return false
}

// stillBrokenWithFinalNewline reports whether src has a syntax error once its
// last line is ended.
//
// Some grammars expect every top-level declaration to be closed by a newline.
// A Go file whose last line has none parses to a tree ending in a MISSING
// terminator that no visible node holds, and HasError reports it — for a file
// the compiler accepts as written. Parsing once more with the newline in place
// separates that from damage the file really has.
func stillBrokenWithFinalNewline(parser *ts.Parser, src []byte) bool {
	ended := make([]byte, len(src)+1)
	copy(ended, src)
	ended[len(src)] = '\n'
	tree := parser.Parse(ended, nil)
	if tree == nil {
		return true
	}
	defer tree.Close()
	return tree.RootNode().HasError()
}

// dedupSymbols drops consecutive symbols that describe the same declaration
// twice, keeping the first.
//
// A pattern can bind the same node several times — the HTML element query
// matches once per attribute, so `<nav id="main" class="nav">` arrives as two
// identical captures. Sorting puts those duplicates adjacent, which is what
// makes comparing against the last kept symbol sufficient; a non-adjacent
// duplicate would be two declarations in different places and must stay.
func dedupSymbols(symbols []*Symbol) []*Symbol {
	out := symbols[:0]
	for _, s := range symbols {
		if n := len(out); n > 0 &&
			out[n-1].Kind == s.Kind &&
			out[n-1].StartLine == s.StartLine &&
			out[n-1].EndLine == s.EndLine &&
			out[n-1].Name == s.Name &&
			out[n-1].Signature == s.Signature {
			continue
		}
		out = append(out, s)
	}
	return out
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
	//
	// The walk starts from the declaration's outermost same-line ancestor, not
	// from the captured node. For Rust the two are the same, but a Dart method
	// is captured at the function_signature inside a method_signature, and it is
	// the outer node that @override is a sibling of — walking from the inner one
	// finds no siblings at all and every annotated Dart member loses both its
	// annotation and the doc comment above it.
	if lang.attrKind != "" {
		attrAnchor := docAnchor(node)
		for {
			prev := attrAnchor.PrevNamedSibling()
			if prev == nil || prev.Kind() != lang.attrKind {
				break
			}
			if lastContentRow(prev) != int(attrAnchor.StartPosition().Row)-1 {
				break
			}
			attrAnchor = prev
		}
		if attrAnchor.StartPosition().Row < anchor.StartPosition().Row {
			anchor = attrAnchor
		}
	}

	startLine := int(anchor.StartPosition().Row) + 1
	startByte := int(anchor.StartByte())
	endLine := int(node.EndPosition().Row) + 1
	endByte := int(node.EndByte())
	// A node whose end lands in column 0 stops at the very start of the next
	// line, so its last line of content is the one before.
	if node.EndPosition().Column == 0 && endLine > startLine {
		endLine--
	}

	// Dart states a function's body beside its signature rather than inside it,
	// so the captured declaration ends at the closing parenthesis and the code
	// is the sibling that follows. Left alone, every Dart range would stop at
	// the signature line — and a range that omits the body is the one thing a
	// reader cannot use it for.
	if body := trailingBody(node, lang); body != nil {
		if end := lastContentRow(body) + 1; end > endLine {
			endLine = end
		}
		endByte = max(endByte, int(body.EndByte()))
	}

	// A docstring is inside the body, so it never moves the start line the way
	// a comment above the declaration does. Where a language has both, the
	// docstring wins: it is the one the language itself treats as the doc.
	docText := ""
	if lang.docstrings {
		docText = docstringOf(node, src)
	}
	if docText == "" {
		docLine, docByte, fromComment := docStart(anchor, src, lang)
		if docLine > 0 && docLine < startLine {
			startLine = docLine
			startByte = docByte
		}
		docText = fromComment
	}

	sym := &Symbol{
		Kind:      kind,
		Signature: signatureFor(node, src, kind, names, lang),
		Doc:       docText,
		StartLine: startLine,
		EndLine:   endLine,
		startByte: startByte,
		endByte:   endByte,
	}
	if len(names) > 0 {
		sym.Name = names[0]
	}
	return sym
}

// trailingBody returns the body node a declaration states beside itself, or nil
// where the language does not do that or this declaration has none.
//
// The lookup starts from the declaration's outermost same-line ancestor rather
// than from the captured node, because the two are not always the same one. A
// Dart method is captured at the function_signature that carries its name,
// which sits inside a method_signature — and the body is a sibling of the
// outer node, not the inner. docAnchor is the climb that already reconciles
// them for doc comments; the body needs the identical one.
//
// An abstract method has no body, and the sibling after it is the next
// declaration. Checking the kind is what tells those apart.
func trailingBody(node *ts.Node, lang *language) *ts.Node {
	if lang.trailingBodyKind == "" {
		return nil
	}
	next := docAnchor(node).NextNamedSibling()
	if next == nil || next.Kind() != lang.trailingBodyKind {
		return nil
	}
	return next
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
func docStart(node *ts.Node, src []byte, lang *language) (int, int, string) {
	if len(lang.commentKinds) == 0 {
		return 0, 0, ""
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
		return 0, 0, ""
	}

	// block was collected bottom-up; read it back in source order.
	var parts []string
	for i := len(block) - 1; i >= 0; i-- {
		for _, line := range strings.Split(block[i].Utf8Text(src), "\n") {
			if isDirective(line) {
				continue
			}
			if stripped := firstDocLine(line); stripped != "" {
				parts = append(parts, stripped)
			}
		}
	}

	top := block[len(block)-1]
	doc := strings.Join(parts, " ")
	if lang.xmlDocs {
		doc = summaryFromXMLDoc(doc)
	}
	return int(top.StartPosition().Row) + 1, int(top.StartByte()), doc
}

// goDirective matches the text after "//" of a Go-style directive comment —
// //go:embed, //nolint:errcheck, //lint:ignore — the form go/doc leaves out of
// a doc comment.
var goDirective = regexp.MustCompile(`^[a-z0-9]+:[a-z0-9]`)

// toolDirectives open a comment addressed to a linter, formatter or type
// checker rather than to a reader.
var toolDirectives = []string{
	"eslint-disable", "eslint-enable", "@ts-", "prettier-ignore", "biome-ignore",
	"istanbul ignore", "c8 ignore", "noqa", "pylint:", "type: ignore", "fmt: off",
	"fmt: on", "+build ", "-*-",
}

// isDirective reports whether one line of a comment is an instruction to a tool
// rather than a description of the code.
//
// A directive still annotates the declaration, so its line stays in the range.
// It is only kept out of the doc text, where it read as the summary — the
// excerpt of an embedded file system was "go:embed all:dist" — and spent the
// excerpt's length before the sentence that says what the declaration is for.
func isDirective(raw string) bool {
	s := strings.TrimSpace(raw)
	if rest, ok := strings.CutPrefix(s, "//"); ok {
		if goDirective.MatchString(rest) ||
			strings.HasPrefix(rest, "line ") || strings.HasPrefix(rest, "export ") || strings.HasPrefix(rest, "extern ") {
			return true
		}
	}
	text := strings.TrimSpace(strings.TrimLeft(s, "/*#!"))
	for _, d := range toolDirectives {
		if strings.HasPrefix(text, d) {
			return true
		}
	}
	return false
}

// summaryFromXMLDoc reduces a C# XML documentation comment to the prose a
// reader wants.
//
// Left alone, the convention defeats the doc excerpt entirely. The one-line
// form spends a quarter of the budget on <summary></summary>, and the far more
// common block form
//
//	/// <summary>
//	/// Charges a card.
//	/// </summary>
//	/// <param name="card">The card to charge.</param>
//
// joins to a string whose first eighty characters are mostly markup. The
// summary is the sentence that describes the declaration — the rest documents
// its parameters, which the signature beside it already shows — so the summary
// is taken when present and everything after it dropped.
//
// Inline elements are unwrapped rather than deleted. <see cref="Card"/> and
// <paramref name="amount"/> stand in for nouns mid-sentence, and removing them
// outright leaves prose with holes in it, so the referenced name is kept as the
// text it stood for.
func summaryFromXMLDoc(doc string) string {
	if !strings.Contains(doc, "<") {
		return doc
	}
	if _, after, ok := strings.Cut(doc, "<summary>"); ok {
		if before, _, closed := strings.Cut(after, "</summary>"); closed {
			doc = before
		} else {
			doc = after
		}
	}

	var b strings.Builder
	for {
		before, rest, ok := strings.Cut(doc, "<")
		b.WriteString(before)
		if !ok {
			break
		}
		tag, after, closed := strings.Cut(rest, ">")
		if !closed {
			// An unterminated "<" is ordinary prose — a generic written out, or
			// a comparison — not a tag. Keep it.
			b.WriteString("<")
			b.WriteString(rest)
			break
		}
		b.WriteString(xmlDocRef(tag))
		doc = after
	}
	return tidyXMLDocText(collapse(b.String()))
}

// xmlDocEntities decodes the escapes the format requires. A doc that describes
// a comparison has to write &lt; for the < it means, and leaving the escape in
// puts markup back into the prose this function exists to recover.
var xmlDocEntities = strings.NewReplacer(
	"&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'",
	// Ampersand last: decoding it first would turn "&amp;lt;" — an escaped
	// escape — into a live "<".
	"&amp;", "&",
)

// tidyXMLDocText repairs the spacing left by unwrapping inline elements.
//
// An element standing in for a noun is replaced by the word plus a space on
// each side, because it usually sits mid-sentence and needs them. At the end of
// a clause it does not, and "charges the Card ." reads as a typo — so the space
// before closing punctuation is taken back out.
func tidyXMLDocText(s string) string {
	s = xmlDocEntities.Replace(s)
	for _, p := range []string{" .", " ,", " ;", " :", " !", " ?", " )"} {
		s = strings.ReplaceAll(s, p, p[1:])
	}
	return strings.TrimSpace(s)
}

// xmlDocRef returns the name an inline doc element stands for, or "" for an
// element that is pure markup. cref names a type or member and name names a
// parameter; both read as the word the sentence meant.
func xmlDocRef(tag string) string {
	for _, attr := range []string{`cref="`, `name="`} {
		if _, after, ok := strings.Cut(tag, attr); ok {
			if val, _, closed := strings.Cut(after, `"`); closed {
				// A cref carries a prefix for its kind — T: for a type, M: for
				// a method — which is compiler bookkeeping, not prose.
				if _, stripped, ok := strings.Cut(val, ":"); ok && len(val) > 2 && val[1] == ':' {
					val = stripped
				}
				return " " + val + " "
			}
		}
	}
	return " "
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
//
// HTML's <!-- --> is checked after the others: its markers are whole words a
// doc rarely begins or ends with mid-block, so a suffix trim is safe, and
// putting it earlier would let a CSS "*/"-style line rule it out. Without this
// a `<!-- primary nav -->` above a nav element keeps its markers and reads as
// markup rather than as the description it is.
func firstDocLine(raw string) string {
	return strings.TrimSpace(decorationRun.ReplaceAllString(stripDocMarkers(raw), ""))
}

// decorationRun matches a run of rule characters at either end of a comment
// line. Section banners — "/* ==== Typefaces ==== */", "# ------ model",
// "<!-- ══ Nav ════ -->" — are drawn with them, and left in they filled the doc
// excerpt with "=====" before the word that names the section. An ASCII run
// needs three or more, so a "--flag" or a "- list item" keeps its text; a
// box-drawing character is never prose, so any run of those goes.
var decorationRun = regexp.MustCompile(`^(?:[-=*#~_+/]{3,}|[═─━]+)\s*|\s*(?:[-=*#~_+/]{3,}|[═─━]+)$`)

// stripDocMarkers removes the comment syntax from one line of a comment.
func stripDocMarkers(raw string) string {
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
	s = strings.TrimPrefix(s, "<!--")
	s = strings.TrimSuffix(s, "-->")
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
			// A byte-order mark opening the file is not content: the comment
			// after it still starts its line. Counted as text, it cost the first
			// declaration of every such file its doc comment.
			return i == len(utf8BOM)-1 && bytes.HasPrefix(src, utf8BOM)
		}
	}
	return true
}

// utf8BOM is the byte-order mark some editors write at the start of a file.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

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

	// C and C++ bury a declaration's name inside its declarator — behind a
	// pointer, a reference, an array or a function's parameter list — rather
	// than naming a field for it: `static char *name(void)` is a
	// function_definition whose name sits three declarators down. A
	// declaration can declare several (`int a, b;`), so each is followed.
	if names := declaratorNames(node, src); len(names) > 0 {
		return names
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

	// Dart leaves several declarations without a name field, naming them with a
	// bare identifier instead. Each kind here is unique to that grammar, so
	// matching on the kind alone is unambiguous — the same call the impl_item
	// case above makes.
	//
	// A constructor is named for its class rather than for the part after the
	// dot: `CounterPage.named` answers to CounterPage, which is what the
	// grammar's own name field reports for the plain constructor_signature, and
	// splitting the two would make the same declaration answer to two names.
	if kinds, ok := dartNameChild[node.Kind()]; ok {
		if n := firstChildOfKind(node, kinds); n != nil {
			return []string{n.Utf8Text(src)}
		}
	}

	// HTML and CSS bind no name field anywhere. Each kind below is unique to
	// its grammar, so matching on the kind alone is unambiguous — the same call
	// the impl_item and Dart cases above make.
	//
	// An element is named by the id or the class its author gave it, and only
	// falls back to the tag name when it has neither — but the query only
	// captures elements that carry one of the two, so the tag fallback fires
	// for script and style, whose tag is the only name they have.
	switch node.Kind() {
	case "pair":
		// A function-valued property of an object-literal module is known by
		// its key: `post: async () => {…}` is `post`.
		if key := node.ChildByFieldName("key"); key != nil {
			return []string{strings.Trim(key.Utf8Text(src), `"'`)}
		}
	case "export_statement":
		// Captured whole only when the export has no declaration to name —
		// `export default () => {…}` — so the name is the one it is imported by.
		return []string{"default"}
	case "element", "script_element", "style_element":
		if name := htmlElementName(node, src); name != "" {
			return []string{name}
		}
	case "rule_set":
		if name := cssRuleName(node, src); name != "" {
			return []string{name}
		}
	case "keyframes_statement":
		if n := firstChildOfKind(node, []string{"keyframes_name"}); n != nil {
			return []string{n.Utf8Text(src)}
		}
	case "media_statement", "supports_statement", "at_rule":
		// None of these binds a name, and media and supports carry no
		// at_keyword child at all — their query parses as feature_query or
		// binary_query. The first word of the statement is what a reader
		// calls the block: the "@media" in "@media (max-width: 600px)".
		// The signature beside it carries the query itself.
		if word := strings.Fields(firstLine(node.Utf8Text(src))); len(word) > 0 {
			return []string{word[0]}
		}
	}

	// The rest bind their names one level down, through specs or declarators —
	// Go's `const ( A = 1; B = 2 )`, TypeScript's `const a = 1, b = 2`. Listing
	// them is what makes a grouped declaration worth a line, since its own
	// first line is only `const (`.
	var names []string
	specs := specsOf(node)
	for _, spec := range specs {
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
		names = namesByKind(specs, src)
	}
	return names
}

// specsOf returns the specs a grouped declaration binds its names through.
//
// Most grammars hang them directly under the declaration, but Go's var group
// puts a var_spec_list in between — `var ( A = 1 )` is (var_declaration
// (var_spec_list (var_spec …))) where the const group has no such layer — so a
// walk one level down found only the list, and every `var ( … )` block showed
// as a bare "var (" with none of its names.
func specsOf(node *ts.Node) []*ts.Node {
	var specs []*ts.Node
	for i := uint(0); i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child == nil {
			continue
		}
		if strings.HasSuffix(child.Kind(), "_spec_list") {
			for j := uint(0); j < child.NamedChildCount(); j++ {
				if spec := child.NamedChild(j); spec != nil {
					specs = append(specs, spec)
				}
			}
			continue
		}
		specs = append(specs, child)
	}
	return specs
}

// declaratorNames follows each of node's `declarator` fields down to the name
// it declares. It returns nothing for a node without one, which is every
// grammar but C's and C++'s.
func declaratorNames(node *ts.Node, src []byte) []string {
	var names []string
	for i := uint(0); i < node.NamedChildCount(); i++ {
		if node.FieldNameForNamedChild(uint32(i)) != "declarator" {
			continue
		}
		if name := declaratorName(node.NamedChild(i), src); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// declaratorName unwraps one C or C++ declarator to the name inside it.
func declaratorName(d *ts.Node, src []byte) string {
	for range 16 {
		if d == nil {
			return ""
		}
		switch d.Kind() {
		case "identifier", "field_identifier", "type_identifier", "qualified_identifier",
			"destructor_name", "operator_name", "template_function":
			return d.Utf8Text(src)
		case "parenthesized_declarator", "reference_declarator":
			// These hold the inner declarator as a plain child, not a field.
			d = d.NamedChild(0)
			continue
		}
		d = d.ChildByFieldName("declarator")
	}
	return ""
}

// dartNameChild maps a Dart declaration kind to the child kinds that can carry
// its identifier, in the order they should be tried.
var dartNameChild = map[string][]string{
	"mixin_declaration":              {"identifier"},
	"type_alias":                     {"type_identifier"},
	"constant_constructor_signature": {"identifier"},
	"factory_constructor_signature":  {"identifier"},
}

// firstChildOfKind returns the first named child matching any of kinds, trying
// each kind in turn before moving on to the next.
func firstChildOfKind(node *ts.Node, kinds []string) *ts.Node {
	for _, want := range kinds {
		for i := uint(0); i < node.NamedChildCount(); i++ {
			if child := node.NamedChild(i); child != nil && child.Kind() == want {
				return child
			}
		}
	}
	return nil
}

// namesByKind is the fallback for grammars that hang a spec's identifier off an
// unnamed-field child. PHP's (const_element (name) (expression)) declares no
// fields at all, so the field walk in namesOf sees nothing and a top-level
// `const VERSION = '1.2.0'` would go unnamed.
//
// It runs only when the field walk came back empty, so Go's const_spec and
// TypeScript's variable_declarator — both of which do name the field — never
// reach it.
func namesByKind(specs []*ts.Node, src []byte) []string {
	var names []string
	for _, spec := range specs {
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

// htmlElementName names an element the way its author refers to it.
//
// The id is the strongest signal — it is unique in the document and it is what
// CSS and JS reach for — so it wins over class, which then wins over the tag
// name. The class takes its first whitespace-separated word, because the
// outline is one line per element and a utility-class soup ("card mt-4 p-2")
// reads best by its first, authoring name.
//
// The value is found by kind rather than by field: the HTML grammar names no
// fields at all, and an attribute_value sits directly under the attribute or
// one level down inside a quoted_attribute_value — a shape firstDescendantOfKind
// already reconciles.
func htmlElementName(node *ts.Node, src []byte) string {
	var tagNode *ts.Node
	classValue := ""
	for i := uint(0); i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child == nil {
			continue
		}
		switch child.Kind() {
		case "start_tag", "self_closing_tag":
			if tagNode == nil {
				tagNode = child
			}
			for j := uint(0); j < child.NamedChildCount(); j++ {
				attr := child.NamedChild(j)
				if attr == nil || attr.Kind() != "attribute" {
					continue
				}
				attrName := firstDescendantOfKind(attr, []string{"attribute_name"})
				if attrName == nil {
					continue
				}
				name := strings.ToLower(attrName.Utf8Text(src))
				if name != "id" && name != "class" {
					continue
				}
				val := firstDescendantOfKind(attr, []string{"attribute_value"})
				if val == nil {
					continue
				}
				value := val.Utf8Text(src)
				if name == "id" {
					return value
				}
				if classValue == "" {
					classValue = value
				}
			}
		}
	}
	if classValue != "" {
		if field := strings.Fields(classValue); len(field) > 0 {
			return field[0]
		}
	}
	if tagNode != nil {
		if n := firstDescendantOfKind(tagNode, []string{"tag_name"}); n != nil {
			return n.Utf8Text(src)
		}
	}
	return ""
}

// cssRuleName names a rule by its selectors text, collapsed to one line.
//
// A rule declares nothing it could be called by — the selector is the identity,
// and it is exactly what a reader scans a stylesheet for. A multi-line selector
// list folds onto one line so the outline stays tabular.
func cssRuleName(node *ts.Node, src []byte) string {
	sel := firstChildOfKind(node, []string{"selectors"})
	if sel == nil {
		return ""
	}
	return collapse(sel.Utf8Text(src))
}

// firstDescendantOfKind returns the first node of any of kinds found in a
// depth-first walk from node, or nil when none exists. firstChildOfKind only
// scans direct named children, which is not enough where a grammar nests the
// node one level down — HTML's attribute_value inside quoted_attribute_value.
func firstDescendantOfKind(node *ts.Node, kinds []string) *ts.Node {
	for _, want := range kinds {
		if node.Kind() == want {
			return node
		}
	}
	for i := uint(0); i < node.NamedChildCount(); i++ {
		if child := node.NamedChild(i); child != nil {
			if found := firstDescendantOfKind(child, kinds); found != nil {
				return found
			}
		}
	}
	return nil
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
			prev := out[len(out)-1]
			if s.EndLine > prev.EndLine {
				prev.EndLine = s.EndLine
			}
			prev.endByte = max(prev.endByte, s.endByte)
			continue
		}
		out = append(out, s)
	}
	return out
}

// assignDepth marks how deeply each symbol nests, by containment over the
// position-sorted list: a symbol lying wholly inside the one before it is its
// child. This keeps class members visibly attached to their class without the
// renderer needing to know anything about class syntax.
//
// Containment is judged on byte spans. On lines alone, a symbol starting on
// the line where the previous one ends read as inside it, and siblings sharing
// a line chained ever deeper — `<li class="a">…</li><li class="b">` put b under
// a, and a run of such siblings each under the one before.
func assignDepth(symbols []*Symbol) {
	var stack []*Symbol
	for _, s := range symbols {
		for len(stack) > 0 {
			top := stack[len(stack)-1]
			if s.startByte < top.endByte && s.endByte <= top.endByte {
				break
			}
			stack = stack[:len(stack)-1]
		}
		s.Depth = len(stack)
		stack = append(stack, s)
	}
}

// sortByPosition orders symbols by where they start and, among those starting
// at the same place, puts the enclosing one first so its contents follow it.
func sortByPosition(symbols []*Symbol) {
	sort.SliceStable(symbols, func(i, j int) bool {
		if symbols[i].startByte != symbols[j].startByte {
			return symbols[i].startByte < symbols[j].startByte
		}
		return symbols[i].endByte > symbols[j].endByte
	})
}
