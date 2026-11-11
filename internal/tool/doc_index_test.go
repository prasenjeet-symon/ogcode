package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/docindex"
)

// docIndexRunner is the part of the tool interface these tests drive. Both index
// tools implement it, so every behaviour is asserted for both at once — they
// share one implementation now, and a test that covered only docx_index would not
// notice the pair drifting apart again.
type docIndexRunner interface {
	ID() string
	Description() string
	Parameters() json.RawMessage
	Execute(context.Context, json.RawMessage, Context) (Result, error)
}

// docIndexTools returns each tool with the store its fixture will supply. The
// store is swapped by withStore, keyed by ID, because the two constructors take
// the same argument but return distinct types.
func docIndexTools() []docIndexRunner {
	return []docIndexRunner{NewDocxIndexTool(nil), NewPdfIndexTool(nil)}
}

func withStore(t *testing.T, tool docIndexRunner, store *docindex.Store) docIndexRunner {
	t.Helper()
	switch tool.ID() {
	case "docx_index":
		return NewDocxIndexTool(store)
	case "pdf_index":
		return NewPdfIndexTool(store)
	}
	t.Fatalf("unknown index tool %q", tool.ID())
	return nil
}

func docIndexTitle(tool docIndexRunner) string {
	if tool.ID() == "docx_index" {
		return "DOCX Index"
	}
	return "PDF Index"
}

// docIndexFixture builds a real store holding pages 1..pages of one document and
// returns the session directory the tools resolve a relative path against.
func docIndexFixture(t *testing.T, pages int) (*docindex.Store, string) {
	t.Helper()
	nums := make([]int, 0, pages)
	for i := 1; i <= pages; i++ {
		nums = append(nums, i)
	}
	return docIndexFixturePages(t, nums...)
}

// docIndexStore opens a store over a fresh temp directory and returns it with
// that directory, which is the session directory a relative path resolves
// against.
func docIndexStore(t *testing.T) (*docindex.Store, string) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return docindex.NewStore(database), dir
}

// upsertDocPages stores the given page numbers for one document under one
// recorded modification time, exactly as submit_doc_index records it.
func upsertDocPages(t *testing.T, store *docindex.Store, path string, modTime int64, pageNums ...int) {
	t.Helper()
	for _, n := range pageNums {
		if err := store.Upsert(&docindex.PageEntry{
			DocPath: path,
			PageNum: n,
			Labels:  []string{fmt.Sprintf("topic %d", n)},
			ModTime: modTime,
		}); err != nil {
			t.Fatalf("upsert page %d: %v", n, err)
		}
	}
}

// docIndexFixturePages stores exactly the given page numbers for one document.
// It is how a kind that indexes only the pages it managed to label is simulated:
// the entry count and the document's last page number then differ.
//
// The index tools stat the document and compare its modification time with the
// one recorded on the rows, so the fixture has to be a real file whose mtime the
// rows carry — a row without one reads as stale.
func docIndexFixturePages(t *testing.T, pageNums ...int) (*docindex.Store, string) {
	t.Helper()
	store, dir := docIndexStore(t)
	path := filepath.Join(dir, "report.docx")
	if err := os.WriteFile(path, []byte("indexed document"), 0o644); err != nil {
		t.Fatalf("write fixture document: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat fixture document: %v", err)
	}
	upsertDocPages(t, store, path, info.ModTime().UnixMilli(), pageNums...)
	return store, dir
}

func runDocIndex(t *testing.T, tool docIndexRunner, dir string, args map[string]any) Result {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	res, err := tool.Execute(context.Background(), raw, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("%s execute: %v", tool.ID(), err)
	}
	return res
}

// listedPages parses the listing's JSON and returns the page numbers it carries,
// so a range assertion is about the pages listed rather than about substrings.
func listedPages(t *testing.T, out string) []int {
	t.Helper()
	var pages []struct {
		Page   int      `json:"page"`
		Labels []string `json:"labels"`
	}
	if err := json.Unmarshal([]byte(out), &pages); err != nil {
		t.Fatalf("listing is not a JSON array of page entries: %v\n%s", err, out)
	}
	nums := make([]int, len(pages))
	for i, p := range pages {
		nums[i] = p.Page
	}
	return nums
}

