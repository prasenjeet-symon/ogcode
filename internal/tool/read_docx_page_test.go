package tool

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/docindex"
	"github.com/prasenjeet-symon/ogcode/internal/docx"
)

// docxTestStamp and docxTestStamp2 are the fixed content stamps the cache tests
// below use in place of a real file's stamp.
var (
	docxTestStamp  = docxPageStamp{1}
	docxTestStamp2 = docxPageStamp{2}
)

// writeDocxFile writes a minimal Word document holding one paragraph of text.
// The entry is stored uncompressed so that rewriting the same file with different
// text of the same length leaves its size untouched — the cache-identity test
// depends on the size staying put while only the content changes.
func writeDocxFile(t *testing.T, path, text string) {
	t.Helper()
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body><w:p><w:r><w:t>` + text + `</w:t></w:r></w:p></w:body>
</w:document>`
	writeDocxArchive(t, path, docXML)
}

// writePagedDocxFile writes a Word document whose paragraphs are separated by
// explicit page breaks (<w:br w:type="page"/>), so the extractor sees exactly
// one pseudo-page per text — the cheapest way to force a known page count.
func writePagedDocxFile(t *testing.T, path string, texts ...string) {
	t.Helper()
	var body strings.Builder
	for i, text := range texts {
		body.WriteString("<w:p><w:r><w:t>" + text + "</w:t></w:r>")
		if i < len(texts)-1 {
			body.WriteString(`<w:r><w:br w:type="page"/></w:r>`)
		}
		body.WriteString("</w:p>")
	}
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>` + body.String() + `</w:body>
</w:document>`
	writeDocxArchive(t, path, docXML)
}

// writeDocxArchive stores docXML as the only part of a minimal .docx.
func writeDocxArchive(t *testing.T, path, docXML string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "word/document.xml", Method: zip.Store})
	if err != nil {
		t.Fatalf("create document.xml: %v", err)
	}
	if _, err := w.Write([]byte(docXML)); err != nil {
		t.Fatalf("write document.xml: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

// runReadDocxPage drives the tool the way a model call does.
func runReadDocxPage(t *testing.T, dir, path string, page int) Result {
	t.Helper()
	return runReadDocxPageArgs(t, dir, map[string]any{"path": path, "page": page})
}

// runReadDocxPageArgs drives the tool with an arbitrary argument object.
func runReadDocxPageArgs(t *testing.T, dir string, args map[string]any) Result {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	res, err := ReadDocxPageTool{}.Execute(context.Background(), raw, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("read_docx_page %v: %v", args, err)
	}
	return res
}

// An unchanged file must be served from the parse the first read cached. The
// proof is the backing array: a cache hit hands back the very slice the first
// parse produced.
func TestReadDocxPage_CacheServesTheUnchangedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.docx")
	writeDocxFile(t, path, "first cached content")

	first, err := docxPagesFor(path)
	if err != nil {
		t.Fatalf("first parse: %v", err)
	}
	if len(first) != 1 || !strings.Contains(first[0].Text, "first cached content") {
		t.Fatalf("first parse returned %+v", first)
	}

	second, err := docxPagesFor(path)
	if err != nil {
		t.Fatalf("cached read: %v", err)
	}
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("page counts differ: first %d, second %d", len(first), len(second))
	}
	if &first[0] != &second[0] {
		t.Error("second read re-parsed the file instead of serving the cache")
	}
}

// A rewrite that keeps both the size and the modification time — what cp -p,
// rsync -a or a restore leaves behind — is still a different document, and the
// content stamp must see it.
func TestReadDocxPage_SameSizeSameMtimeRewriteIsReParsed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.docx")
	writeDocxFile(t, path, "first cached content")

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if _, err := docxPagesFor(path); err != nil {
		t.Fatalf("prime cache: %v", err)
	}

	writeDocxFile(t, path, "other cached content")
	rewritten, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat rewritten: %v", err)
	}
	if rewritten.Size() != info.Size() {
		t.Fatalf("fixture sizes differ (%d vs %d) — the cache check needs a same-size rewrite",
			rewritten.Size(), info.Size())
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatalf("restore mtime: %v", err)
	}

	pages, err := docxPagesFor(path)
	if err != nil {
		t.Fatalf("read after rewrite: %v", err)
	}
	if len(pages) != 1 || !strings.Contains(pages[0].Text, "other cached content") {
		t.Errorf("a same-size, same-mtime rewrite was served from the old parse: %+v", pages)
	}
}

