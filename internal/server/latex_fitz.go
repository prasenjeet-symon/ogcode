package server

import (
	"bytes"
	"image"
	"image/jpeg"
	"regexp"

	"github.com/gen2brain/go-fitz"
)

// Pre-compiled regexes for extracting LaTeX document metadata.
var (
	regexpLatexDocClass = regexp.MustCompile(`\\documentclass(?:\[.*?\])?\{(.+?)\}`)
	regexpLatexTitle    = regexp.MustCompile(`\\title\{(.+?)\}`)
)

// fitzDoc wraps a go-fitz.Document for rendering pages to images.
type fitzDoc struct {
	doc *fitz.Document
}

// fitzOpen opens a PDF file and returns a fitzDoc for rendering.
func fitzOpen(path string) (*fitzDoc, error) {
	doc, err := fitz.New(path)
	if err != nil {
		return nil, err
	}
	return &fitzDoc{doc: doc}, nil
}

// NumPage returns the number of pages in the document.
func (d *fitzDoc) NumPage() int {
	return d.doc.NumPage()
}

// Close closes the document.
func (d *fitzDoc) Close() {
	d.doc.Close()
}

// ImageDPI renders a page at the given DPI and returns an image.
// page is 0-based.
func (d *fitzDoc) ImageDPI(page int, dpi float64) (image.Image, error) {
	return d.doc.ImageDPI(page, dpi)
}

// jpegEncode encodes an image to JPEG with the given quality.
func jpegEncode(w *bytes.Buffer, img image.Image, quality int) error {
	return jpeg.Encode(w, img, &jpeg.Options{Quality: quality})
}
