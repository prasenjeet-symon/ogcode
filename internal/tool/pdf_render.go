package tool

import (
	"bytes"
	"container/list"
	"fmt"
	"image/jpeg"
	"os"
	"sync"

	"github.com/gen2brain/go-fitz"
)

// Render tuning. The long edge is capped well under common vision-model limits
// (Anthropic resizes images above ~1568px), and DPI is clamped so tiny pages
// aren't upscaled into huge images nor large pages rendered illegibly small.
//
// Pixel-cap note (INFO only — caps intentionally differ, do NOT unify):
// the web frontend attaches images at ~1024px, the backend image path
// downscales to ~1568px (viewMaxLongEdgePx, the Anthropic ceiling), and PDF
// page renders cap at 1400px here because text legibility needs more pixels
// than a photo does. Each cap serves its own consumer.
const (
	renderMaxLongEdgePx = 1400.0
	renderMaxDPI        = 150.0
	renderMinDPI        = 50.0
	renderJPEGQuality   = 80
)

// pdfRenderCacheSize bounds the rendered-page cache. Renders are big (~100KB
// JPEGs), so the cache holds only a handful of recent pages; it exists to
// absorb repeat reads of the same page in one session, not to memoize books.
const pdfRenderCacheSize = 8

// pdfRenderKey identifies one cached render: the file, its content generation
// (mtime+size, so an edited PDF re-renders) and the 1-based page.
type pdfRenderKey struct {
	path  string
	mtime int64
	size  int64
	page  int
}

type pdfRenderEntry struct {
	key  pdfRenderKey
	data []byte
}

var pdfRenderMu sync.Mutex
var pdfRenderLRU = list.New()
var pdfRenderIndex = map[pdfRenderKey]*list.Element{}

// renderPDFPageImage renders a single PDF page (1-based) to a JPEG image,
// sizing it so the long edge stays near renderMaxLongEdgePx. It returns the
// encoded bytes and the media type ("image/jpeg"). Recent renders are cached
// in a small LRU keyed by path+mtime+size+page, so repeat reads of one page
// (pdf_index then read_pdf_page, or a cached-then-fresh retry) don't reopen
// the document through fitz every call.
func renderPDFPageImage(path string, page int) ([]byte, string, error) {
	var mtime, size int64
	if fi, err := os.Stat(path); err == nil {
		mtime = fi.ModTime().UnixNano()
		size = fi.Size()
	}
	key := pdfRenderKey{path: path, mtime: mtime, size: size, page: page}
	if data, ok := pdfRenderCacheGet(key); ok {
		return data, "image/jpeg", nil
	}

	doc, err := fitz.New(path)
	if err != nil {
		return nil, "", fmt.Errorf("open pdf for render: %w", err)
	}
	defer doc.Close()

	if page < 1 || page > doc.NumPage() {
		return nil, "", fmt.Errorf("page %d out of range (document has %d pages)", page, doc.NumPage())
	}
	idx := page - 1 // go-fitz uses 0-based page indices

	dpi := renderMaxDPI
	if b, err := doc.Bound(idx); err == nil {
		longPts := b.Dx()
		if b.Dy() > longPts {
			longPts = b.Dy()
		}
		if longPts > 0 {
			longInches := float64(longPts) / 72.0
			dpi = renderMaxLongEdgePx / longInches
			if dpi > renderMaxDPI {
				dpi = renderMaxDPI
			}
			if dpi < renderMinDPI {
				dpi = renderMinDPI
			}
		}
	}

	img, err := doc.ImageDPI(idx, dpi)
	if err != nil {
		return nil, "", fmt.Errorf("render page %d: %w", page, err)
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: renderJPEGQuality}); err != nil {
		return nil, "", fmt.Errorf("encode page %d to jpeg: %w", page, err)
	}
	data := buf.Bytes()
	pdfRenderCachePut(key, data)
	return data, "image/jpeg", nil
}

// pdfRenderCacheGet returns a copy of the cached render for key, moving it to
// the front of the LRU.
func pdfRenderCacheGet(key pdfRenderKey) ([]byte, bool) {
	pdfRenderMu.Lock()
	defer pdfRenderMu.Unlock()
	el, ok := pdfRenderIndex[key]
	if !ok {
		return nil, false
	}
	pdfRenderLRU.MoveToFront(el)
	data := el.Value.(pdfRenderEntry).data
	out := make([]byte, len(data))
	copy(out, data)
	return out, true
}

// pdfRenderCachePut stores a copy of data under key, evicting the oldest entry
// past the size bound.
func pdfRenderCachePut(key pdfRenderKey, data []byte) {
	pdfRenderMu.Lock()
	defer pdfRenderMu.Unlock()
	if el, ok := pdfRenderIndex[key]; ok {
		pdfRenderLRU.MoveToFront(el)
		el.Value = pdfRenderEntry{key: key, data: append([]byte(nil), data...)}
		return
	}
	el := pdfRenderLRU.PushFront(pdfRenderEntry{key: key, data: append([]byte(nil), data...)})
	pdfRenderIndex[key] = el
	for pdfRenderLRU.Len() > pdfRenderCacheSize {
		back := pdfRenderLRU.Back()
		if back == nil {
			return
		}
		pdfRenderLRU.Remove(back)
		delete(pdfRenderIndex, back.Value.(pdfRenderEntry).key)
	}
}
