package docindex

import (
	"fmt"
	"strings"
)

// DocxExtensions are the file extensions treated as Word documents everywhere
// the document index understands them. All three are the same OOXML ZIP
// container — a macro-enabled document (.docm) and a template (.dotx) differ
// from a .docx only in their declared content types — so the one reader in
// internal/docx opens them alike, and every stored page of one belongs to a
// single document.
//
// This slice is the single owner of the list: the store's query clauses (see
// docxPathMatchConditions / docxPathExclusionConditions) and the indexer's
// extension dispatch read it.
//
// .doc is deliberately in NO list: it is an OLE/BIFF compound file, not a ZIP,
// so it needs a different reader and would fail extraction. .dotm (a
// macro-enabled template) is out of scope for now; both belong to neither the
// word-page listing nor the text listing.
var DocxExtensions = []string{".docx", ".docm", ".dotx"}

// docxPathMatchConditions returns "(LOWER(doc_path) LIKE '%.docx' OR ...)"
// matching every stored path whose extension is one of DocxExtensions.
// The extensions are compile-time literals, so the assembled SQL cannot
// inject anything.
func docxPathMatchConditions() string {
	clauses := make([]string, len(DocxExtensions))
	for i, ext := range DocxExtensions {
		clauses[i] = fmt.Sprintf("LOWER(doc_path) LIKE '%%%s'", ext)
	}
	return "(" + strings.Join(clauses, " OR ") + ")"
}

// docxPathExclusionConditions returns one "AND LOWER(doc_path) NOT LIKE"
// clause per extension in DocxExtensions, for queries that must not count
// any Word-document page as a text file.
func docxPathExclusionConditions() string {
	return pathExclusionConditions(DocxExtensions)
}
