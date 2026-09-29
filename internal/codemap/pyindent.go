package codemap

import (
	"fmt"
	"strings"
)

// pythonIndentFinding applies CPython's indentation rules, which the
// tree-sitter grammar does not enforce: it parses an unexpected indent, a block
// header with no body, a dedent to a level that was never opened, and tabs and
// spaces traded mid-block, all without an error. Those are the most common ways
// an edit breaks a Python file, so the check has to catch them itself.
//
// The rules are the tokenizer's: indentation is measured at the first token of
// each logical line — a line inside brackets, after a backslash, inside a
// triple-quoted string, or holding only a comment carries none — with tabs
// advancing to the next multiple of 8 and, for the consistency check, counted
// as one column as well. A logical line ending in ':' opens a block the next
// one must indent. Only the first problem is reported, as CPython does: after
// it, every line is measured against a block structure that no longer exists.
func pythonIndentFinding(src []byte) *finding {
	s := &pyScanner{src: src}
	return s.run()
}

type pyLevel struct{ col, alt int }

type pyScanner struct {
	src   []byte
	stack []pyLevel
	// logicalAt is the offset of the first token of the current logical line.
	logicalAt int
}

func (s *pyScanner) run() *finding {
	src := s.src
	s.stack = []pyLevel{{0, 0}}
	depth := 0         // open brackets: newlines inside them do not end a line
	newLogical := true // no token yet on the current logical line
	var last byte      // last significant byte of the logical line
	header := -1       // offset of a block header still waiting for its body

	endLogical := func() {
		if !newLogical {
			if last == ':' {
				header = s.logicalAt
			} else {
				header = -1
			}
		}
		newLogical = true
	}

	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\f' || c == '\r':
			i++
			continue
		case c == '\n':
			if depth == 0 {
				endLogical()
			}
			i++
			continue
		case c == '#':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		case c == '\\' && (at(src, i+1) == '\n' || (at(src, i+1) == '\r' && at(src, i+2) == '\n')):
			// An explicit continuation: the next physical line belongs to
			// this logical one and its indentation means nothing.
			i += 2
			if src[i-1] == '\r' {
				i++
			}
			continue
		}

		if newLogical && depth == 0 {
			newLogical = false
			s.logicalAt = i
			if f := s.indent(i, &header); f != nil {
				return f
			}
		}

		switch {
		case c == '"' || c == '\'':
			i = s.scanString(i, "")
			last = c
		case isPyIdentByte(c):
			j := i
			for j < len(src) && isPyIdentByte(src[j]) {
				j++
			}
			if (at(src, j) == '"' || at(src, j) == '\'') && isStringPrefix(string(src[i:j])) {
				i = s.scanString(j, string(src[i:j]))
				last = '"'
			} else {
				last = src[j-1]
				i = j
			}
		default:
			switch c {
			case '(', '[', '{':
				depth++
			case ')', ']', '}':
				if depth > 0 {
					depth--
				}
			}
			last = c
			i++
		}
	}

	if depth == 0 {
		endLogical()
	}
	if header >= 0 {
		// The file ends on a header whose block never came. CPython names
		// the header's own line, there being no line after it to blame.
		line, _ := newLineIndex(src).position(src, header)
		return &finding{start: header, end: header, message: fmt.Sprintf("expected an indented block after line %d", line)}
	}
	return nil
}

// indent applies the rules at the first token of a logical line, at offset i.
func (s *pyScanner) indent(i int, header *int) *finding {
	src := s.src
	col, alt := 0, 0
	for _, c := range src[lineStartBefore(src, i):i] {
		switch c {
		case ' ':
			col++
			alt++
		case '\t':
			col = (col/8 + 1) * 8
			alt++
		case '\f':
			col, alt = 0, 0
		}
	}

	fail := func(msg string) *finding { return &finding{start: i, end: i, message: msg} }
	const tabs = "inconsistent use of tabs and spaces in indentation"

	// The tokenizer's verdict comes first, as in CPython: it has rejected a
	// tab-for-spaces swap or an unmatched dedent before the parser ever asks
	// whether a block header got its body.
	pending := *header >= 0
	opened := *header
	*header = -1
	missingBlock := func() *finding {
		line, _ := newLineIndex(src).position(src, opened)
		return fail(fmt.Sprintf("expected an indented block after line %d", line))
	}

	top := s.stack[len(s.stack)-1]
	switch {
	case col == top.col:
		if alt != top.alt {
			return fail(tabs)
		}
		if pending {
			return missingBlock()
		}
	case col > top.col:
		if alt <= top.alt {
			return fail(tabs)
		}
		if !pending {
			return fail("unexpected indent")
		}
		s.stack = append(s.stack, pyLevel{col, alt})
	default:
		for len(s.stack) > 1 && col < s.stack[len(s.stack)-1].col {
			s.stack = s.stack[:len(s.stack)-1]
		}
		top = s.stack[len(s.stack)-1]
		if col != top.col {
			return fail("unindent does not match any outer indentation level")
		}
		if alt != top.alt {
			return fail(tabs)
		}
		if pending {
			return missingBlock()
		}
	}
	return nil
}

