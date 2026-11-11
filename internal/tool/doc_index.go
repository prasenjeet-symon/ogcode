package tool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/prasenjeet-symon/ogcode/internal/docindex"
)

// docIndexKind is everything docx_index and pdf_index do not share — the words.
// The two tools list the same store the same way, and keeping one implementation
// of that listing is what stops paging, range validation and the messages from
// drifting between them: they were byte-identical copies, so every fix had to be
// made twice, and eventually was not.
type docIndexKind struct {
	id              string
	title           string
	description     string
	pathDescription string
	pageNoun        string // "page" or "pseudo-page", for the schema's wording
}

var (
	docxIndexKind = docIndexKind{
		id:              "docx_index",
		title:           "DOCX Index",
		description:     "Return the semantic index for a Word document (.docx, .docm or .dotx): for each pseudo-page, the topic labels produced by ogcode index. Use this to understand document structure and locate relevant pages before reading them with read_docx_page.",
		pathDescription: "Path to the Word document (.docx, .docm or .dotx), absolute or relative to session directory",
		pageNoun:        "pseudo-page",
	}
	pdfIndexKind = docIndexKind{
		id:              "pdf_index",
		title:           "PDF Index",
		description:     "Return the semantic index for a PDF file: for each page, the topic labels produced by ogcode index. Use this to understand document structure and locate relevant pages before reading them with read_pdf_page.",
		pathDescription: "Path to the PDF file (absolute or relative to session directory)",
		pageNoun:        "page",
	}
)

// docIndexParams is the argument set both index tools accept. The page bounds
// are pointers so an absent bound stays distinguishable from a supplied 0: the
// latter is a caller error to refuse, not a request for the default.
type docIndexParams struct {
	Path      string `json:"path"`
	StartPage *int   `json:"start_page"`
	EndPage   *int   `json:"end_page"`
}

// pageMap is one entry of the listing: a page and the labels stored for it.
type pageMap struct {
	Page   int      `json:"page"`
	Labels []string `json:"labels"`
}

