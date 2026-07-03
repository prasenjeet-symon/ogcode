package indexer

import (
	"github.com/prasenjeet-symon/ogcode/internal/docx"
)

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