func wantPages(t *testing.T, got []int, want ...int) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("listed pages = %v, want %v", got, want)
	}
}

// No bounds is the pre-existing call, and its output must not have moved: every
// page, in order, under the "(N pages)" title.
func TestDocIndex_NoRangeListsEveryPage(t *testing.T) {
	store, dir := docIndexFixture(t, 5)
	for _, tool := range docIndexTools() {
		res := runDocIndex(t, withStore(t, tool, store), dir, map[string]any{"path": "report.docx"})
		wantPages(t, listedPages(t, res.Output), 1, 2, 3, 4, 5)
		want := fmt.Sprintf("%s / report.docx (5 pages)", docIndexTitle(tool))
		if res.Title != want {
			t.Errorf("%s title = %q, want %q", tool.ID(), res.Title, want)
		}
		if !strings.Contains(res.Output, `"labels": [`) {
			t.Errorf("%s listing lost the labels field: %s", tool.ID(), res.Output)
		}
	}
}

// A bounded call lists only the pages in the bounds, and its title names them so
// the reader can tell a narrowed listing from a short document.
func TestDocIndex_RangeNarrowsTheListing(t *testing.T) {
	store, dir := docIndexFixture(t, 8)
	for _, tool := range docIndexTools() {
		res := runDocIndex(t, withStore(t, tool, store), dir, map[string]any{
			"path": "report.docx", "start_page": 3, "end_page": 5,
		})
		wantPages(t, listedPages(t, res.Output), 3, 4, 5)
		want := fmt.Sprintf("%s / report.docx (pages 3–5 of 8)", docIndexTitle(tool))
		if res.Title != want {
			t.Errorf("%s title = %q, want %q", tool.ID(), res.Title, want)
		}
	}
}

// A lone end_page means "from the first page to end_page".
func TestDocIndex_EndPageAloneStartsAtOne(t *testing.T) {
	store, dir := docIndexFixture(t, 8)
	for _, tool := range docIndexTools() {
		res := runDocIndex(t, withStore(t, tool, store), dir, map[string]any{
			"path": "report.docx", "end_page": 4,
		})
		wantPages(t, listedPages(t, res.Output), 1, 2, 3, 4)
		if !strings.Contains(res.Title, "(pages 1–4 of 8)") {
			t.Errorf("%s title = %q, want it to carry the applied range", tool.ID(), res.Title)
		}
	}
}

// An end past the document is clamped, not refused: the caller asked for "to the
// end", which is what the document's last page is.
func TestDocIndex_EndPagePastTheDocumentIsClamped(t *testing.T) {
	store, dir := docIndexFixture(t, 8)
	for _, tool := range docIndexTools() {
		res := runDocIndex(t, withStore(t, tool, store), dir, map[string]any{
			"path": "report.docx", "start_page": 6, "end_page": 50,
		})
		wantPages(t, listedPages(t, res.Output), 6, 7, 8)
		if !strings.Contains(res.Title, "(pages 6–8 of 8)") {
			t.Errorf("%s title = %q, want the clamped range", tool.ID(), res.Title)
		}
	}
}

// A range that lands past the end still answers usefully: the bounds that were
// asked for and the document's real page count.
func TestDocIndex_RangePastTheEndReportsThePageCount(t *testing.T) {
	store, dir := docIndexFixture(t, 12)
	for _, tool := range docIndexTools() {
		res := runDocIndex(t, withStore(t, tool, store), dir, map[string]any{
			"path": "report.docx", "start_page": 40, "end_page": 50,
		})
		want := "no pages in range 40–50 (document has 12 pages)"
		if res.Output != want {
			t.Errorf("%s output = %q, want %q", tool.ID(), res.Output, want)
		}
	}
}

