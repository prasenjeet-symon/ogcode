package tool

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/docx"
)

// docxPageCacheCap bounds how many parsed documents stay resident, and
// docxPageCacheMaxBytes bounds the extracted text they hold in total. The entry
// cap alone was not a memory bound: eight long reports can hold tens of MB
// between them, so the byte budget is what actually keeps a long session from
// accumulating parsed books it will never open again. The cache always keeps at
// least one entry — a document larger than the budget is still served from
// memory rather than re-parsed on every page read.
const (
	docxPageCacheCap      = 8
	docxPageCacheMaxBytes = 32 << 20
)

// docxPageCacheEntry is one parsed document with the identity its staleness is
// judged by: the file's size, modification time and content stamp, all as they
// stood when the entry was parsed. textBytes is the cached text it costs (see
// pageTextBytes), kept alongside those so eviction can weigh the entries.
type docxPageCacheEntry struct {
	pages     []docx.PageText
	size      int64         // the file's size, part of the cache identity
	textBytes int64         // the cached text this entry accounts for
	modTime   int64         // UnixNano, as stat reported it at parse time
	stamp     docxPageStamp // content stamp of the file at parse time
	lastUsed  time.Time
}

// docxPageCache holds parsed documents keyed by absolute path. An entry is valid
// only while the file still carries the same size, modification time and content
// stamp; eviction is least-recently-used while either bound is broken: more than
// docxPageCacheCap entries, or more than maxBytes of extracted text.
//
// The stamp alone does not make the identity exact. Size and mtime are preserved
// by a plain copy (`cp -p`, `rsync -a`, a git checkout or stash, a backup
// restore) and coarsened by a filesystem with a coarse mtime, and a rename leaves
// the old path's entry with nothing to notice at that path. Any of those is
// caught only when the first or last docxStampBytes of the file differ; a file
// whose changed bytes all fall outside those two windows, at the same size and
// mtime, is still served from the earlier parse. That is the guarantee: two small
// reads per hit, not a full-file checksum.
type docxPageCache struct {
	mu       sync.Mutex
	entries  map[string]*docxPageCacheEntry
	total    int64            // sum of the entries' textBytes
	maxBytes int64            // byte budget; a field so tests can shrink it
	clock    func() time.Time // injectable for LRU tests
}

func newDocxPageCache() *docxPageCache {
	return &docxPageCache{
		entries:  make(map[string]*docxPageCacheEntry),
		maxBytes: docxPageCacheMaxBytes,
		clock:    time.Now,
	}
}

var docxPages = newDocxPageCache()

// get returns the pages cached for path while the file still carries the same
// size, modification time and content stamp they were parsed from. The second
// result is false on a miss, so a document that legitimately parses to no pages
// is not mistaken for one that was never parsed. A stale entry is dropped: the
// file is re-read, never served from memory.
func (c *docxPageCache) get(path string, size, modTime int64, stamp docxPageStamp) ([]docx.PageText, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[path]
	if !ok {
		return nil, false
	}
	if e.size != size || e.modTime != modTime || e.stamp != stamp {
		delete(c.entries, path)
		c.total -= e.textBytes
		return nil, false
	}
	e.lastUsed = c.clock()
	return e.pages, true
}

// put installs a freshly parsed document, evicting least-recently-used entries
// until both the entry cap and the byte budget hold.
func (c *docxPageCache) put(path string, pages []docx.PageText, size, modTime int64, stamp docxPageStamp) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.entries[path]; ok {
		c.total -= old.textBytes
	}
	e := &docxPageCacheEntry{
		pages:     pages,
		size:      size,
		textBytes: pageTextBytes(pages),
		modTime:   modTime,
		stamp:     stamp,
		lastUsed:  c.clock(),
	}
	c.entries[path] = e
	c.total += e.textBytes

	// The last entry is never evicted: a single document bigger than the budget
	// is still served from memory instead of being re-parsed on every read.
	for len(c.entries) > 1 && (len(c.entries) > docxPageCacheCap || c.total > c.maxBytes) {
		victim := ""
		var oldest time.Time
		for p, e := range c.entries {
			if victim == "" || e.lastUsed.Before(oldest) {
				victim, oldest = p, e.lastUsed
			}
		}
		c.total -= c.entries[victim].textBytes
		delete(c.entries, victim)
	}
}

// pageTextBytes is the cached text an entry accounts for in the cache's budget:
// the extracted page text, not the slice headers or the parsed document behind
// it.
func pageTextBytes(pages []docx.PageText) int64 {
	var n int64
	for _, p := range pages {
		n += int64(len(p.Text))
	}
	return n
}

// docxStampBytes is how much of each end of the file the content stamp covers.
const docxStampBytes = 4 << 10

