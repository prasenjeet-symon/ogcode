package indexer

// Spreadsheet indexing. The heavy lifting (per-cell type inference, the
// compatibility lattice, min/max/distinct/sample accumulation, SQL DDL) lives
// in the leaf package internal/sheet so that internal/tool (query_sheet) can
// share the exact same profile without an import cycle — tool already imports
// agent which imports indexer.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/docindex"
	"github.com/prasenjeet-symon/ogcode/internal/sheet"
)

// isSheetExt reports whether ext names a spreadsheet that the indexer
// profiles as a multi-page document. Delimited files (csv/tsv) are NOT text
// files (see text.go) — they flow through this multi-page path instead, one
// page per sheet. The list is owned by docindex.SheetExtensions, shared with
// the store's listing queries.
func isSheetExt(ext string) bool {
	ext = strings.ToLower(ext)
	for _, want := range docindex.SheetExtensions {
		if want == ext {
			return true
		}
	}
	return false
}

// SheetColumn and SheetProfile are aliases of the leaf package's types so
// that the query_sheet tool (which imports internal/sheet directly) and the
// indexed document text can never disagree on shape or semantics.
type (
	// SheetColumn describes one column of a sheet profile.
	SheetColumn = sheet.SheetColumn
	// SheetProfile describes one sheet of a workbook (or the whole CSV file,
	// named "Sheet1").
	SheetProfile = sheet.SheetProfile
)

// ProfileXLSX profiles every sheet in an .xlsx/.xlsm workbook, one profile
// per sheet. See internal/sheet for the inference rules.
func ProfileXLSX(path string) ([]SheetProfile, error) {
	return sheet.ProfileXLSX(path)
}

// ProfileCSV profiles a delimited text file (.csv/.tsv) as exactly one sheet
// named "Sheet1", sniffing the delimiter and any UTF-8/UTF-16 BOM.
func ProfileCSV(path string) ([]SheetProfile, error) {
	return sheet.ProfileCSV(path)
}

// ExtractSheetPages converts a spreadsheet file into one indexed page per
// sheet, each page carrying the profile text. This is the indexer-side
// counterpart of ExtractPages (PDF) and ExtractDocxPages (DOCX).
func ExtractSheetPages(path string) ([]PageText, error) {
	ext := strings.ToLower(filepath.Ext(path))
	var profiles []SheetProfile
	var err error
	switch ext {
	case ".xlsx", ".xlsm":
		profiles, err = sheet.ProfileXLSX(path)
	case ".csv", ".tsv":
		profiles, err = sheet.ProfileCSV(path)
	default:
		return nil, fmt.Errorf("not a spreadsheet extension: %s", ext)
	}
	if err != nil {
		return nil, err
	}
	pages := make([]PageText, len(profiles))
	for i, p := range profiles {
		pages[i] = PageText{PageNum: i + 1, Sheet: true, Text: sheet.ProfileText(p)}
	}
	return pages, nil
}

var (
	// sheetDateRe matches ISO-8601 dates the profile carries (normalised
	// date Min/Max, sample cells); they must survive whole as keywords.
	sheetDateRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
	// sheetNumRe matches integer and decimal numbers, comma-grouped or not —
	// min/max extremes, row counts, counts and numeric distinct values.
	sheetNumRe = regexp.MustCompile(`\d[\d,]*(?:\.\d+)?`)
)

// extractSheetValueKeywords pulls the distinctive VALUES of a spreadsheet
// profile out of its rendered text — ISO dates, row counts, min/max extremes
// and decimal or comma-grouped amounts — as extra keywords for the label
// agent. The shared word-only extractor (extractKeywords) splits on letters,
// so none of these numbers would otherwise reach it and two sheets with
// generic column names ("Name", "Date", "Value") would be indistinguishable.
// Dates keep their full form, thousands separators are dropped, tokens are
// deduplicated and single-character matches fall away, matching
// extractKeywords' rules.
func extractSheetValueKeywords(text string) []string {
	seen := make(map[string]struct{})
	var values []string
	add := func(v string) {
		if len(v) < 2 {
			return
		}
		if _, dup := seen[v]; dup {
			return
		}
		seen[v] = struct{}{}
		values = append(values, v)
	}
	for _, d := range sheetDateRe.FindAllString(text, -1) {
		add(d)
	}
	// Dates first, then numbers on the remainder — so a date's digits do not
	// re-surface as fragmented numbers ("2024", "01", "05").
	withoutDates := sheetDateRe.ReplaceAllString(text, " ")
	for _, n := range sheetNumRe.FindAllString(withoutDates, -1) {
		add(strings.ReplaceAll(n, ",", ""))
	}
	return values
}
