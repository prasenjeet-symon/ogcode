package codemap

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	ts "github.com/tree-sitter/go-tree-sitter"
)

// MaxDiagnostics caps how many problems one check reports. Tree-sitter recovers
// after each error and keeps parsing, so a file damaged near the top can
// cascade into dozens of complaints that all describe the same mistake. The
// first few locate the damage; the rest are noise the agent would pay context
// for.
const MaxDiagnostics = 20

// ErrDirectory is returned for a path that names a directory, which has no
// syntax or outline of its own.
var ErrDirectory = errors.New("is a directory")

// Diagnostic is one syntax problem recovered from a parse.
//
// Line and Column are 1-based, matching the numbering file_map prints and read
// accepts, so a diagnostic can be handed straight to a ranged read. Column
// counts characters, not bytes, so it agrees with an editor on a line that
// holds non-ASCII text.
type Diagnostic struct {
	Line   int
	Column int
	// EndLine is the last line the problem covers, equal to Line for a
	// single-line one. A damaged region can run for many lines, and its extent
	// is what tells a reader whether they are looking at one bad token or a
	// file that needs rewriting.
	EndLine int
	// Missing marks a diagnostic the parser inferred from a token that should
	// have been there rather than from bytes it could not use. These carry the
	// better message — the parser names the exact token it wanted.
	Missing bool
	Message string
	// Source is the file's line at Line, trimmed of trailing space. Empty when
	// the diagnostic points past the last line.
	Source string
}

// CheckResult is the outcome of a syntax check on one file.
type CheckResult struct {
	Path string
	Lang string
	// Checked is false when nothing could check this file — no parser covers
	// its type, or (see Reason) its content is not the language yet. The file
	// was not parsed, which is not the same as it being clean: nothing may be
	// concluded from an empty Diagnostics in that case.
	Checked bool
	// Reason says why a file of a checkable type was still not checked — a
	// YAML file holding template syntax, say. Empty when the type itself has
	// no parser.
	Reason string
	// Diagnostics is empty for a file that parses cleanly.
	Diagnostics []Diagnostic
	// Truncated is true when MaxDiagnostics cut the list short.
	Truncated bool
}

// OK reports whether the file was parsed and had no syntax errors. An unchecked
// file is never OK: it is unknown.
func (r *CheckResult) OK() bool { return r.Checked && len(r.Diagnostics) == 0 }

// CountLabel is the error count as a reader should see it: "20+" when the cap
// cut the list short, since a bare 20 would be a count the file does not have.
func (r *CheckResult) CountLabel() string {
	if r.Truncated {
		return strconv.Itoa(len(r.Diagnostics)) + "+"
	}
	return strconv.Itoa(len(r.Diagnostics))
}

// Check parses path and reports its syntax errors.
//
// It returns the same errors Outline does for the same reasons — ErrBinary,
// ErrDirectory, *TooLargeError, and whatever os.Stat/os.ReadFile produce — so
// a caller can share one error switch across both.
func Check(path string) (*CheckResult, error) {
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

	return CheckSource(path, src)
}

// finding is a problem located by byte offset, before it is placed on a line.
// Offsets rather than rows and columns let every source of findings — a
// tree-sitter parse, a snippet embedded in HTML, a JSON decoder — hand over
// the same shape, and the placement (lines, character columns, the source
// line) happens once, against the whole file.
type finding struct {
	start, end int // byte offsets into the file; end == start for a point
	missing    bool
	// region marks a finding whose message should become "unparsable region,
	// runs to line N" when it turns out to span lines.
	region  bool
	message string
}

// CheckSource is Check over content already in hand, for a caller that has just
// written the bytes and would otherwise read them back.
func CheckSource(path string, src []byte) (*CheckResult, error) {
	res := &CheckResult{Path: path, Lang: "text"}

	if v := validatorFor(path); v != nil {
		res.Lang = v.name
		findings, reason := v.check(path, src)
		if reason != "" {
			res.Reason = reason
			return res, nil
		}
		res.Checked = true
		res.place(src, findings)
		return res, nil
	}

	lang := lookup(path)
	if lang == nil {
		return res, nil
	}
	res.Lang = lang.name

	var blocks []embeddedBlock
	var visit func(*ts.Node)
	if lang.name == "html" {
		visit = func(root *ts.Node) { blocks = embeddedBlocks(root, src) }
	}
	findings, err := parseFindings(lang, src, visit)
	if err != nil {
		return nil, err
	}
	res.Checked = true

	switch lang.name {
	case "python":
		// The grammar accepts indentation CPython rejects; see pyindent.go.
		findings = withIndentation(findings, pythonIndentFinding(src))
	case "html":
		// The grammar reads a script or style body as opaque text.
		embedded, err := checkEmbedded(src, blocks)
		if err != nil {
			return nil, err
		}
		findings = append(findings, embedded...)
	}

	res.place(src, findings)
	return res, nil
}