// parameters renders the JSON schema both tools advertise. Only the wording of
// the path and the page noun differ between them; the shape, including the
// optional page range, is the same.
func (k docIndexKind) parameters() json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{
		"type": "object",
		"required": ["path"],
		"properties": {
			"path": {
				"type": "string",
				"description": %q
			},
			"start_page": {
				"type": "integer",
				"description": "First %s to list (1-based, inclusive); omit to start at the first page"
			},
			"end_page": {
				"type": "integer",
				"description": "Last %s to list (1-based, inclusive); omit to list through the last page"
			}
		}
	}`, k.pathDescription, k.pageNoun, k.pageNoun))
}

// execute is the whole of docx_index and pdf_index. A call with no page bounds
// lists every stored page, exactly as before the bounds existed; with bounds it
// lists the slice, so the agent can look at a chapter without paying for the
// whole catalogue.
func (k docIndexKind) execute(store *docindex.Store, args json.RawMessage, tctx Context) (Result, error) {
	if store == nil {
		return Result{Title: k.title, Output: "the document index is not available in this environment"}, nil
	}
	var params docIndexParams
	if err := DecodeArgs(args, &params); err != nil {
		return Result{}, fmt.Errorf("parse %s args: %w", k.id, err)
	}
	if params.Path == "" {
		return Result{Title: k.title, Output: "path is required"}, nil
	}

	// An omitted start_page means page 1 and an omitted end_page means the last
	// page, so a lone end_page still starts at the first page.
	start := 1
	if params.StartPage != nil {
		start = *params.StartPage
	}
	if start < 1 {
		return Result{Title: k.title, Output: "start_page must be >= 1"}, nil
	}
	if params.EndPage != nil && *params.EndPage < 1 {
		return Result{Title: k.title, Output: "end_page must be >= 1"}, nil
	}
	if params.EndPage != nil && *params.EndPage < start {
		return Result{Title: k.title, Output: fmt.Sprintf("end_page must be >= start_page (got %d–%d)", start, *params.EndPage)}, nil
	}

	path := params.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(tctx.SessionDir, path)
	}

	entries, err := store.GetByDoc(path)
	if err != nil {
		return Result{}, fmt.Errorf("get index for %s: %w", path, err)
	}
	if len(entries) == 0 {
		return Result{
			Title:  k.title,
			Output: fmt.Sprintf("no index found for %s — run `ogcode index` first to build the semantic map", filepath.Base(path)),
		}, nil
	}

	// Pseudo-page boundaries are heading-derived, so an edit can renumber every
	// page after the changed section: a row set from an older revision names
	// pages the reader now numbers differently, or one past the end, and the
	// reader refuses them. The modification time recorded at indexing is what
	// tells the two apart. Every row of one document carries the same value, so
	// the last entry speaks for the document; 0 means no time was recorded, which
	// reads as stale.
	info, statErr := os.Stat(path)
	if statErr != nil {
		return Result{
			Title:  k.title,
			Output: fmt.Sprintf("cannot read %s: %v — the index may be stale; run `ogcode index` again", filepath.Base(path), statErr),
		}, nil
	}
	if recorded := entries[len(entries)-1].ModTime; recorded == 0 || recorded != info.ModTime().UnixMilli() {
		return Result{
			Title:  fmt.Sprintf("%s / %s (stale index)", k.title, filepath.Base(path)),
			Output: fmt.Sprintf("the index for %s is stale — the file was modified after it was indexed; run `ogcode index` again", filepath.Base(path)),
		}, nil
	}

	// GetByDoc orders by page number, so the last entry carries the document's
	// last page number. Every kind stores contiguous pages starting at 1 today,
	// which makes it equal to len(entries) — but a kind that stores only the
	// pages it managed to label must not make the bounds lie.
	total := len(entries)
	lastPage := entries[total-1].PageNum

	last := lastPage
	if params.EndPage != nil && *params.EndPage < last {
		last = *params.EndPage // an end past the document is clamped, not refused
	}

	pages := make([]pageMap, 0, total)
	for _, e := range entries {
		if e.PageNum < start || e.PageNum > last {
			continue
		}
		labels := e.Labels
		if labels == nil {
			labels = []string{}
		}
		pages = append(pages, pageMap{Page: e.PageNum, Labels: labels})
	}

	// An empty range still has to be legible, so it reports the bounds that were
	// asked for rather than the clamped pair — those read backwards ("40–12")
	// when the start page lies past the document's last page.
	if len(pages) == 0 {
		shownEnd := last
		if params.EndPage != nil {
			shownEnd = *params.EndPage
		}
		if shownEnd < start {
			shownEnd = start
		}
		return Result{
			Title:  fmt.Sprintf("%s / %s (pages %d–%d of %d)", k.title, filepath.Base(path), start, shownEnd, lastPage),
			Output: fmt.Sprintf("no pages in range %d–%d (document has %d pages)", start, shownEnd, lastPage),
		}, nil
	}

	out, err := json.MarshalIndent(pages, "", "  ")
	if err != nil {
		return Result{}, fmt.Errorf("marshal index: %w", err)
	}

	// The title carries the bounds in force, so the reader can tell a narrowed
	// listing from a short document without counting entries. It names the range
	// whenever the listing is not the whole contiguous document — a start past
	// page 1 or a last page that is not the row count, which is how a kind that
	// stores only pages 1 and 5 of 5 reads. Both advertise "pages 1–5 of 5"
	// instead of a bare "2 pages".
	title := fmt.Sprintf("%s / %s (%d pages)", k.title, filepath.Base(path), total)
	firstListed := pages[0].Page
	if params.StartPage != nil || params.EndPage != nil {
		title = fmt.Sprintf("%s / %s (pages %d–%d of %d)", k.title, filepath.Base(path), start, last, lastPage)
	} else if firstListed != 1 || lastPage != total {
		title = fmt.Sprintf("%s / %s (pages %d–%d of %d)", k.title, filepath.Base(path), firstListed, pages[len(pages)-1].Page, lastPage)
	}

	return Result{Title: title, Output: string(out)}, nil
}
