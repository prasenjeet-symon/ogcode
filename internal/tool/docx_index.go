package tool

import (
	"context"
	"encoding/json"

	"github.com/prasenjeet-symon/ogcode/internal/docindex"
)

// DocxIndexTool returns the stored semantic map for a Word document — pseudo-page
// labels — so the agent can decide which pages to read before calling
// read_docx_page.
type DocxIndexTool struct {
	Store *docindex.Store
}

func NewDocxIndexTool(store *docindex.Store) DocxIndexTool {
	return DocxIndexTool{Store: store}
}

func (DocxIndexTool) ID() string { return docxIndexKind.id }

func (DocxIndexTool) Description() string { return docxIndexKind.description }

func (DocxIndexTool) Parameters() json.RawMessage { return docxIndexKind.parameters() }

// Execute delegates to the shared listing in doc_index.go: pdf_index runs the
// same code over the same store, and only the wording differs.
func (t DocxIndexTool) Execute(_ context.Context, args json.RawMessage, tctx Context) (Result, error) {
	return docxIndexKind.execute(t.Store, args, tctx)
}
