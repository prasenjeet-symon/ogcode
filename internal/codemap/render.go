package codemap

import (
	"fmt"
	"strings"
)

// maxDocLen caps the doc excerpt on a symbol line. A doc comment earns its
// place by saying what the name cannot; past a clause or two it stops paying
// for the tokens it costs.
const maxDocLen = 80

// Render formats a FileMap for a model to read.
//
// The output is plain text rather than the JSON the pdf_index tool returns.
// That is a deliberate break from the neighbouring tool: JSON spends roughly
// three times the tokens on braces, quotes and repeated key names to carry the
// same fields, and token economy is the entire reason this tool exists. A
// model reads an aligned two-column table just as reliably.
func Render(fm *FileMap) string {
	var b strings.Builder

	fmt.Fprintf(&b, "%s — %s, %d lines\n", fm.Path, fm.Lang, fm.TotalLines)

	if len(fm.Symbols) == 0 {
		b.WriteString("\nNo declarations found. Read the file directly.\n")
		return b.String()
	}

	b.WriteString("jump to any range with: read(path, start_line=N, end_line=M)\n\n")

	width := rangeWidth(fm.Symbols)
	for _, s := range fm.Symbols {
		indent := strings.Repeat("  ", s.Depth)
		fmt.Fprintf(&b, "%*s  %s%s\n", width, formatRange(s), indent, s.Signature)
		if doc := docExcerpt(s); doc != "" {
			fmt.Fprintf(&b, "%*s  %s└ %s\n", width, "", indent, doc)
		}
	}

	var notes []string
	if fm.Omitted > 0 {
		notes = append(notes, fmt.Sprintf("%d further declarations omitted (outline capped at %d)", fm.Omitted, MaxSymbols))
	}
	if fm.ParseError {
		notes = append(notes, "the file has syntax errors; declarations after the damaged region may be missing")
	}
	if fm.Fallback {
		notes = append(notes, "no grammar for this file type — ranges are approximate and end where the next declaration begins")
	}
	if len(notes) > 0 {
		b.WriteString("\n")
		for _, n := range notes {
			fmt.Fprintf(&b, "note: %s\n", n)
		}
	}

	return b.String()
}

// docExcerpt trims the doc comment down to the part the signature does not
// already say. Go convention opens a doc with the identifier itself, which is
// on the line directly above — repeating it wastes tokens on every symbol.
func docExcerpt(s *Symbol) string {
	doc := s.Doc
	if doc == "" {
		return ""
	}
	if s.Name != "" {
		if rest, ok := strings.CutPrefix(doc, s.Name+" "); ok {
			doc = rest
		}
	}
	// Prefer ending on the first full sentence: a doc's opening sentence is
	// written to stand alone, so it reads better than the same number of
	// characters chopped at the cap.
	if i := strings.Index(doc, ". "); i >= 0 && i+1 <= maxDocLen {
		return doc[:i+1]
	}
	if len(doc) <= maxDocLen {
		return doc
	}
	cut := maxDocLen
	for cut > 0 && doc[cut] != ' ' {
		cut--
	}
	if cut == 0 {
		cut = maxDocLen
	}
	return strings.TrimSpace(doc[:cut]) + "…"
}

func formatRange(s *Symbol) string {
	if s.StartLine == s.EndLine {
		return fmt.Sprintf("%d", s.StartLine)
	}
	return fmt.Sprintf("%d-%d", s.StartLine, s.EndLine)
}

func rangeWidth(symbols []*Symbol) int {
	width := 0
	for _, s := range symbols {
		if n := len(formatRange(s)); n > width {
			width = n
		}
	}
	return width
}
