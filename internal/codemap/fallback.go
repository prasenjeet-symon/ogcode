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
	// Shell.
	{"func", regexp.MustCompile(`^\s*(?:function\s+)?([A-Za-z_][\w-]*)\s*\(\)\s*\{`), 1},
}

var markdownHeading = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*#*\s*$`)

// fallbackSymbols scans src line by line for declaration-shaped lines.
func fallbackSymbols(path string, src []byte) []*Symbol {
	lines := strings.Split(string(src), "\n")

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

// markdownSymbols outlines a document by its headings, which is the closest
// thing prose has to a declaration.
func markdownSymbols(lines []string) []*Symbol {
	var symbols []*Symbol
	inFence := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		m := markdownHeading.FindStringSubmatch(line)
		if m == nil {
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