// parseFindings parses src with lang's grammar and returns its syntax errors.
// visit, when set, sees the root before the tree is released.
func parseFindings(lang *language, src []byte, visit func(*ts.Node)) ([]finding, error) {
	parser := ts.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(lang.grammar()); err != nil {
		return nil, fmt.Errorf("set language: %w", err)
	}

	// The grammar reads the shimmed bytes (see shims.go); positions and quoted
	// text come from src itself, which the shim leaves the same length.
	parsed := src
	if shim := grammarShims[lang.name]; shim != nil {
		parsed = shim(src)
	}
	tree := parser.Parse(parsed, nil)
	if tree == nil {
		return nil, fmt.Errorf("parse produced no tree")
	}
	defer func() { tree.Close() }()

	root := tree.RootNode()
	if repair := grammarRepairs[lang.name]; repair != nil && root.HasError() {
		if repaired, ok := repair(root, parsed); ok {
			if again := parser.Parse(repaired, nil); again != nil {
				tree.Close()
				tree, root = again, again.RootNode()
			}
		}
	}
	if visit != nil {
		visit(root)
	}
	if !root.HasError() {
		return nil, nil
	}
	findings := collectFindings(root, src)
	if len(findings) == 0 {
		// The tree says it has an error and no visible node holds it. Calling
		// that a clean parse would pass a file its compiler rejects.
		if f, ok := hiddenErrorFinding(root, src); ok {
			findings = append(findings, f)
		}
	}
	return findings, nil
}