// The cache is bounded, and the entry dropped is the one used longest ago rather
// than whichever the map hands over first.
func TestDocxPageCache_EvictsLeastRecentlyUsedEntry(t *testing.T) {
	cache := newDocxPageCache()
	var tick int64
	cache.clock = func() time.Time {
		tick++
		return time.Unix(tick, 0)
	}
	put := func(i int) string {
		path := fmt.Sprintf("/books/%d.docx", i)
		cache.put(path, []docx.PageText{{PageNum: 1, Text: "x"}}, 100, 100, docxTestStamp)
		return path
	}
	var paths []string
	for i := 0; i < docxPageCacheCap; i++ {
		paths = append(paths, put(i))
	}

	// Refresh the first document, then add one past the cap: the victim must be
	// the least-recently-used entry (the second inserted), not the refreshed one.
	if _, ok := cache.get(paths[0], 100, 100, docxTestStamp); !ok {
		t.Fatalf("expected a hit for a freshly cached document")
	}
	put(docxPageCacheCap)

	if _, ok := cache.get(paths[1], 100, 100, docxTestStamp); ok {
		t.Errorf("the least-recently-used entry survived eviction; cache holds %d entries", len(cache.entries))
	}
	if _, ok := cache.get(paths[0], 100, 100, docxTestStamp); !ok {
		t.Error("the refreshed entry was evicted instead of the stale one")
	}
	if len(cache.entries) != docxPageCacheCap {
		t.Errorf("cache holds %d entries, want cap %d", len(cache.entries), docxPageCacheCap)
	}
}

// The entry cap is not a memory bound on its own: the byte budget evicts the
// least-recently-used entries until the cached text fits.
func TestDocxPageCache_EvictsUntilTheTextFitsTheByteBudget(t *testing.T) {
	cache := newDocxPageCache()
	var tick int64
	cache.clock = func() time.Time {
		tick++
		return time.Unix(tick, 0)
	}
	cache.maxBytes = 10
	put := func(i int, text string) string {
		path := fmt.Sprintf("/books/%d.docx", i)
		cache.put(path, []docx.PageText{{PageNum: 1, Text: text}}, 100, 100, docxTestStamp)
		return path
	}

	first := put(1, "aaaa") // 4 bytes
	put(2, "bbbb")          // 8 bytes, inside the budget
	if len(cache.entries) != 2 {
		t.Fatalf("cache holds %d entries, want 2 while under the budget", len(cache.entries))
	}

	put(3, "cccc") // 12 bytes: the oldest entry has to go
	if _, ok := cache.get(first, 100, 100, docxTestStamp); ok {
		t.Error("the least-recently-used entry survived the byte budget")
	}
	if len(cache.entries) != 2 {
		t.Errorf("cache holds %d entries, want 2 after one eviction", len(cache.entries))
	}
	if cache.total != 8 {
		t.Errorf("cached text = %d bytes, want 8", cache.total)
	}
	if cache.total > cache.maxBytes {
		t.Errorf("cached text = %d bytes, over the %d-byte budget", cache.total, cache.maxBytes)
	}

	// A read refreshes an entry, so the next over-budget put evicts the one not
	// touched instead of the one just used.
	third := fmt.Sprintf("/books/%d.docx", 3)
	if _, ok := cache.get(third, 100, 100, docxTestStamp); !ok {
		t.Fatal("expected the newest entry to still be cached")
	}
	put(4, "dddd") // 12 bytes: over the budget again, so the stale entry goes
	if _, ok := cache.get(third, 100, 100, docxTestStamp); !ok {
		t.Error("the refreshed entry was evicted instead of the stale one")
	}
	if _, ok := cache.get("/books/2.docx", 100, 100, docxTestStamp); ok {
		t.Error("the stale entry survived the second eviction")
	}
	if cache.total != 8 {
		t.Errorf("cached text = %d bytes, want 8", cache.total)
	}
	if cache.total > cache.maxBytes {
		t.Errorf("cached text = %d bytes, over the %d-byte budget", cache.total, cache.maxBytes)
	}
}