// docxPageStamp is the cheap content identity the cache checks on every hit: the
// sha256 of the file's first and last docxStampBytes. It exists because an mtime
// is not an identity on its own: cp -p, rsync -a, git checkout/stash and backup
// restores preserve it, coarse-mtime filesystems coarsen it, and a rename leaves
// the old path's entry so a different file landing there with the same size and
// mtime would otherwise be served from the previous parse.
type docxPageStamp [sha256.Size]byte

// docxPageStampFor hashes the first and last docxStampBytes of the file at path,
// whose size the caller already stat'ed. A cache hit costs stat plus these two
// small reads. A file that cannot be read reports an error so the caller skips
// the cache and lets the extractor report the failure.
func docxPageStampFor(path string, size int64) (docxPageStamp, error) {
	var stamp docxPageStamp
	f, err := os.Open(path)
	if err != nil {
		return stamp, err
	}
	defer f.Close()

	h := sha256.New()
	buf := make([]byte, docxStampBytes)
	n, err := f.ReadAt(buf, 0)
	if err != nil && err != io.EOF {
		return stamp, err
	}
	h.Write(buf[:n])
	if size > docxStampBytes {
		n, err = f.ReadAt(buf, size-docxStampBytes)
		if err != nil && err != io.EOF {
			return stamp, err
		}
		h.Write(buf[:n])
	}
	copy(stamp[:], h.Sum(nil))
	return stamp, nil
}

// docxPagesFor returns the parsed pseudo-pages of the document at path, reusing
// an earlier parse while the file is unchanged. Every call used to open the
// archive and re-parse word/document.xml, so reading pages 1, 2 and 3 parsed the
// whole document three times; a cache hit costs one stat, two small reads and a
// hash.
func docxPagesFor(path string) ([]docx.PageText, error) {
	key, err := filepath.Abs(path)
	if err != nil {
		key = path // an unresolvable path still works as an identity
	}
	// A failing stat falls through to the extractor, so a missing or unreadable
	// file reports the extractor's error rather than a bare stat error, and a
	// file whose content cannot be stamped skips the cache the same way.
	var size, modTime int64
	var stamp docxPageStamp
	statOK := false
	if info, err := os.Stat(path); err == nil {
		size, modTime = info.Size(), info.ModTime().UnixNano()
		if s, serr := docxPageStampFor(path, size); serr == nil {
			stamp, statOK = s, true
			if pages, ok := docxPages.get(key, size, modTime, stamp); ok {
				return pages, nil
			}
		}
	}
	pages, err := docx.ExtractPages(path)
	if err != nil {
		return nil, err
	}
	if statOK {
		docxPages.put(key, pages, size, modTime, stamp)
	}
	return pages, nil
}

// ReadDocxPageTool extracts the text of a single pseudo-page from a Word document
// for the agent to read. Use docx_index first to identify which pages are
// relevant, then call this tool to read their content.
type ReadDocxPageTool struct{}

func (ReadDocxPageTool) ID() string { return "read_docx_page" }

func (ReadDocxPageTool) Description() string {
	return "Extract the plain text of a single pseudo-page from a Word document (.docx, .docm or .dotx). Use docx_index first to identify which pages are relevant, then call this tool to read their content. Word documents do not have inherent page boundaries, so pages are grouped by explicit page breaks or word count limits, with a section heading starting a new pseudo-page once the current page is already full. Footnotes are numbered in document order and their references appear inline as [^N], with the footnote body appended as a [^N]: line at the end of the page that references it; endnotes render the same way as [^eN] and [^eN]:. Headers and footers are not extracted; text inside text boxes is. Pass end_page to read several consecutive pseudo-pages in one call."
}