// collectFindings walks the damaged parts of a tree and turns its ERROR and
// MISSING nodes into findings, in source order.
//
// The walk descends only into subtrees HasError marks, which skips the clean
// bulk of the file, and it stops at each ERROR rather than recursing into it. A
// damaged region nests further ERRORs that all describe the same mistake, so
// the outermost is reported — once — and reported as a line range rather than a
// point. The range is what makes the outermost node the right choice: an
// unclosed bracket produces an ERROR running from the bracket to wherever the
// parser finally gave up, and "lines 3-40 do not parse" is a true and useful
// thing to say, where the start line alone would look like a one-line typo and
// the end line alone would point past the mistake entirely.
func collectFindings(root *ts.Node, src []byte) []finding {
	var out []finding

	var walk func(n *ts.Node)
	walk = func(n *ts.Node) {
		switch {
		case n.IsMissing():
			// The node is zero-width and its kind is the token the parser
			// wanted, which makes this the one case where tree-sitter can say
			// what is wrong and not merely where.
			at := int(n.StartByte())
			out = append(out, finding{
				start:   at,
				end:     at,
				missing: true,
				message: fmt.Sprintf("expected %s", quoteKind(n.Kind())),
			})
			return
		case n.IsError():
			out = append(out, finding{
				start:   int(n.StartByte()),
				end:     int(n.EndByte()),
				region:  true,
				message: fmt.Sprintf("unexpected %s", snippet(n.Utf8Text(src))),
			})
			return
		case !n.HasError():
			return
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return out
}

// hiddenErrorFinding locates an error that no visible node carries.
//
// A MISSING token of a hidden kind — Go's statement terminator is one, left
// behind by `pa;ckage p` or by `funcName()` fused onto its keyword — is in the
// tree, but neither Child nor a cursor will return it, so HasError is its only
// trace. The deepest node that still has an error with no child that does is
// the one holding it, and that node's S-expression, which does print hidden
// MISSING nodes, says which named child the token should have followed.
//
// It returns false for the one such token that is not an error: a terminator
// missing after the last statement of a file with no final newline, which Go
// accepts and the grammar still marks.
func hiddenErrorFinding(root *ts.Node, src []byte) (finding, bool) {
	n := root
	for {
		var next *ts.Node
		for i := uint(0); i < n.ChildCount(); i++ {
			if c := n.Child(i); c.HasError() {
				next = c
				break
			}
		}
		if next == nil {
			break
		}
		n = next
	}

	at := int(n.StartByte())
	f := finding{start: at, end: at, message: fmt.Sprintf("syntax error in this %s", n.Kind())}
	k, ok := missingAfterNamedChild(n.ToSexp())
	named := int(n.NamedChildCount())
	switch {
	case !ok:
	case k < named:
		// The token belongs before the next named child, which is where the
		// parser found something it could not continue with.
		next := n.NamedChild(uint(k))
		at = int(next.StartByte())
		f = finding{start: at, end: at, message: fmt.Sprintf("unexpected %s", snippet(next.Utf8Text(src)))}
	case k > 0:
		// Nothing follows it: the construct ends without the token.
		prev := n.NamedChild(uint(k - 1))
		at = int(prev.EndByte())
		if at >= len(bytes.TrimRight(src, " \t\r\n")) {
			return finding{}, false
		}
		f = finding{start: at, end: at, message: "incomplete statement: a token is missing here"}
	}
	return f, true
}

// missingAfterNamedChild reads a node's S-expression and returns how many of
// its named children precede the first MISSING child — the index of the
// named child the missing token comes before. Quoted kinds are skipped, since
// a missing ")" prints as (MISSING ")").
func missingAfterNamedChild(sexp string) (int, bool) {
	depth, count := 0, 0
	for i := 0; i < len(sexp); i++ {
		switch sexp[i] {
		case '"':
			for i++; i < len(sexp) && sexp[i] != '"'; i++ {
				if sexp[i] == '\\' {
					i++
				}
			}
		case '(':
			depth++
			if depth == 2 {
				if strings.HasPrefix(sexp[i+1:], "MISSING") {
					return count, true
				}
				count++
			}
		case ')':
			depth--
		}
	}
	return 0, false
}

// place turns findings into diagnostics: sorted, capped at MaxDiagnostics, and
// positioned by line and character column against the whole file.
func (r *CheckResult) place(src []byte, findings []finding) {
	if len(findings) == 0 {
		return
	}
	sort.SliceStable(findings, func(i, j int) bool { return findings[i].start < findings[j].start })

	idx := newLineIndex(src)
	for i, f := range findings {
		if i > 0 && f.start == findings[i-1].start && f.message == findings[i-1].message {
			continue
		}
		if len(r.Diagnostics) == MaxDiagnostics {
			r.Truncated = true
			break
		}
		line, col := idx.position(src, f.start)
		endLine := line
		if f.end > f.start {
			var endCol int
			endLine, endCol = idx.position(src, f.end)
			// A span ending at column 1 stops at the very start of the next
			// line, so its last line of content is the one before.
			if endCol == 1 && endLine > line {
				endLine--
			}
		}
		msg := f.message
		if f.region && endLine > line {
			msg = fmt.Sprintf("unparsable region, runs to line %d", endLine)
		}
		r.Diagnostics = append(r.Diagnostics, Diagnostic{
			Line:    line,
			Column:  col,
			EndLine: endLine,
			Missing: f.missing,
			Message: msg,
			Source:  idx.lineText(src, line),
		})
	}
}

// lineIndex holds the byte offset at which each line of a file starts.
type lineIndex []int

func newLineIndex(src []byte) lineIndex {
	idx := lineIndex{0}
	for i, c := range src {
		if c == '\n' {
			idx = append(idx, i+1)
		}
	}
	return idx
}

// position converts a byte offset to a 1-based line and a 1-based column
// counted in characters.
func (idx lineIndex) position(src []byte, off int) (line, col int) {
	if off > len(src) {
		off = len(src)
	}
	if off < 0 {
		off = 0
	}
	line = sort.Search(len(idx), func(i int) bool { return idx[i] > off })
	return line, utf8.RuneCount(src[idx[line-1]:off]) + 1
}

// lineText returns line (1-based) without its newline or trailing space, or
// "" past the end of the file.
func (idx lineIndex) lineText(src []byte, line int) string {
	if line < 1 || line > len(idx) {
		return ""
	}
	start, end := idx[line-1], len(src)
	if line < len(idx) {
		end = idx[line] - 1
	}
	if start > end {
		return ""
	}
	return strings.TrimRight(string(src[start:end]), " \t\r")
}

// lineStartBefore returns the offset of the start of the line holding off.
func lineStartBefore(src []byte, off int) int {
	return bytes.LastIndexByte(src[:off], '\n') + 1
}

// quoteKind renders a node kind for a message. Punctuation and keywords are the
// literal text the parser wanted and read best quoted; named kinds such as
// identifier are category names and read best bare.
func quoteKind(kind string) string {
	for _, r := range kind {
		if r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return fmt.Sprintf("%q", kind)
		}
	}
	return kind
}

// snippet reduces a run of unparsable source to one short quoted line. An
// ERROR node can span many lines, and the message only needs enough of it for a
// reader to recognize the spot. The cut falls on a character boundary, so a
// non-ASCII line is shortened, not garbled.
func snippet(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i]) + " ..."
	}
	const max = 48
	if utf8.RuneCountInString(s) > max {
		cut := 0
		for i := range s {
			if cut == max {
				s = s[:i] + " ..."
				break
			}
			cut++
		}
	}
	if s == "" {
		return "token"
	}
	return fmt.Sprintf("%q", s)
}