// One document larger than the whole budget is still served: evicting it would
// re-parse it on every page read, and there would be nothing to gain.
func TestDocxPageCache_KeepsOneOversizedEntry(t *testing.T) {
	cache := newDocxPageCache()
	cache.maxBytes = 4
	pages := []docx.PageText{{PageNum: 1, Text: strings.Repeat("x", 64)}, {PageNum: 2, Text: strings.Repeat("y", 64)}}
	cache.put("/books/big.docx", pages, 100, 100, docxTestStamp)

	if len(cache.entries) != 1 {
		t.Fatalf("cache holds %d entries, want the single oversized one", len(cache.entries))
	}
	if want := int64(128); cache.total != want {
		t.Errorf("cached text = %d bytes, want %d", cache.total, want)
	}
	cached, ok := cache.get("/books/big.docx", 100, 100, docxTestStamp)
	if !ok || len(cached) != len(pages) {
		t.Errorf("oversized entry is not served: ok=%v pages=%d", ok, len(cached))
	}
}

// The cache must not change what the tool answers: a page past the end still says
// how long the document is, and a valid page still returns its text.
func TestReadDocxPage_PageOutOfRangeMessage(t *testing.T) {
	dir := t.TempDir()
	writeDocxFile(t, filepath.Join(dir, "report.docx"), "only page")

	args, _ := json.Marshal(map[string]any{"path": "report.docx", "page": 4})
	res, err := ReadDocxPageTool{}.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("out-of-range execute: %v", err)
	}
	if want := "page 4 out of range (document has 1 pages) — call docx_index to list the pages"; res.Output != want {
		t.Errorf("output = %q, want %q", res.Output, want)
	}
	if want := "DOCX Page 4 of 1 / report.docx"; res.Title != want {
		t.Errorf("title = %q, want %q", res.Title, want)
	}

	args, _ = json.Marshal(map[string]any{"path": "report.docx", "page": 1})
	res, err = ReadDocxPageTool{}.Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Output != "only page" {
		t.Errorf("page 1 text = %q", res.Output)
	}
	if want := "DOCX Page 1 / report.docx"; res.Title != want {
		t.Errorf("title = %q, want %q", res.Title, want)
	}
}