// A kind that stores only some pages must not be described as if the entry count
// were the document's last page number: with only pages 3 and 5 stored, the last
// page is 5 even though the listing holds two entries.
func TestDocIndex_NonContiguousPagesReportTheLastPageNumber(t *testing.T) {
	store, dir := docIndexFixturePages(t, 3, 5)
	for _, tool := range docIndexTools() {
		res := runDocIndex(t, withStore(t, tool, store), dir, map[string]any{"path": "report.docx"})
		wantPages(t, listedPages(t, res.Output), 3, 5)
		want := fmt.Sprintf("%s / report.docx (pages 3–5 of 5)", docIndexTitle(tool))
		if res.Title != want {
			t.Errorf("%s title = %q, want %q", tool.ID(), res.Title, want)
		}

		res = runDocIndex(t, withStore(t, tool, store), dir, map[string]any{
			"path": "report.docx", "start_page": 3, "end_page": 3,
		})
		wantPages(t, listedPages(t, res.Output), 3)

		// A start past the last page reports the asked-for range and the document's
		// last page, never a backwards pair.
		for _, tc := range []struct {
			args map[string]any
			want string
		}{
			{map[string]any{"path": "report.docx", "start_page": 6, "end_page": 9}, "no pages in range 6–9 (document has 5 pages)"},
			{map[string]any{"path": "report.docx", "start_page": 6}, "no pages in range 6–6 (document has 5 pages)"},
		} {
			res := runDocIndex(t, withStore(t, tool, store), dir, tc.args)
			if res.Output != tc.want {
				t.Errorf("%s %v output = %q, want %q", tool.ID(), tc.args, res.Output, tc.want)
			}
		}
	}
}

// A listing that starts at page 1 but skips pages is still not the whole
// document: pages 1 and 5 of 5 must not be titled "(2 pages)", which would read
// as a two-page document and make page 5 look out of range.
func TestDocIndex_SparsePagesStartingAtOneNameTheRange(t *testing.T) {
	store, dir := docIndexFixturePages(t, 1, 5)
	for _, tool := range docIndexTools() {
		res := runDocIndex(t, withStore(t, tool, store), dir, map[string]any{"path": "report.docx"})
		wantPages(t, listedPages(t, res.Output), 1, 5)
		want := fmt.Sprintf("%s / report.docx (pages 1–5 of 5)", docIndexTitle(tool))
		if res.Title != want {
			t.Errorf("%s title = %q, want %q", tool.ID(), res.Title, want)
		}
	}
}

// The page count docx_index advertises is a promise read_docx_page has to keep:
// if the index says a document has N pages, page N is readable and only N+1 is
// out of range. An agent that finds a page in the index must always be able to
// read it.
func TestDocxIndexPageCountMatchesReadDocxPage(t *testing.T) {
	texts := []string{"First page body.", "Second page body.", "Third page body."}
	store, dir := docIndexFixturePages(t)
	path := filepath.Join(dir, "report.docx")
	writePagedDocxFile(t, path, texts...)

	pages, err := docxPagesFor(path)
	if err != nil {
		t.Fatalf("extract fixture: %v", err)
	}
	if len(pages) != len(texts) {
		t.Fatalf("fixture extracted %d pages, want %d: %+v", len(pages), len(texts), pages)
	}
	nums := make([]int, len(pages))
	for i, p := range pages {
		if !strings.Contains(p.Text, texts[i]) {
			t.Fatalf("page %d text = %q, want it to hold %q", p.PageNum, p.Text, texts[i])
		}
		nums[i] = p.PageNum
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat fixture: %v", err)
	}
	upsertDocPages(t, store, path, info.ModTime().UnixMilli(), nums...)

	index := runDocIndex(t, NewDocxIndexTool(store), dir, map[string]any{"path": "report.docx"})
	wantTitle := fmt.Sprintf("DOCX Index / report.docx (%d pages)", len(pages))
	if index.Title != wantTitle {
		t.Errorf("docx_index title = %q, want %q", index.Title, wantTitle)
	}
	listed := make([]int, len(pages))
	for i := range pages {
		listed[i] = i + 1
	}
	wantPages(t, listedPages(t, index.Output), listed...)

	for i, text := range texts {
		res := runReadDocxPage(t, dir, "report.docx", i+1)
		if res.Output != text {
			t.Errorf("read_docx_page page %d = %q, want %q", i+1, res.Output, text)
		}
	}

	last := len(pages)
	res := runReadDocxPage(t, dir, "report.docx", last+1)
	if want := fmt.Sprintf("page %d out of range (document has %d pages) — call docx_index to list the pages", last+1, last); res.Output != want {
		t.Errorf("read_docx_page page %d = %q, want %q", last+1, res.Output, want)
	}
}

