package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ledongthuc/pdf"
)

// imageFallbackWordThreshold is the word count below which a page is treated as
// image/graph-heavy. Such pages yield little useful text, so for vision-capable
// models we render the page to an image instead.
const imageFallbackWordThreshold = 20

// ReadPdfPageTool extracts the text of a single PDF page for the agent to read.
type ReadPdfPageTool struct{}

func (ReadPdfPageTool) ID() string { return "read_pdf_page" }

func (ReadPdfPageTool) Description() string {
	return "Extract the plain text of a single page from a PDF file. Use pdf_index first to identify which pages are relevant, then call this tool to read their content. Pages dominated by graphs, diagrams, or scanned images return a rendered image instead of text when the model supports vision."
}

func (ReadPdfPageTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["path", "page"],
		"properties": {
			"path": {
				"type": "string",
				"description": "Path to the PDF file (absolute or relative to session directory)"
			},
			"page": {
				"type": "integer",
				"description": "1-based page number to read"
			}
		}
	}`)
}

func (ReadPdfPageTool) Execute(_ context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var params struct {
		Path string `json:"path"`
		Page int    `json:"page"`
	}
	if err := DecodeArgs(args, &params); err != nil {
		return Result{}, fmt.Errorf("parse read_pdf_page args: %w", err)
	}
	if params.Path == "" {
		return Result{Title: "Read PDF Page", Output: "path is required"}, nil
	}
	if params.Page < 1 {
		return Result{Title: "Read PDF Page", Output: "page must be >= 1"}, nil
	}

	path := params.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(tctx.SessionDir, path)
	}

	f, reader, err := pdf.Open(path)
	if err != nil {
		return Result{}, fmt.Errorf("open pdf %s: %w", path, err)
	}
	defer f.Close()

	numPages := reader.NumPage()
	if params.Page > numPages {
		return Result{
			Title:  fmt.Sprintf("PDF Page %d", params.Page),
			Output: fmt.Sprintf("page %d out of range (document has %d pages)", params.Page, numPages),
		}, nil
	}

	p := reader.Page(params.Page)
	if p.V.IsNull() {
		return Result{
			Title:  fmt.Sprintf("PDF Page %d", params.Page),
			Output: "(empty page)",
		}, nil
	}

	fonts := make(map[string]*pdf.Font)
	for _, name := range p.Fonts() {
		f := p.Font(name)
		fonts[name] = &f
	}
	text, err := p.GetPlainText(fonts)
	if err != nil {
		return Result{}, fmt.Errorf("extract text from page %d: %w", params.Page, err)
	}

	text = strings.TrimSpace(text)

	title := fmt.Sprintf("PDF Page %d / %s", params.Page, filepath.Base(path))

	// If the page yields little text, it is likely a graph/diagram/scanned image.
	// For vision-capable models, render the page and return it as an image.
	if len(strings.Fields(text)) < imageFallbackWordThreshold && tctx.ModelSupportsImages {
		if data, mediaType, rerr := renderPDFPageImage(path, params.Page); rerr == nil {
			note := fmt.Sprintf("Page %d has little extractable text (likely a graph, diagram, or scanned image); a rendered image of the page is attached.", params.Page)
			if text != "" {
				note += "\n\nExtracted text:\n" + text
			}
			return Result{
				Title:  title,
				Output: note,
				Image: &ResultImage{
					MediaType: mediaType,
					Data:      base64.StdEncoding.EncodeToString(data),
				},
			}, nil
		}
		// Render failed — fall through to returning whatever text we have.
	}

	if text == "" {
		text = "(no extractable text on this page)"
	}

	return Result{
		Title:  title,
		Output: text,
	}, nil
}
