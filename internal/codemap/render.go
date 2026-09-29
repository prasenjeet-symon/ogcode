package codemap

import (
	"fmt"
	"strings"
	"unicode/utf8"
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

	if fm.FromLine > 0 {
		fmt.Fprintf(&b, "%s — %s, %d lines; map of lines %d-%d\n", fm.Path, fm.Lang, fm.TotalLines, fm.FromLine, fm.ToLine)
	} else {
		fmt.Fprintf(&b, "%s — %s, %d lines\n", fm.Path, fm.Lang, fm.TotalLines)
	}

	if len(fm.Symbols) == 0 {
		if fm.FromLine > 0 {
			b.WriteString("\nNo declarations in this range. Read it directly.\n")
			return b.String()
		}
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
	switch {
	case fm.Omitted > 0 && fm.StopLine > 0:
		notes = append(notes, fmt.Sprintf("the outline is capped at %d entries and stops at line %d; %d further declarations are not shown — map the rest with file_map(path, start_line=%d)",
			MaxSymbols, fm.StopLine, fm.Omitted, fm.StopLine+1))
	case fm.Omitted > 0:
		notes = append(notes, fmt.Sprintf("the outline is capped at %d entries, so the %d declarations nested %d or more levels deep are not shown; map one region in full with file_map(path, start_line=N, end_line=M)",
			MaxSymbols, fm.Omitted, fm.OmittedDepth))
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
		// No space to break at — a doc in a script written without them. Cut
		// on a character boundary instead: the byte at the cap can sit inside
		// a multi-byte character, and slicing there emitted invalid UTF-8.
		cut = maxDocLen
		for cut > 0 && !utf8.RuneStart(doc[cut]) {
			cut--
		}
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

// RenderCheck formats a CheckResult for a model to read.
//
// The three outcomes are worded to be unmistakable from each other, because the
// cost of confusing them is the whole point of the check: a clean parse says
// move on, a diagnostic says stop and fix, and an unchecked file says this
// proved nothing. The last is the one worth being loud about — an agent that
// reads "no grammar" as "no errors" has bought false confidence.
func RenderCheck(res *CheckResult) string {
	var b strings.Builder

	if !res.Checked {
		fmt.Fprintf(&b, "NOT CHECKED: %s\n\n", res.Path)
		if res.Reason != "" {
			fmt.Fprintf(&b, "This %s file was not parsed: %s.\n", res.Lang, res.Reason)
		} else {
			b.WriteString("No parser covers this file type, so it was not parsed.\n")
		}
		b.WriteString("This is not a passing result — nothing was verified. Check it another\nway (run the file's own compiler, linter, or test) before relying on it.\n")
		return b.String()
	}

	if len(res.Diagnostics) == 0 {
		fmt.Fprintf(&b, "OK: %s parses cleanly as %s.\n\n", res.Path, res.Lang)
		b.WriteString("No syntax errors. Note this checks grammar only — it says nothing about\nundefined names, types, or whether the code does what you intended.\n")
		return b.String()
	}

	fmt.Fprintf(&b, "SYNTAX ERRORS: %s in %s (%s)\n\n", res.CountLabel(), res.Path, res.Lang)
	b.WriteString(FormatDiagnostics(res.Diagnostics))
	if res.Truncated {
		fmt.Fprintf(&b, "\n(stopped after %d — fix these first, then check again)\n", MaxDiagnostics)
	}
	b.WriteString("\nThe parser recovers and keeps going after an error, so a single mistake can\nproduce several of these, and the reported position is where the parser gave\nup rather than always where the mistake is. Start at the first one.\n")
	b.WriteString(NewerSyntaxCaveat)
	return b.String()
}

// NewerSyntaxCaveat closes every report of errors. A grammar can lag its
// language — TypeScript's `export type *`, C# 14's extension blocks — and a
// construct newer than the grammar reads as an error. Rewriting valid code to
// satisfy the parser is worse than the error it would fix, so the reader is
// told how to tell the two apart.
const NewerSyntaxCaveat = "If you are sure a flagged line is valid — syntax newer than this parser\nknows — confirm with the language's own compiler or linter rather than\nrewriting it.\n"

// FormatDiagnostics renders diagnostic lines on their own, for a caller that
// supplies its own heading — the write and edit tools append a short version of
// this to their result rather than the whole RenderCheck report.
func FormatDiagnostics(diags []Diagnostic) string {
	var b strings.Builder
	for _, d := range diags {
		fmt.Fprintf(&b, "  %d:%d  %s\n", d.Line, d.Column, d.Message)
		if d.Source != "" {
			// Compiler-style gutter. The line number repeats what the position
			// above already said, but it keeps the offending line unambiguous
			// once several diagnostics are stacked, and it survives the log
			// being read out of order.
			fmt.Fprintf(&b, "  %5d | %s\n", d.Line, d.Source)
		}
	}
	return b.String()
}