// Out-of-range bounds are a caller mistake, so they come back as a message the
// model can act on, not as an error the loop turns into a failed tool call.
func TestDocIndex_BadPageBoundsAreRefusedPolitely(t *testing.T) {
	store, dir := docIndexFixture(t, 8)
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"path": "report.docx", "start_page": 0}, "start_page must be >= 1"},
		{map[string]any{"path": "report.docx", "start_page": -3}, "start_page must be >= 1"},
		{map[string]any{"path": "report.docx", "end_page": 0}, "end_page must be >= 1"},
		{map[string]any{"path": "report.docx", "start_page": 5, "end_page": 3}, "end_page must be >= start_page (got 5–3)"},
	} {
		for _, tool := range docIndexTools() {
			res := runDocIndex(t, withStore(t, tool, store), dir, tc.args)
			if res.Output != tc.want {
				t.Errorf("%s %v output = %q, want %q", tool.ID(), tc.args, res.Output, tc.want)
			}
		}
	}
}

// page bounds on a document that was never indexed must not mask the reason:
// the "run ogcode index first" message is what the model needs.
func TestDocIndex_UnindexedDocumentKeepsTheIndexHint(t *testing.T) {
	store, dir := docIndexFixture(t, 0)
	for _, tool := range docIndexTools() {
		res := runDocIndex(t, withStore(t, tool, store), dir, map[string]any{
			"path": "report.docx", "start_page": 2, "end_page": 3,
		})
		if !strings.Contains(res.Output, "no index found for report.docx") ||
			!strings.Contains(res.Output, "run `ogcode index` first") {
			t.Errorf("%s output = %q, want the index hint", tool.ID(), res.Output)
		}
	}
}

// A nil store is the environment without an index; it must answer, not panic.
func TestDocIndex_NilStoreRefusesWithoutPanicking(t *testing.T) {
	for _, tool := range docIndexTools() {
		res, err := tool.Execute(context.Background(), nil, Context{SessionDir: "/proj"})
		if err != nil {
			t.Fatalf("%s execute: %v", tool.ID(), err)
		}
		if !strings.Contains(res.Output, "the document index is not available in this environment") {
			t.Errorf("%s nil-store output = %q", tool.ID(), res.Output)
		}
	}
}

