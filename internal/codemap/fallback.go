package codemap

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// The fallback scanner covers every extension no grammar handles yet. It is
// deliberately shallow: line-anchored patterns for the declaration forms that
// dominate real source files, with each symbol running until the next one
// starts. That over-reports an end line when a declaration is followed by blank
// lines or trailing comments, which costs a reader a few extra lines on a jump
// — an acceptable trade against returning nothing at all and sending them back
// to reading the whole file.
//
// Anything this gets wrong is fixed by adding the grammar, not by growing the
// patterns. Resist tuning it into a parser.

type fallbackPattern struct {
	kind string
	re   *regexp.Regexp
	// group is the submatch index holding the identifier.
	group int
}

var fallbackPatterns = []fallbackPattern{
	// JS/TS: function, class, interface, type alias, enum, and the arrow-function
	// const form that most modern code actually uses.
	{"func", regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*([A-Za-z_$][\w$]*)`), 1},
	{"type", regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)`), 1},
	{"type", regexp.MustCompile(`^\s*(?:export\s+)?(?:interface|type|enum)\s+([A-Za-z_$][\w$]*)`), 1},
	{"func", regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*(?::[^=]+)?=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_$][\w$]*)\s*(?::[^=]+)?=>`), 1},
	// Python.
	{"func", regexp.MustCompile(`^\s*(?:async\s+)?def\s+([A-Za-z_][\w]*)`), 1},
	{"type", regexp.MustCompile(`^\s*class\s+([A-Za-z_][\w]*)`), 1},
	// Rust.
	{"func", regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?fn\s+([A-Za-z_][\w]*)`), 1},
	{"type", regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:struct|enum|trait|impl)\s+([A-Za-z_][\w]*)`), 1},
	// Kotlin, whose modifiers stack ahead of the keyword: `override suspend fun`,
	// `data class`, `sealed interface`. A bare `class` is already caught above,
	// but without these a Kotlin file mapped its classes and none of its
	// functions. The receiver of an extension function (`fun String.slug()`) is
	// skipped so the entry is named for the function.
	{"func", regexp.MustCompile(`^\s*(?:(?:public|private|protected|internal|open|override|abstract|final|suspend|inline|operator|infix|tailrec|external|actual|expect)\s+)*fun\s+(?:<[^>]*>\s*)?(?:[\w.<>?, ]+\.)?([A-Za-z_]\w*)\s*\(`), 1},
	{"type", regexp.MustCompile(`^\s*(?:(?:public|private|protected|internal|open|abstract|final|sealed|data|enum|annotation|inner|value|inline|companion|expect|actual)\s+)*(?:class|interface|object)\s+([A-Za-z_]\w*)`), 1},
	// Shell.
	{"func", regexp.MustCompile(`^\s*(?:function\s+)?([A-Za-z_][\w-]*)\s*\(\)\s*\{`), 1},
}

// markdownHeading matches an ATX heading as CommonMark defines one: up to three
// spaces of indent, one to six #s, and an optional closing run of #s that has
// to be set off by a space. That last condition is what keeps the text intact —
// "# Using C#" is a heading about C#, and a pattern that stripped any trailing
// #s turned it into "Using C".
var markdownHeading = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t\r]*$`)

// fallbackSymbols scans src line by line for declaration-shaped lines.
func fallbackSymbols(path string, src []byte) []*Symbol {
	lines := sourceLines(src)

	if isMarkdown(path) {
		return markdownSymbols(lines)
	}

	var symbols []*Symbol
	for i, line := range lines {
		// A cheap reject before the regex sweep: real declarations are short
		// enough that scanning a minified line is pure waste.
		if len(line) > 400 {
			continue
		}
		for _, p := range fallbackPatterns {
			m := p.re.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			symbols = append(symbols, &Symbol{
				Kind:      p.kind,
				Name:      m[p.group],
				Signature: capSig(collapse(strings.TrimSuffix(strings.TrimSpace(line), "{"))),
				StartLine: i + 1,
			})
			break
		}
	}

	closeRanges(symbols, len(lines))
	return symbols
}

// sourceLines splits src into lines numbered the way read and countLines
// number them.
//
// A final newline ends the last line rather than opening an empty one after
// it: split naively, a file of N lines came back as N+1, and the last entry in
// every heuristic or markdown map ran to a line past the end of the file. A
// byte-order mark is dropped too, since it is not part of the first line's
// text and would keep a heading on line 1 from matching.
func sourceLines(src []byte) []string {
	lines := strings.Split(strings.TrimPrefix(string(src), "\ufeff"), "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// markdownSymbols outlines a document by its headings, which is the closest
// thing prose has to a declaration.
//
// Lines inside a fenced code block are code, not headings — a shell comment
// "# install" in a ``` block is not a section — and so is YAML front matter,
// whose comments start with # as well. A fence opens with three or more
// backticks or tildes and closes with at least as many of the same character;
// tracking only ``` let a ~~~ block's comments through as headings.
func markdownSymbols(lines []string) []*Symbol {
	var symbols []*Symbol
	fence := ""
	for i := frontMatterEnd(lines); i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if fence != "" {
			if closesFence(trimmed, fence) {
				fence = ""
			}
			continue
		}
		if f := openingFence(trimmed); f != "" {
			fence = f
			continue
		}
		m := markdownHeading.FindStringSubmatch(line)
		if m == nil || strings.TrimSpace(m[2]) == "" {
			continue
		}
		symbols = append(symbols, &Symbol{
			Kind:      "h" + strconv.Itoa(len(m[1])),
			Name:      m[2],
			Signature: strings.Repeat("  ", len(m[1])-1) + m[2],
			StartLine: i + 1,
		})
	}
	closeRanges(symbols, len(lines))
	return symbols
}

// frontMatterEnd returns the index of the first line after a YAML front matter
// block — the --- delimited header static-site tools put at the top of a page —
// or 0 when the file has none.
func frontMatterEnd(lines []string) int {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return 0
	}
	for i := 1; i < len(lines); i++ {
		if t := strings.TrimSpace(lines[i]); t == "---" || t == "..." {
			return i + 1
		}
	}
	return 0
}

// openingFence returns the run of backticks or tildes that opens a fenced code
// block on this line, or "" when the line opens none.
func openingFence(line string) string {
	for _, c := range []byte{'`', '~'} {
		n := 0
		for n < len(line) && line[n] == c {
			n++
		}
		if n >= 3 {
			return line[:n]
		}
	}
	return ""
}

// closesFence reports whether line closes the block fence opened: a run of the
// same character at least as long, with nothing after it.
func closesFence(line, fence string) bool {
	n := 0
	for n < len(line) && line[n] == fence[0] {
		n++
	}
	return n >= len(fence) && strings.TrimSpace(line[n:]) == ""
}

// closeRanges ends each symbol where the next one begins. Without real spans
// this is the best available guess, and it never leaves a gap a reader could
// mistake for "nothing here".
func closeRanges(symbols []*Symbol, totalLines int) {
	for i, s := range symbols {
		if i+1 < len(symbols) {
			s.EndLine = symbols[i+1].StartLine - 1
		} else {
			s.EndLine = totalLines
		}
		if s.EndLine < s.StartLine {
			s.EndLine = s.StartLine
		}
	}
}

func isMarkdown(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown", ".mdx":
		return true
	}
	return false
}
