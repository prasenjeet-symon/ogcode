package docindex

import (
	"fmt"
	"strings"
)

// SheetExtensions are the file extensions treated as spreadsheets everywhere
// the document index understands them. Every stored page of such a document
// belongs to one sheet (a delimited file is the single sheet "Sheet1"), so
// callers group the pages by DocPath instead of counting rows as files.
//
// This slice is the single owner of the list: the store's query clauses (see
// sheetPathMatchConditions / sheetPathExclusionConditions) and the indexer's
// extension dispatch read it. internal/sheet (the leaf profiling package) and
// internal/server/query_sheet_loader keep their own copies, since importing
// docindex from a leaf is not allowed and those files sit outside this
// package's seam — parity is pinned by tests in internal/docindex and
// internal/indexer.
//
// .xls and .ods are deliberately in NO list (excelize cannot read BIFF/ODF),
// so they belong to neither the sheet pages nor the text listing.
var SheetExtensions = []string{".xlsx", ".xlsm", ".csv", ".tsv"}

// sheetPathMatchConditions returns "(LOWER(doc_path) LIKE '%.xlsx' OR ...)"
// matching every stored path whose extension is one of SheetExtensions.
// The extensions are compile-time literals, so the assembled SQL cannot
// inject anything.
func sheetPathMatchConditions() string {
	clauses := make([]string, len(SheetExtensions))
	for i, ext := range SheetExtensions {
		clauses[i] = fmt.Sprintf("LOWER(doc_path) LIKE '%%%s'", ext)
	}
	return "(" + strings.Join(clauses, " OR ") + ")"
}

// sheetPathExclusionConditions returns one "AND LOWER(doc_path) NOT LIKE"
// clause per extension in SheetExtensions, for queries that must not count
// any sheet page as a text file.
func sheetPathExclusionConditions() string {
	clauses := make([]string, len(SheetExtensions))
	for i, ext := range SheetExtensions {
		clauses[i] = fmt.Sprintf("AND LOWER(doc_path) NOT LIKE '%%%s'", ext)
	}
	return strings.Join(clauses, " ")
}