// The range is advertised, or the model never knows it may narrow the listing;
// and it stays optional, because both bounds absent is the old whole-catalogue
// call.
func TestDocIndex_SchemasAdvertiseTheOptionalPageRange(t *testing.T) {
	for _, tool := range docIndexTools() {
		var schema struct {
			Required   []string `json:"required"`
			Properties map[string]struct {
				Type string `json:"type"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(tool.Parameters(), &schema); err != nil {
			t.Fatalf("%s schema: %v", tool.ID(), err)
		}
		for _, name := range []string{"start_page", "end_page"} {
			prop, ok := schema.Properties[name]
			if !ok {
				t.Errorf("%s schema has no %s property", tool.ID(), name)
				continue
			}
			if prop.Type != "integer" {
				t.Errorf("%s %s type = %q, want integer", tool.ID(), name, prop.Type)
			}
			for _, req := range schema.Required {
				if req == name {
					t.Errorf("%s lists %s as required; a range must stay optional", tool.ID(), name)
				}
			}
		}
		if _, ok := schema.Properties["path"]; !ok {
			t.Errorf("%s schema lost its path property", tool.ID())
		}
	}
}

// Word documents now include .docm and .dotx, and the description is where a
// model learns which files a tool covers. The PDF wording must not pick up the
// Word extensions along the way.
func TestDocIndex_DescriptionsNameTheWordFormats(t *testing.T) {
	const word = "Word document (.docx, .docm or .dotx)"

	docx := NewDocxIndexTool(nil)
	if !strings.Contains(docx.Description(), word) {
		t.Errorf("docx_index description does not name the Word formats: %q", docx.Description())
	}
	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(docx.Parameters(), &schema); err != nil {
		t.Fatalf("docx_index schema: %v", err)
	}
	if !strings.Contains(schema.Properties["path"].Description, ".docm or .dotx") {
		t.Errorf("docx_index path description does not name the Word formats: %q",
			schema.Properties["path"].Description)
	}

	pdf := NewPdfIndexTool(nil)
	if !strings.Contains(pdf.Description(), "PDF file") {
		t.Errorf("pdf_index description lost its PDF wording: %q", pdf.Description())
	}
	if strings.Contains(pdf.Description(), ".docm") {
		t.Errorf("pdf_index description picked up the Word formats: %q", pdf.Description())
	}
}

// The IDs and the constructors are public facts other packages rely on.
func TestDocIndex_PublicIDsUnchanged(t *testing.T) {
	if got := NewDocxIndexTool(nil).ID(); got != "docx_index" {
		t.Errorf("docx tool id = %q", got)
	}
	if got := NewPdfIndexTool(nil).ID(); got != "pdf_index" {
		t.Errorf("pdf tool id = %q", got)
	}
}

// A fresh index — rows whose recorded modification time is the file's own —
// lists normally: the staleness check must not turn every listing into a stale
// answer.
func TestDocIndex_FreshIndexListsNormally(t *testing.T) {
	store, dir := docIndexFixture(t, 3)
	for _, tool := range docIndexTools() {
		res := runDocIndex(t, withStore(t, tool, store), dir, map[string]any{"path": "report.docx"})
		wantPages(t, listedPages(t, res.Output), 1, 2, 3)
		if strings.Contains(res.Title, "stale") {
			t.Errorf("%s title = %q, want a fresh listing", tool.ID(), res.Title)
		}
	}
}

// An edit after indexing is what makes the stored pseudo-page boundaries lie: the
// reader numbers the pages differently now, so the listing must say the index is
// stale rather than advertise pages the reader will refuse.
func TestDocIndex_StaleIndexIsRefusedAfterRewrite(t *testing.T) {
	store, dir := docIndexFixture(t, 5)
	path := filepath.Join(dir, "report.docx")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatalf("touch fixture: %v", err)
	}

	for _, tool := range docIndexTools() {
		res := runDocIndex(t, withStore(t, tool, store), dir, map[string]any{"path": "report.docx"})
		if want := fmt.Sprintf("%s / report.docx (stale index)", docIndexTitle(tool)); res.Title != want {
			t.Errorf("%s title = %q, want %q", tool.ID(), res.Title, want)
		}
		want := "the index for report.docx is stale — the file was modified after it was indexed; run `ogcode index` again"
		if res.Output != want {
			t.Errorf("%s output = %q, want %q", tool.ID(), res.Output, want)
		}
	}
}

// A row written before the column existed records no time at all, which cannot
// prove the index is fresh: it reads as stale, costing one re-index.
func TestDocIndex_ZeroModTimeReadsAsStale(t *testing.T) {
	store, dir := docIndexStore(t)
	path := filepath.Join(dir, "report.docx")
	if err := os.WriteFile(path, []byte("indexed document"), 0o644); err != nil {
		t.Fatalf("write fixture document: %v", err)
	}
	upsertDocPages(t, store, path, 0, 1, 2)

	for _, tool := range docIndexTools() {
		res := runDocIndex(t, withStore(t, tool, store), dir, map[string]any{"path": "report.docx"})
		if !strings.Contains(res.Title, "(stale index)") {
			t.Errorf("%s title = %q, want a stale-index title", tool.ID(), res.Title)
		}
	}
}

// A document that has gone missing cannot have its index trusted, and the answer
// has to be a result the model can act on rather than a failed tool call.
func TestDocIndex_MissingFileReportsCannotRead(t *testing.T) {
	store, dir := docIndexStore(t)
	path := filepath.Join(dir, "report.docx")
	upsertDocPages(t, store, path, 1_700_000_000_000, 1, 2)

	for _, tool := range docIndexTools() {
		res := runDocIndex(t, withStore(t, tool, store), dir, map[string]any{"path": "report.docx"})
		if res.Title != docIndexTitle(tool) {
			t.Errorf("%s title = %q, want %q", tool.ID(), res.Title, docIndexTitle(tool))
		}
		if !strings.Contains(res.Output, "cannot read report.docx:") ||
			!strings.Contains(res.Output, "the index may be stale; run `ogcode index` again") {
			t.Errorf("%s output = %q, want the cannot-read message", tool.ID(), res.Output)
		}
	}
}