func (ReadDocxPageTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["path", "page"],
		"properties": {
			"path": {
				"type": "string",
				"description": "Path to the Word document (.docx, .docm or .dotx), absolute or relative to session directory"
			},
			"page": {
				"type": "integer",
				"description": "1-based pseudo-page number to read"
			},
			"end_page": {
				"type": "integer",
				"description": "Last pseudo-page to read (1-based, inclusive); omit to read only the single page, or pass it with page to read the range page..end_page"
			}
		}
	}`)
}

func (ReadDocxPageTool) Execute(_ context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var params struct {
		Path    string `json:"path"`
		Page    int    `json:"page"`
		EndPage *int   `json:"end_page"`
	}
	if err := DecodeArgs(args, &params); err != nil {
		return Result{}, fmt.Errorf("parse read_docx_page args: %w", err)
	}
	if params.Path == "" {
		return Result{Title: "Read DOCX Page", Output: "path is required"}, nil
	}
	if params.Page < 1 {
		return Result{Title: "Read DOCX Page", Output: "page must be >= 1"}, nil
	}
	if params.EndPage != nil && *params.EndPage < params.Page {
		return Result{
			Title:  "Read DOCX Page",
			Output: fmt.Sprintf("end_page must be >= page (got %d–%d)", params.Page, *params.EndPage),
		}, nil
	}

	path := params.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(tctx.SessionDir, path)
	}

	// A .doc/.dot/.dotm is a Word file this reader cannot open — an OLE compound
	// file (.doc, .dot), or the macro-enabled template docindex deliberately
	// leaves out — so answer that instead of the extractor's "zip: not a valid
	// zip file". Only these Word formats are gated: any other non-ZIP file must
	// still surface the ZIP error, which is the right structural test for it.
	if isLegacyWordFormat(path) {
		return Result{
			Title:  fmt.Sprintf("DOCX / %s", filepath.Base(path)),
			Output: fmt.Sprintf("%s is a legacy/unsupported Word format — convert it to .docx (or .docm/.dotx) and read that", filepath.Base(path)),
		}, nil
	}

	pages, err := docxPagesFor(path)
	if err != nil {
		// docx.ExtractPages already names the path, so wrapping it here printed
		// the path a second time.
		return Result{}, err
	}

	if len(pages) == 0 {
		return Result{
			Title:  fmt.Sprintf("DOCX / %s", filepath.Base(path)),
			Output: "the document has no extractable text",
		}, nil
	}

	if params.Page > len(pages) {
		return Result{
			Title:  fmt.Sprintf("DOCX Page %d of %d / %s", params.Page, len(pages), filepath.Base(path)),
			Output: fmt.Sprintf("page %d out of range (document has %d pages) — call docx_index to list the pages", params.Page, len(pages)),
		}, nil
	}

	// An end_page past the document is clamped: the caller asked to read to the
	// end, which is what the document's last page is.
	end := params.Page
	if params.EndPage != nil {
		end = *params.EndPage
		if end > len(pages) {
			end = len(pages)
		}
	}

	var text string
	truncated := false
	last := params.Page
	if params.EndPage == nil {
		text = pageText(pages[params.Page-1])
	} else {
		text, last, truncated = readPagesRange(pages, params.Page, end)
	}

	title := fmt.Sprintf("DOCX Page %d / %s", params.Page, filepath.Base(path))
	if params.EndPage != nil {
		title = fmt.Sprintf("DOCX Pages %d–%d of %d / %s", params.Page, last, len(pages), filepath.Base(path))
	}
	return Result{Title: title, Output: text, Truncated: truncated}, nil
}

// readPagesRange renders pseudo-pages first..end, capping the result at
// MaxToolOutputBytes and cutting on a page boundary. The loop's own truncation
// backstop would also bound the result, but it slices mid-page and its notice
// names no resume point; cutting here lets the notice say which page to ask
// for next. It returns the text, the last page it included, and whether
// anything was left out.
func readPagesRange(pages []docx.PageText, first, end int) (string, int, bool) {
	var b strings.Builder
	last := first - 1
	for i := first; i <= end; i++ {
		chunk := pageText(pages[i-1])
		if i > first {
			chunk = fmt.Sprintf("\n\n--- page %d ---\n\n", i) + chunk
		}
		if b.Len() > 0 && b.Len()+len(chunk) > MaxToolOutputBytes {
			break
		}
		if b.Len() == 0 && len(chunk) > MaxToolOutputBytes {
			// The page the caller asked for does not fit on its own. Cut it
			// rather than return it whole: the caller still gets an answer,
			// and the cut is stated instead of left to the loop's backstop.
			b.WriteString(cutRuneSafe(chunk, MaxToolOutputBytes))
			last = i
			fmt.Fprintf(&b, "\n\n[page %d truncated at %d KB]", i, MaxToolOutputBytes>>10)
			break
		}
		b.WriteString(chunk)
		last = i
	}

	text := b.String()
	if last < end {
		text += fmt.Sprintf("\n\n[output truncated at %d KB: pages %d–%d not shown. Call read_docx_page with page=%d to continue.]",
			MaxToolOutputBytes>>10, last+1, end, last+1)
		return text, last, true
	}
	return text, last, false
}

// legacyWordExtensions are the Word formats this reader cannot open and answers
// with a conversion hint instead of a ZIP error. They are exactly the Word-ish
// extensions docindex.DocxExtensions deliberately leaves out: .doc and .dot are
// OLE compound files rather than OOXML ZIPs, and .dotm is out of scope there.
var legacyWordExtensions = []string{".doc", ".dot", ".dotm"}

// isLegacyWordFormat reports whether path names one of those formats.
func isLegacyWordFormat(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, want := range legacyWordExtensions {
		if ext == want {
			return true
		}
	}
	return false
}

// pageText is one page's text with the fallback the tool has always used for a
// page the extractor found nothing on.
func pageText(p docx.PageText) string {
	if p.Text == "" {
		return "(no extractable text on this page)"
	}
	return p.Text
}
