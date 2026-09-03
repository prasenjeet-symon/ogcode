package codemap

import (
	"embed"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	tsdart "github.com/UserNobody14/tree-sitter-dart/bindings/go"
	ts "github.com/tree-sitter/go-tree-sitter"
	tscs "github.com/tree-sitter/tree-sitter-c-sharp/bindings/go"
	tscss "github.com/tree-sitter/tree-sitter-css/bindings/go"
	tsgo "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tshtml "github.com/tree-sitter/tree-sitter-html/bindings/go"
	tsjava "github.com/tree-sitter/tree-sitter-java/bindings/go"
	tsphp "github.com/tree-sitter/tree-sitter-php/bindings/go"
	tspy "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tsrs "github.com/tree-sitter/tree-sitter-rust/bindings/go"

	tssw "github.com/prasenjeet-symon/ogcode/internal/codemap/grammars/swift"
	tsts "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

//go:embed queries/*.scm
var queryFS embed.FS

// language binds a file extension to a tree-sitter grammar and the outline
// query run against it.
//
// The grammar and the compiled query are built once on first use and shared
// from then on. Compiling a query means parsing the .scm and building the
// pattern automaton, which is far more expensive than the parse it serves; a
// *ts.Query is only mutated by DisableCapture/DisablePattern, which this
// package never calls, so sharing one across goroutines is safe. The mutable
// half of matching lives in the per-call *ts.QueryCursor.
type language struct {
	// name is what the outline header reports.
	name string
	// newLang returns the grammar. Held as a func so the CGO grammar is only
	// touched for files that actually need it.
	newLang func() *ts.Language
	// queryFile names the .scm under queries/.
	queryFile string
	// commentKinds lists the node kinds that hold a doc comment. docStart()
	// walks preceding siblings of these kinds to pull a declaration's doc
	// comment into its line range. Most grammars name one kind; Rust splits
	// line and block comments into two.
	commentKinds []string
	// attrKind names a node that precedes a declaration as a sibling and
	// belongs to it — Rust's attribute_item. It sits between the doc comment
	// and the declaration it annotates, so without skipping over it the comment
	// walk stops at the attribute and every annotated item loses its doc.
	// Python's decorators nest instead, and use wrapperKind.
	attrKind string
	// wrapperKind names a node that wraps a declaration and carries part of its
	// range — Python's decorated_definition. The queries capture the inner
	// definition, because that is what holds the name and the signature, so
	// without this a decorated declaration would start below its decorators.
	// Empty for grammars where no such wrapper exists.
	wrapperKind string
	// trailingBodyKind names a node that follows a declaration as a sibling and
	// holds the body belonging to it — Dart's function_body. It is the mirror of
	// attrKind: where an attribute sits above the declaration and widens its
	// range upward, this sits below and widens it downward.
	//
	// Without it a Dart symbol's range covers its signature line and stops. The
	// range is what a reader hands to read(), so a method would come back
	// without the code inside it — the one thing the range exists to deliver.
	trailingBodyKind string
	// xmlDocs marks a language whose doc comments are XML rather than prose —
	// C# and the /// <summary> convention. Without it the excerpt spends its
	// budget on markup instead of on the sentence the markup wraps.
	xmlDocs bool
	// docstrings marks a language that documents a declaration from the inside,
	// with a string literal at the top of its body, rather than with a comment
	// above it. docStart finds nothing in such a language; docstringOf does.
	docstrings bool

	once  sync.Once
	lang  *ts.Language
	query *ts.Query
	err   error
}

// registry maps lowercased file extension to its language.
//
// Adding a language is an entry here plus a queries/<lang>.scm; everything
// downstream — doc-comment attachment, name extraction, signature rendering,
// the tool itself — is grammar-agnostic. Each grammar compiles its parser.c
// into the binary for all six release targets, so they earn their way in one at
// a time rather than arriving as a bundle.
//
// TypeScript ships two parsers in one module and both are registered: TSX
// cannot parse the `<T>expr` type assertion (it reads as JSX), and TypeScript
// cannot parse JSX at all, so neither covers the other's files.
//
// Plain JavaScript is routed to the TSX parser rather than pulling in a third
// grammar. TypeScript is a syntactic superset of JavaScript and TSX adds JSX on
// top, so every .js and .jsx file is valid input — and .js files carrying JSX,
// which a bare TypeScript parser would reject, parse correctly.
//
// PHP likewise ships two parsers, and here only one is registered: `php` reads
// a file as text that opens into code at `<?php`, which is what a file on disk
// is, while `php_only` expects a bare fragment with no tags. Templates (.phtml)
// return to HTML between blocks, and `php` handles that in the same parser.
//
// Python is the first entry to need more than a grammar and a query: it hangs
// decorators above the declaration they modify and its doc lives inside the
// body, so it sets wrapperKind and docstrings as well.
//
// Rust needs the third such accommodation. It splits comments into two node
// kinds, marks a doc comment with a third slash, and puts attributes between
// the doc comment and the item — as siblings rather than as a wrapper.
//
// Swift needs none of them. It splits comments in two the way Rust does, but
// parses its attributes into a (modifiers) node inside the declaration, so the
// range and the signature cover them without help. Its grammar is the one
// entry that is vendored rather than required — see grammars/swift.
//
// Java is the same shape as Swift and needs nothing beyond the two comment
// kinds: its annotations also live in a (modifiers) node inside the
// declaration.
//
// HTML captures by attribute rather than by declaration kind: an element is
// worth an outline line only when it carries an id or a class, the two
// attributes authors put on a region worth jumping to. script and style
// elements are captured unconditionally, since their bodies parse as a
// raw_text leaf that no other query can see into. Names come from the id, the
// class, or the tag, in that order — markup names itself by role, not by
// syntax.
//
// CSS names a rule by the selectors text itself: there is nothing else to
// name it by, and a collapsed selector line is exactly what a reader scans a
// stylesheet for. Its patterns stay unanchored because rules nest inside the
// blocks of other rules — anchoring under the stylesheet would hide a media
// query's rules, the bulk of most real stylesheets.
var registry = map[string]*language{
	".go": {
		name:         "go",
		newLang:      func() *ts.Language { return ts.NewLanguage(tsgo.Language()) },
		queryFile:    "queries/go.scm",
		commentKinds: []string{"comment"},
	},

	".ts":  typescript(),
	".mts": typescript(),
	".cts": typescript(),

	".tsx": tsx(),
	".js":  tsx(),
	".jsx": tsx(),
	".mjs": tsx(),
	".cjs": tsx(),

	".php":   php(),
	".phtml": php(),

	".py":  python(),
	".pyi": python(),
	".pyw": python(),

	".rs": rust(),

	".cs":  csharp(),
	".csx": csharp(),

	".dart": dart(),

	".swift": swift(),

	".java": java(),

	".html": html(),
	".htm":  html(),

	".css": css(),
}

// LanguageNames returns the name of every language a real grammar covers, sorted
// and de-duplicated. It exists so callers that describe that coverage to a model
// — FileMapTool.Description — can be pinned against the registry instead of
// against a list someone remembered to update. Adding a grammar without saying
// so leaves the agent treating an approximate scan as exact, and vice versa.
func LanguageNames() []string {
	seen := make(map[string]bool, len(registry))
	for _, l := range registry {
		seen[l.name] = true
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// typescript and tsx each build a fresh language value per extension so that
// every entry owns its own sync.Once. Sharing one value across map keys would
// be safe but makes the zero-value-per-entry invariant easy to break later.
func typescript() *language {
	return &language{
		name:         "typescript",
		newLang:      func() *ts.Language { return ts.NewLanguage(tsts.LanguageTypescript()) },
		queryFile:    "queries/typescript.scm",
		commentKinds: []string{"comment"},
	}
}

func tsx() *language {
	return &language{
		name:         "tsx",
		newLang:      func() *ts.Language { return ts.NewLanguage(tsts.LanguageTSX()) },
		queryFile:    "queries/typescript.scm",
		commentKinds: []string{"comment"},
	}
}

// php builds a fresh language value per extension, for the same
// once-per-entry reason as typescript and tsx above.
func php() *language {
	return &language{
		name:         "php",
		newLang:      func() *ts.Language { return ts.NewLanguage(tsphp.LanguagePHP()) },
		queryFile:    "queries/php.scm",
		commentKinds: []string{"comment"},
	}
}

// python builds a fresh language value per extension, for the same
// once-per-entry reason as typescript and tsx above.
//
// commentKind is still set: a `#` comment above a declaration is the only doc a
// module-level constant can carry, since a binding has no body to hold a
// docstring.
func python() *language {
	return &language{
		name:         "python",
		newLang:      func() *ts.Language { return ts.NewLanguage(tspy.Language()) },
		queryFile:    "queries/python.scm",
		commentKinds: []string{"comment"},
		wrapperKind:  "decorated_definition",
		docstrings:   true,
	}
}

// rust builds a fresh language value per extension, for the same
// once-per-entry reason as typescript and tsx above.
//
// No wrapperKind: Rust hangs its attributes beside the item rather than around
// it, which is what attrKind is for.
func rust() *language {
	return &language{
		name:         "rust",
		newLang:      func() *ts.Language { return ts.NewLanguage(tsrs.Language()) },
		queryFile:    "queries/rust.scm",
		commentKinds: []string{"line_comment", "block_comment"},
		attrKind:     "attribute_item",
	}
}

// swift builds a fresh language value per extension, for the same
// once-per-entry reason as typescript and tsx above.
//
// Neither wrapperKind nor attrKind: Swift keeps its attributes inside the
// declaration node, so nothing has to be walked to find them.
func swift() *language {
	return &language{
		name:         "swift",
		newLang:      func() *ts.Language { return ts.NewLanguage(tssw.Language()) },
		queryFile:    "queries/swift.scm",
		commentKinds: []string{"comment", "multiline_comment"},
	}
}

// csharp builds a fresh language value per extension, for the same
// once-per-entry reason as typescript and tsx above.
//
// Neither wrapperKind nor attrKind: C# keeps its attributes inside the
// declaration node, as Java and Swift do with annotations and attributes, so
// nothing has to be walked to find them.
//
// One commentKind covers all three spellings — //, /* */ and the /// that
// carries XML documentation all parse as (comment).
func csharp() *language {
	return &language{
		// The display name is the language's own spelling. It reaches the
		// model twice — in the outline header and in the file_map description's
		// list of exactly-parsed languages — and those two have to agree, which
		// a test pins against this registry. "csharp" survives only in the
		// query filename, where the "#" would be awkward.
		name:         "c#",
		newLang:      func() *ts.Language { return ts.NewLanguage(tscs.Language()) },
		queryFile:    "queries/csharp.scm",
		commentKinds: []string{"comment"},
		xmlDocs:      true,
	}
}

// dart builds a fresh language value per extension, for the same
// once-per-entry reason as typescript and tsx above.
//
// Dart is the only grammar so far that needs both halves of the range widened.
// An annotation on a class member is a preceding sibling, as in Rust, so
// attrKind carries the start up over @override; and a function's body is a
// following sibling rather than a child, so trailingBodyKind carries the end
// down over it.
//
// Two comment kinds, for a reason unlike Rust's: Dart does not split line from
// block, it splits documentation from ordinary comment. /// and /** */ parse as
// documentation_comment and everything else as comment, and a declaration
// documented with a plain // — which the analyzer does not treat as a doc, but
// which real code is full of — would lose it if only the first were listed.
func dart() *language {
	return &language{
		name:             "dart",
		newLang:          func() *ts.Language { return ts.NewLanguage(tsdart.Language()) },
		queryFile:        "queries/dart.scm",
		commentKinds:     []string{"documentation_comment", "comment"},
		attrKind:         "annotation",
		trailingBodyKind: "function_body",
	}
}

// java builds a fresh language value per extension, for the same
// once-per-entry reason as typescript and tsx above.
//
// Neither wrapperKind nor attrKind: like Swift, Java keeps its annotations
// inside the declaration node, so nothing has to be walked to find them.
func java() *language {
	return &language{
		name:         "java",
		newLang:      func() *ts.Language { return ts.NewLanguage(tsjava.Language()) },
		queryFile:    "queries/java.scm",
		commentKinds: []string{"line_comment", "block_comment"},
	}
}

// html builds a fresh language value per extension, for the same
// once-per-entry reason as typescript and tsx above.
//
// .html and .htm share one builder and one query. The capture side — which
// elements earn a line — lives entirely in the query's id/class predicate and
// the unconditional script/style patterns, so neither accommodation beyond
// commentKinds is needed: comments attach through the standard sibling walk.
func html() *language {
	return &language{
		name:         "html",
		newLang:      func() *ts.Language { return ts.NewLanguage(tshtml.Language()) },
		queryFile:    "queries/html.scm",
		commentKinds: []string{"comment"},
	}
}

// css builds the stylesheet entry, for the same once-per-entry reason as
// typescript and tsx above. Declarations are left to the rule that contains
// them, so the outline stays one line per selector.
func css() *language {
	return &language{
		name:         "css",
		newLang:      func() *ts.Language { return ts.NewLanguage(tscss.Language()) },
		queryFile:    "queries/css.scm",
		commentKinds: []string{"comment"},
	}
}

// isComment reports whether kind is one of the comment kinds this grammar uses.
func (l *language) isComment(kind string) bool {
	for _, k := range l.commentKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// lookup returns the language for a path, or nil when no grammar covers it.
func lookup(path string) *language {
	return registry[strings.ToLower(filepath.Ext(path))]
}

// load compiles the grammar and query on first use and returns the shared pair.
func (l *language) load() (*ts.Language, *ts.Query, error) {
	l.once.Do(func() {
		src, err := queryFS.ReadFile(l.queryFile)
		if err != nil {
			l.err = fmt.Errorf("read %s: %w", l.queryFile, err)
			return
		}
		l.lang = l.newLang()
		q, qErr := ts.NewQuery(l.lang, string(src))
		if qErr != nil {
			// A query that fails to compile is a bug in a file we ship, not
			// anything the caller did — say which pattern so it is findable.
			l.err = fmt.Errorf("compile %s at row %d col %d: %s",
				l.queryFile, qErr.Row, qErr.Column, qErr.Message)
			return
		}
		l.query = q
	})
	return l.lang, l.query, l.err
}