// scanString consumes a string literal whose opening quote is at i and
// returns the offset just past it. A single-quoted string stops at an
// unescaped newline — that is a syntax error the grammar reports, and ending it
// there keeps the rest of the file measurable.
func (s *pyScanner) scanString(i int, prefix string) int {
	src := s.src
	q := src[i]
	triple := at(src, i+1) == q && at(src, i+2) == q
	prefix = strings.ToLower(prefix)
	format := strings.ContainsAny(prefix, "ft")
	raw := strings.ContainsRune(prefix, 'r')
	if triple {
		i += 3
	} else {
		i++
	}
	for i < len(src) {
		c := src[i]
		switch {
		case c == '\\' && format && (at(src, i+1) == '{' || at(src, i+1) == '}'):
			// In an f-string a backslash never escapes a brace: "\\{{" is a
			// backslash and a literal brace, "\\{x}" a backslash and a field.
			i++
			continue
		case c == '\\' && format && !raw && at(src, i+1) == 'N' && at(src, i+2) == '{':
			// \N{BULLET}: the braces name a character, they open no field.
			for i += 3; i < len(src) && src[i] != '}' && src[i] != q && src[i] != '\n'; i++ {
			}
			if at(src, i) == '}' {
				i++
			}
			continue
		case c == '\\':
			// Also in a raw string, where a backslash keeps the quote after it
			// from closing the literal.
			i += 2
			continue
		case c == '\n' && !triple:
			return i
		case format && c == '{':
			if at(src, i+1) == '{' {
				i += 2
				continue
			}
			i = s.scanReplacement(i + 1)
			continue
		case c == q:
			if !triple {
				return i + 1
			}
			if at(src, i+1) == q && at(src, i+2) == q {
				return i + 3
			}
		}
		i++
	}
	return i
}

// scanReplacement consumes an f-string replacement field from just past its
// '{' to just past its '}'. Since Python 3.12 the expression may hold strings
// in the enclosing quote and span lines, so it is scanned as code: brackets
// nest and strings are read whole. A top-level ':' starts the format spec,
// which is literal text apart from nested fields — "{x:'>10}" pads with a
// quote, it does not open a string.
func (s *pyScanner) scanReplacement(i int) int {
	src := s.src
	depth, spec := 0, false
	for i < len(src) {
		c := src[i]
		switch {
		case spec && c == '{':
			i = s.scanReplacement(i + 1)
			continue
		case c == '}' && depth == 0:
			return i + 1
		case spec:
		case c == '"' || c == '\'':
			i = s.scanString(i, "")
			continue
		case isPyIdentByte(c):
			j := i
			for j < len(src) && isPyIdentByte(src[j]) {
				j++
			}
			if (at(src, j) == '"' || at(src, j) == '\'') && isStringPrefix(string(src[i:j])) {
				i = s.scanString(j, string(src[i:j]))
			} else {
				i = j
			}
			continue
		case c == ':' && depth == 0:
			spec = true
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			if depth > 0 {
				depth--
			}
		}
		i++
	}
	return i
}

// isPyIdentByte reports whether c can be part of a name or number. Bytes past
// ASCII are counted in, since Python names may be any letters.
func isPyIdentByte(c byte) bool {
	return c == '_' || c >= 0x80 || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// isStringPrefix reports whether a name directly before a quote is a string
// prefix — r, b, u, f and t (3.14) and their combinations — rather than a name.
func isStringPrefix(p string) bool {
	switch strings.ToLower(p) {
	case "r", "u", "b", "br", "rb", "f", "fr", "rf", "t", "tr", "rt":
		return true
	}
	return false
}

// at returns src[i], or 0 past the end.
func at(src []byte, i int) byte {
	if i < len(src) {
		return src[i]
	}
	return 0
}

// withIndentation adds the indentation finding when it comes before every
// finding the grammar made. A later one is kept out: a statement the grammar
// could not parse — a header missing its ':' — also throws off the indentation
// of the lines after it, and reporting that too would describe one mistake
// twice.
func withIndentation(fs []finding, ind *finding) []finding {
	if ind == nil {
		return fs
	}
	for _, f := range fs {
		if f.start <= ind.start {
			return fs
		}
	}
	return append(fs, *ind)
}