// The description is where a model learns which files this tool reads and how
// pages are formed, so both the new Word formats and heading-aware grouping have
// to be in it.
func TestReadDocxPage_DescriptionNamesWordFormatsAndHeadings(t *testing.T) {
	desc := ReadDocxPageTool{}.Description()
	for _, want := range []string{"Word document (.docx, .docm or .dotx)", "section heading", "Headers and footers are not extracted"} {
		if !strings.Contains(desc, want) {
			t.Errorf("description does not mention %q: %s", want, desc)
		}
	}

	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(ReadDocxPageTool{}.Parameters(), &schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if !strings.Contains(schema.Properties["path"].Description, ".docm or .dotx") {
		t.Errorf("path description does not name the Word formats: %q",
			schema.Properties["path"].Description)
	}
	if !strings.Contains(schema.Properties["end_page"].Description, "range") {
		t.Errorf("end_page is not described as a range: %q",
			schema.Properties["end_page"].Description)
	}
}

func TestReadDocxPage_RequiresPathAndAPositivePage(t *testing.T) {
	dir := t.TempDir()
	if res := runReadDocxPageArgs(t, dir, map[string]any{"page": 1}); res.Output != "path is required" {
		t.Errorf("missing path output = %q, want %q", res.Output, "path is required")
	}
	if res := runReadDocxPageArgs(t, dir, map[string]any{"path": "report.docx", "page": 0}); res.Output != "page must be >= 1" {
		t.Errorf("page 0 output = %q, want %q", res.Output, "page must be >= 1")
	}
}

// The extractor's error already names the file, so the tool must not wrap it a
// second time — nor may a failing read be reported as an empty document.
func TestReadDocxPage_MissingFileErrorNamesThePathOnce(t *testing.T) {
	dir := t.TempDir()
	raw, err := json.Marshal(map[string]any{"path": "gone.docx", "page": 1})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	_, err = ReadDocxPageTool{}.Execute(context.Background(), raw, Context{SessionDir: dir})
	if err == nil {
		t.Fatal("a missing file must be reported as an error")
	}
	if strings.Contains(err.Error(), "extract docx") {
		t.Errorf("error is wrapped with the path a second time: %v", err)
	}
	if !strings.Contains(err.Error(), "gone.docx") {
		t.Errorf("error does not name the file: %v", err)
	}
}

// A non-ZIP file that is not a Word format must still surface the structural
// ZIP failure rather than a Word-specific hint.
func TestReadDocxPage_NonZipFileSurfacesTheZipError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("plain text"), 0o644); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}
	raw, err := json.Marshal(map[string]any{"path": "notes.txt", "page": 1})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	_, err = ReadDocxPageTool{}.Execute(context.Background(), raw, Context{SessionDir: dir})
	if err == nil {
		t.Fatal("a non-ZIP file must be reported as an error")
	}
	if !strings.Contains(err.Error(), "zip") {
		t.Errorf("error does not report the ZIP failure: %v", err)
	}
	if strings.Contains(err.Error(), "legacy/unsupported") {
		t.Errorf("a .txt was answered with the Word-format hint: %v", err)
	}
}

// A directory named like a document is not a legacy Word file: it fails inside
// the extractor and the tool reports that error.
func TestReadDocxPage_DirectoryIsNotALegacyWordFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "folder.docx"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw, err := json.Marshal(map[string]any{"path": "folder.docx", "page": 1})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	_, err = ReadDocxPageTool{}.Execute(context.Background(), raw, Context{SessionDir: dir})
	if err == nil {
		t.Fatal("a directory must be reported as an error")
	}
	if strings.Contains(err.Error(), "legacy/unsupported") {
		t.Errorf("a directory was answered with the Word-format hint: %v", err)
	}
}

