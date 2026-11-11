package tool

import (
	"context"
	"encoding/json"

	"github.com/prasenjeet-symon/ogcode/internal/docindex"
)

// PdfIndexTool returns the stored semantic map for a PDF — page labels — so the
// agent can decide which pages to read before calling read_pdf_page.
type PdfIndexTool struct {
	Store *docindex.Store
}

func NewPdfIndexTool(store *docindex.Store) PdfIndexTool {
	return PdfIndexTool{Store: store}
}

func (PdfIndexTool) ID() string { return pdfIndexKind.id }

func (PdfIndexTool) Description() string { return pdfIndexKind.description }

func (PdfIndexTool) Parameters() json.RawMessage { return pdfIndexKind.parameters() }

// Execute delegates to the shared listing in doc_index.go: docx_index runs the
// same code over the same store, and only the wording differs.
func (t PdfIndexTool) Execute(_ context.Context, args json.RawMessage, tctx Context) (Result, error) {
	return pdfIndexKind.execute(t.Store, args, tctx)
}
