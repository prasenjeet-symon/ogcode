package codemap

import (
	"embed"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	ts "github.com/tree-sitter/go-tree-sitter"
	tsgo "github.com/tree-sitter/tree-sitter-go/bindings/go"
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
	// commentKind is the node kind used for doc comments in this grammar.
	// docStart() walks preceding siblings of this kind to pull a declaration's
	// doc comment into its line range.
	commentKind string

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
var registry = map[string]*language{
	".go": {
		name:        "go",
		newLang:     func() *ts.Language { return ts.NewLanguage(tsgo.Language()) },
		queryFile:   "queries/go.scm",
		commentKind: "comment",
	},

	".ts":  typescript(),
	".mts": typescript(),
	".cts": typescript(),

	".tsx": tsx(),
	".js":  tsx(),
	".jsx": tsx(),
	".mjs": tsx(),
	".cjs": tsx(),
}

// typescript and tsx each build a fresh language value per extension so that
// every entry owns its own sync.Once. Sharing one value across map keys would
// be safe but makes the zero-value-per-entry invariant easy to break later.
func typescript() *language {
	return &language{
		name:        "typescript",
		newLang:     func() *ts.Language { return ts.NewLanguage(tsts.LanguageTypescript()) },
		queryFile:   "queries/typescript.scm",
		commentKind: "comment",
	}
}

func tsx() *language {
	return &language{
		name:        "tsx",
		newLang:     func() *ts.Language { return ts.NewLanguage(tsts.LanguageTSX()) },
		queryFile:   "queries/typescript.scm",
		commentKind: "comment",
	}
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
