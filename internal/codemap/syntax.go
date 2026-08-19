package codemap

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"
)

// MaxDiagnostics caps how many problems one check reports. Tree-sitter recovers
// after each error and keeps parsing, so a file damaged near the top can
// cascade into dozens of complaints that all describe the same mistake. The
// first few locate the damage; the rest are noise the agent would pay context
// for.
const MaxDiagnostics = 20

// Diagnostic is one syntax problem recovered from a parse.
//
// Line and Column are 1-based, matching the numbering file_map prints and read
// accepts, so a diagnostic can be handed straight to a ranged read.
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
	// Checked is false when no grammar covers this file's extension. The file
	// was not parsed at all, which is not the same as it being clean — nothing
	// may be concluded from an empty Diagnostics in that case.
	Checked bool
	// Diagnostics is empty for a file that parses cleanly.
	Diagnostics []Diagnostic
	// Truncated is true when MaxDiagnostics cut the list short.
	Truncated bool
}

// OK reports whether the file was parsed and had no syntax errors. An unchecked
// file is never OK: it is unknown.
func (r *CheckResult) OK() bool { return r.Checked && len(r.Diagnostics) == 0 }

// Check parses path and reports its syntax errors.
//
// It returns the same errors Outline does for the same reasons — ErrBinary,
// *TooLargeError, and whatever os.Stat/os.ReadFile produce — so a caller can
// share one error switch across both.
func Check(path string) (*CheckResult, error) {
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

	return CheckSource(path, src)
}

// CheckSource is Check over content already in hand, for a caller that has just
// written the bytes and would otherwise read them back.
func CheckSource(path string, src []byte) (*CheckResult, error) {
	res := &CheckResult{Path: path, Lang: "text"}

	lang := lookup(path)
	if lang == nil {
		return res, nil
	}
	res.Lang = lang.name

	tsLang, _, err := lang.load()
	if err != nil {
		return nil, err
	}

	parser := ts.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(tsLang); err != nil {
		return nil, fmt.Errorf("set language: %w", err)
	}

	tree := parser.Parse(src, nil)
	if tree == nil {
		return nil, fmt.Errorf("parse produced no tree")
	}
	defer tree.Close()

	res.Checked = true
	root := tree.RootNode()
	if !root.HasError() {
		return res, nil
	}

	lines := bytes.Split(src, []byte("\n"))
	for _, d := range collectDiagnostics(root, src) {
		if len(res.Diagnostics) == MaxDiagnostics {
			res.Truncated = true
			break
		}
		if d.Line-1 < len(lines) {
			d.Source = strings.TrimRight(string(lines[d.Line-1]), " \t\r")
		}
		res.Diagnostics = append(res.Diagnostics, d)
	}
	return res, nil
}

// collectDiagnostics walks the damaged parts of a tree and turns its ERROR and
// MISSING nodes into diagnostics, in source order.
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
func collectDiagnostics(root *ts.Node, src []byte) []Diagnostic {
	var out []Diagnostic

	var walk func(n *ts.Node)
	walk = func(n *ts.Node) {
		switch {
		case n.IsMissing():
			// The node is zero-width and its kind is the token the parser
			// wanted, which makes this the one case where tree-sitter can say
			// what is wrong and not merely where.
			line := int(n.StartPosition().Row) + 1
			out = append(out, Diagnostic{
				Line:    line,
				EndLine: line,
				Column:  int(n.StartPosition().Column) + 1,
				Missing: true,
				Message: fmt.Sprintf("expected %s", quoteKind(n.Kind())),
			})
			return
		case n.IsError():
			start := int(n.StartPosition().Row) + 1
			end := int(n.EndPosition().Row) + 1
			// A node ending in column 0 stops at the very start of the next
			// line, so its last line of content is the one before.
			if n.EndPosition().Column == 0 && end > start {
				end--
			}
			msg := fmt.Sprintf("unexpected %s", snippet(n.Utf8Text(src)))
			if end > start {
				msg = fmt.Sprintf("unparsable region, runs to line %d", end)
			}
			out = append(out, Diagnostic{
				Line:    start,
				EndLine: end,
				Column:  int(n.StartPosition().Column) + 1,
				Message: msg,
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
// reader to recognize the spot.
func snippet(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i]) + " ..."
	}
	const max = 48
	if len(s) > max {
		s = s[:max] + " ..."
	}
	if s == "" {
		return "token"
	}
	return fmt.Sprintf("%q", s)
}