// Every Word format this reader cannot open gets the conversion hint instead of
// a raw ZIP error, and the set stays disjoint from the formats docindex indexes.
func TestReadDocxPage_LegacyWordFormatsGetAConversionHint(t *testing.T) {
	for _, ext := range legacyWordExtensions {
		for _, known := range docindex.DocxExtensions {
			if ext == known {
				t.Fatalf("%s is both legacy and a supported format", ext)
			}
		}
	}

	dir := t.TempDir()
	for _, ext := range legacyWordExtensions {
		name := "old" + ext
		if err := os.WriteFile(filepath.Join(dir, name), []byte("not a zip"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		res := runReadDocxPageArgs(t, dir, map[string]any{"path": name, "page": 1})
		want := name + " is a legacy/unsupported Word format — convert it to .docx (or .docm/.dotx) and read that"
		if res.Output != want {
			t.Errorf("%s output = %q, want %q", ext, res.Output, want)
		}
		if wantTitle := "DOCX / " + name; res.Title != wantTitle {
			t.Errorf("%s title = %q, want %q", ext, res.Title, wantTitle)
		}
	}
}

// A document with no text has no pages, and the reader says exactly that.
func TestReadDocxPage_EmptyDocument(t *testing.T) {
	dir := t.TempDir()
	writePagedDocxFile(t, filepath.Join(dir, "report.docx"))

	res := runReadDocxPageArgs(t, dir, map[string]any{"path": "report.docx", "page": 1})
	if want := "DOCX / report.docx"; res.Title != want {
		t.Errorf("title = %q, want %q", res.Title, want)
	}
	if want := "the document has no extractable text"; res.Output != want {
		t.Errorf("output = %q, want %q", res.Output, want)
	}
}

// end_page reads consecutive pseudo-pages in one call, clamped to the document.
func TestReadDocxPage_EndPageReadsARange(t *testing.T) {
	dir := t.TempDir()
	writePagedDocxFile(t, filepath.Join(dir, "report.docx"), "one", "two", "three", "four")

	cases := []struct {
		name       string
		page       int
		endPage    int
		wantOutput string
		wantTitle  string
	}{
		{"middle", 2, 3, "two\n\n--- page 3 ---\n\nthree", "DOCX Pages 2–3 of 4 / report.docx"},
		{"clamped", 3, 9, "three\n\n--- page 4 ---\n\nfour", "DOCX Pages 3–4 of 4 / report.docx"},
		{"single", 2, 2, "two", "DOCX Pages 2–2 of 4 / report.docx"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := runReadDocxPageArgs(t, dir, map[string]any{
				"path": "report.docx", "page": tc.page, "end_page": tc.endPage,
			})
			if res.Output != tc.wantOutput {
				t.Errorf("output = %q, want %q", res.Output, tc.wantOutput)
			}
			if res.Title != tc.wantTitle {
				t.Errorf("title = %q, want %q", res.Title, tc.wantTitle)
			}
		})
	}
}

func TestReadDocxPage_ReversedEndPageIsRefused(t *testing.T) {
	dir := t.TempDir()
	writePagedDocxFile(t, filepath.Join(dir, "report.docx"), "one", "two", "three")

	res := runReadDocxPageArgs(t, dir, map[string]any{"path": "report.docx", "page": 3, "end_page": 2})
	if want := "end_page must be >= page (got 3–2)"; res.Output != want {
		t.Errorf("output = %q, want %q", res.Output, want)
	}
}

// An end_page range that would exceed the tool's byte cap is cut on a page
// boundary, and the notice names the page to resume from — the loop's own
// truncation backstop would slice mid-page and say nothing about resuming.
func TestReadDocxPage_EndPageRangeIsBounded(t *testing.T) {
	dir := t.TempDir()
	page := strings.Repeat("x", 20<<10)
	writePagedDocxFile(t, filepath.Join(dir, "report.docx"), page, page, page, page, page)

	res := runReadDocxPageArgs(t, dir, map[string]any{"path": "report.docx", "page": 1, "end_page": 5})
	if !res.Truncated {
		t.Error("a capped range read must report itself as truncated")
	}
	if want := "DOCX Pages 1–2 of 5 / report.docx"; res.Title != want {
		t.Errorf("title = %q, want %q", res.Title, want)
	}
	if strings.Contains(res.Output, "--- page 3 ---") {
		t.Errorf("output should stop at the page boundary it fits: %q", res.Output[:200])
	}
	if !strings.Contains(res.Output, "page=3 to continue") {
		t.Errorf("the notice should name the page to resume from, got: %q", res.Output)
	}
	if len(res.Output) > MaxToolOutputBytes+200 {
		t.Errorf("output = %d bytes, over the %d-byte cap", len(res.Output), MaxToolOutputBytes)
	}
}

// A single page larger than the whole cap is still answered, cut and marked.
func TestReadDocxPage_SingleOversizedPageIsCut(t *testing.T) {
	dir := t.TempDir()
	writePagedDocxFile(t, filepath.Join(dir, "big.docx"), strings.Repeat("y", MaxToolOutputBytes+4096), "second")

	res := runReadDocxPageArgs(t, dir, map[string]any{"path": "big.docx", "page": 1, "end_page": 2})
	if !res.Truncated {
		t.Error("an oversized page read must report itself as truncated")
	}
	if !strings.Contains(res.Output, "page 1 truncated at 50 KB") {
		t.Errorf("the cut should be stated, got: %q", res.Output)
	}
	if !strings.Contains(res.Output, "page=2 to continue") {
		t.Errorf("the notice should name the page to resume from, got: %q", res.Output)
	}
}
