package docindex

import (
	"fmt"
	"strings"
)

// PDFExtensions are the file extensions treated as PDF documents by the
// document index. ".pdf" is the only one for now; the slice exists so the
// store's two clauses (see pdfPathMatchConditions / pdfPathExclusionConditions)
// derive from one list instead of spelling the extension inline.
//
// The list is owned by this seam. The indexer reaches it through isPDFExt in
// internal/indexer/docx.go rather than comparing the literal extension, so a
// format added here is collected, counted and extracted by indexer.Run without
// a second edit. That helper is the only indexer-side gate.
var PDFExtensions = []string{".pdf"}

// pdfPathMatchConditions returns "(LOWER(doc_path) LIKE '%.pdf')" matching
// every stored path whose extension is one of PDFExtensions.
// The extensions are compile-time literals, so the assembled SQL cannot
// inject anything.
func pdfPathMatchConditions() string {
	clauses := make([]string, len(PDFExtensions))
	for i, ext := range PDFExtensions {
		clauses[i] = fmt.Sprintf("LOWER(doc_path) LIKE '%%%s'", ext)
	}
	return "(" + strings.Join(clauses, " OR ") + ")"
}

// pdfPathExclusionConditions returns one "AND LOWER(doc_path) NOT LIKE"
// clause per extension in PDFExtensions, for queries that must not count
// any PDF page as a text file.
func pdfPathExclusionConditions() string {
	return pathExclusionConditions(PDFExtensions)
}
