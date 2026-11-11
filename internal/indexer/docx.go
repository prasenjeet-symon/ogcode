package indexer

import (
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/docindex"
	"github.com/prasenjeet-symon/ogcode/internal/docx"
)

// isDocxExt reports whether ext names a Word document the indexer reads through
// ExtractDocxPages. .docx, .docm and .dotx are the same OOXML ZIP and the leaf
// reader opens all three; the list is owned by docindex.DocxExtensions, shared
// with the store's listing query. .doc is not a ZIP container and is excluded
// there, not here, so a new extension is added in exactly one place.
func isDocxExt(ext string) bool {
	ext = strings.ToLower(ext)
	for _, want := range docindex.DocxExtensions {
		if want == ext {
			return true
		}
	}
	return false
}

// isPDFExt reports whether ext names a PDF document the indexer reads through
// ExtractPages. The list is owned by docindex.PDFExtensions, shared with the
// store's listing queries; this dispatch reads it, so a format added there is
// collected, counted and extracted by a run without a second edit.
func isPDFExt(ext string) bool {
	ext = strings.ToLower(ext)
	for _, want := range docindex.PDFExtensions {
		if want == ext {
			return true
		}
	}
	return false
}

// ExtractDocxPages opens the DOCX at docxPath and returns its text grouped into
// pseudo-pages. It delegates to the dedicated docx package so both the indexer
// and the read_docx_page tool can share the same extraction logic without an
// import cycle.
func ExtractDocxPages(docxPath string) ([]PageText, error) {
	pages, err := docx.ExtractPages(docxPath)
	if err != nil {
		return nil, err
	}
	// Convert docx.PageText to indexer.PageText (identical layout).
	result := make([]PageText, len(pages))
	for i, p := range pages {
		result[i] = PageText{PageNum: p.PageNum, Text: p.Text}
	}
	return result, nil
}
