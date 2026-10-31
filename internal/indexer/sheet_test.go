package indexer

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/docindex"
	excelize "github.com/xuri/excelize/v2"
	_ "modernc.org/sqlite"
)

// writeCSV is the CSV fixture helper — every fixture is built at test time,
// nothing checked in.
func writeCSV(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.csv")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// oneColumnCSV builds a single-column CSV with the given header and data rows.
func oneColumnCSV(header string, rows []string) string {
	var b strings.Builder
	b.WriteString(header + "\n")
	for _, r := range rows {
		b.WriteString(r + "\n")
	}
	return b.String()
}

// TestProfileCSVLattice drives the compatibility lattice end to end through
// ProfileCSV — each case is one column whose votes resolve to the expected
// type via the shared contract.
func TestProfileCSVLattice(t *testing.T) {
	cases := []struct {
		name     string
		header   string
		rows     []string
		wantType string
		wantAff  string
		wantMin  string
		wantMax  string
	}{
		{name: "single kind integer", header: "n", rows: []string{"1", "2", "30"},
			wantType: "integer", wantAff: "INTEGER", wantMin: "1", wantMax: "30"},
		{name: "single kind number", header: "n", rows: []string{"1.5", "2.25"},
			wantType: "number", wantAff: "REAL", wantMin: "1.5", wantMax: "2.25"},
		{name: "int+number mix -> number", header: "n", rows: []string{"5", "1.5"},
			wantType: "number", wantAff: "REAL", wantMin: "1.5", wantMax: "5"},
		{name: "anything + text -> text (widening rule)", header: "n", rows: []string{"5", "N/A"},
			wantType: "text", wantAff: "TEXT", wantMin: "5", wantMax: "N/A"},
		{name: "date + number without format signal -> text", header: "d", rows: []string{"2024-01-02", "3.5"},
			wantType: "text", wantAff: "TEXT", wantMin: "2024-01-02", wantMax: "3.5"},
		{name: "all empty -> empty", header: "e", rows: []string{"", "", ""},
			wantType: "empty", wantAff: "TEXT", wantMin: "", wantMax: ""},
		{name: "single kind boolean", header: "b", rows: []string{"TRUE", "false", "TRUE"},
			wantType: "boolean", wantAff: "TEXT", wantMin: "FALSE", wantMax: "TRUE",
		},
		{name: "single kind date ISO", header: "d", rows: []string{"2024-01-02", "2023-12-31"},
			wantType: "date", wantAff: "TEXT", wantMin: "2023-12-31", wantMax: "2024-01-02"},
		{name: "single kind text", header: "s", rows: []string{"alpha", "beta"},
			wantType: "text", wantAff: "TEXT", wantMin: "alpha", wantMax: "beta"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profiles, err := ProfileCSV(writeCSV(t, oneColumnCSV(tc.header, tc.rows)))
			if err != nil {
				t.Fatal(err)
			}
			if len(profiles) != 1 {
				t.Fatalf("want 1 profile, got %d", len(profiles))
			}
			p := profiles[0]
			if p.Name != "Sheet1" {
				t.Errorf("ProfileCSV must name its profile Sheet1, got %q", p.Name)
			}
			if len(p.Columns) != 1 {
				t.Fatalf("want 1 column, got %d", len(p.Columns))
			}
			c := p.Columns[0]
			if c.Type != tc.wantType {
				t.Errorf("Type = %q, want %q", c.Type, tc.wantType)
			}
			if c.Affinity != tc.wantAff {
				t.Errorf("Affinity = %q, want %q", c.Affinity, tc.wantAff)
			}
			if c.Min != tc.wantMin {
				t.Errorf("Min = %q, want %q", c.Min, tc.wantMin)
			}
			if c.Max != tc.wantMax {
				t.Errorf("Max = %q, want %q", c.Max, tc.wantMax)
			}
		})
	}
}

// TestXLSXSerialDate pins the Excel serial-date rule: a raw number whose
// number format carries date codes classifies as a date, converted from the
// Excel 1900 epoch to ISO-8601 — a real Excel date column must not come out
// as a column of floats around 45000.
func TestXLSXSerialDate(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close() //nolint:errcheck
	if err := f.SetCellValue("Sheet1", "A1", "when"); err != nil {
		t.Fatal(err)
	}
	if err := f.SetCellValue("Sheet1", "A2", 45000); err != nil {
		t.Fatal(err)
	}
	style, err := f.NewStyle(&excelize.Style{NumFmt: 14}) // builtin m/d/yyyy
	if err != nil {
		t.Fatal(err)
	}
	if err := f.SetCellStyle("Sheet1", "A2", "A2", style); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "serial.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}

	profiles, err := ProfileXLSX(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 {
		t.Fatalf("want 1 profile, got %d", len(profiles))
	}
	c := profiles[0].Columns[0]
	want := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, 45000).Format("2006-01-02")
	if c.Type != "date" {
		t.Errorf("serial date column Type = %q, want %q", c.Type, "date")
	}
	if c.Min != want || c.Max != want {
		t.Errorf("Min/Max = %q/%q, want %q", c.Min, c.Max, want)
	}

	// The same number without a date format stays a plain integer.
	f2 := excelize.NewFile()
	defer f2.Close() //nolint:errcheck
	if err := f2.SetCellValue("Sheet1", "A1", "plain"); err != nil {
		t.Fatal(err)
	}
	if err := f2.SetCellValue("Sheet1", "A2", 45000); err != nil {
		t.Fatal(err)
	}
	path2 := filepath.Join(t.TempDir(), "plain.xlsx")
	if err := f2.SaveAs(path2); err != nil {
		t.Fatal(err)
	}
	profiles2, err := ProfileXLSX(path2)
	if err != nil {
		t.Fatal(err)
	}
	if got := profiles2[0].Columns[0].Type; got != "integer" {
		t.Errorf("plain number column Type = %q, want %q", got, "integer")
	}
}

// TestXLSXDeclaredBool pins the GetCellType tie-breaker: an xlsx boolean cell
// declared as such (raw "1"/"0") classifies as boolean, not integer.
func TestXLSXDeclaredBool(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close() //nolint:errcheck
	if err := f.SetCellValue("Sheet1", "A1", "flag"); err != nil {
		t.Fatal(err)
	}
	if err := f.SetCellBool("Sheet1", "A2", true); err != nil {
		t.Fatal(err)
	}
	if err := f.SetCellBool("Sheet1", "A3", false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "bool.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	profiles, err := ProfileXLSX(path)
	if err != nil {
		t.Fatal(err)
	}
	c := profiles[0].Columns[0]
	if c.Type != "boolean" {
		t.Errorf("declared bool column Type = %q, want %q", c.Type, "boolean")
	}
	if c.Min != "FALSE" || c.Max != "TRUE" {
		t.Errorf("Min/Max = %q/%q, want FALSE/TRUE", c.Min, c.Max)
	}
}

// TestEmptyCellsDontVote: empty (blank or whitespace) cells never contribute
// to the vote, so N/A-style gaps do not force a column to text.
func TestEmptyCellsDontVote(t *testing.T) {
	profiles, err := ProfileCSV(writeCSV(t, oneColumnCSV("n", []string{"", " ", "5", "\t"})))
	if err != nil {
		t.Fatal(err)
	}
	c := profiles[0].Columns[0]
	if c.Type != "integer" {
		t.Errorf("Type = %q, want integer (empties must not vote)", c.Type)
	}
}

// TestStrideSample: rows past the 1000-row cap are skipped, but the stride
// sample folds rows 2000/4000/8000/... into the same vote, so a column that
// turns text deep in the file is still caught.
func TestStrideSample(t *testing.T) {
	build := func(junkAt int) string {
		var b strings.Builder
		b.WriteString("v\n")
		for i := 1; i <= 2100; i++ {
			if i == junkAt {
				b.WriteString("oops\n")
			} else {
				b.WriteString("7\n")
			}
		}
		return b.String()
	}

	t.Run("junk inside the cap is caught", func(t *testing.T) {
		profiles, err := ProfileCSV(writeCSV(t, build(500)))
		if err != nil {
			t.Fatal(err)
		}
		if got := profiles[0].Columns[0].Type; got != "text" {
			t.Errorf("row 500 is inside the 1000-row cap: Type = %q, want text", got)
		}
	})

	t.Run("junk past the cap but on the stride is caught", func(t *testing.T) {
		profiles, err := ProfileCSV(writeCSV(t, build(2000)))
		if err != nil {
			t.Fatal(err)
		}
		p := profiles[0]
		if got := p.Columns[0].Type; got != "text" {
			t.Errorf("stride row 2000 missed: Type = %q, want text", got)
		}
		if p.Total != 2100 {
			t.Errorf("Total = %d, want 2100", p.Total)
		}
		if p.Rows <= scanRowCapForTest {
			t.Errorf("Rows = %d, want > %d (cap plus stride rows)", p.Rows, scanRowCapForTest)
		}
		if len(p.Sample) == 0 || p.Sample[0][0] != "7" {
			t.Errorf("sample[0] = %v, want leading rows with normalized values", p.Sample)
		}
	})

	t.Run("junk past the cap and off the stride is not caught", func(t *testing.T) {
		profiles, err := ProfileCSV(writeCSV(t, build(1500))) // past cap, off stride
		if err != nil {
			t.Fatal(err)
		}
		if got := profiles[0].Columns[0].Type; got != "integer" {
			t.Errorf("off-stride junk at row 1500 surfaced: Type = %q, want integer", got)
		}
	})
	profiles, err := ProfileCSV(writeCSV(t, oneColumnCSV("v", func() []string {
		rows := make([]string, 0, 2100)
		for i := 1; i <= 2099; i++ {
			rows = append(rows, "7")
		}
		rows = append(rows, "oops") // row 2100: past cap, off the stride
		return rows
	}())))
	if err != nil {
		t.Fatal(err)
	}
	if got := profiles[0].Columns[0].Type; got != "integer" {
		t.Errorf("off-stride junk at row 2100 surfaced: Type = %q, want integer", got)
	}
}

// scanRowCapForTest mirrors internal/sheet's cap without reaching into the
// leaf package's unexported names.
const scanRowCapForTest = 1000

// TestCSVSniffing covers delimiter sniffing (, ; \t) and the UTF-8/UTF-16
// BOM handling; every variant must yield exactly one profile named Sheet1.
func TestCSVSniffing(t *testing.T) {
	cases := []struct {
		name    string
		content []byte
		want    []string
	}{
		{name: "semicolon", content: []byte("a;b\n1;2\n"), want: []string{"a", "b"}},
		{name: "tab", content: []byte("a\tb\n1\t2\n"), want: []string{"a", "b"}},
		{name: "utf8 bom", content: append([]byte{0xEF, 0xBB, 0xBF}, []byte("a,b\n1,2\n")...), want: []string{"a", "b"}},
		{name: "utf16le bom", content: utf16LE("a,b\n1,2\n"), want: []string{"a", "b"}},
		{name: "comma", content: []byte("a,b\n1,2\n"), want: []string{"a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sniff.csv")
			if err := os.WriteFile(path, tc.content, 0o644); err != nil {
				t.Fatal(err)
			}
			profiles, err := ProfileCSV(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(profiles) != 1 {
				t.Fatalf("want exactly 1 profile, got %d", len(profiles))
			}
			p := profiles[0]
			if p.Name != "Sheet1" {
				t.Errorf("profile name = %q, want Sheet1", p.Name)
			}
			if got := p.Headers; len(got) != 2 || got[0] != tc.want[0] || got[1] != tc.want[1] {
				t.Errorf("Headers = %q, want %q", got, tc.want)
			}
		})
	}
}

func utf16LE(s string) []byte {
	out := []byte{0xFF, 0xFE}
	for _, r := range s {
		out = append(out, byte(r&0xFF), byte(r>>8))
	}
	return out
}

// TestProfileSQLExecutes executes every emitted CREATE TABLE on a real
// modernc.org/sqlite connection — for CSV, XLSX and an empty XLSX sheet.
func TestProfileSQLExecutes(t *testing.T) {
	csvPath := writeCSV(t, "name,qty,price,when\nx,1,1.5,2024-01-02\ny,2,2.5,2024-01-03\n")

	f := excelize.NewFile()
	defer f.Close() //nolint:errcheck
	if err := f.SetCellValue("Sheet1", "A1", "id"); err != nil {
		t.Fatal(err)
	}
	if err := f.SetCellValue("Sheet1", "A2", "42"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.NewSheet("Empty!"); err != nil {
		t.Fatal(err)
	}
	xlsxPath := filepath.Join(t.TempDir(), "db.xlsx")
	if err := f.SaveAs(xlsxPath); err != nil {
		t.Fatal(err)
	}

	var profiles []SheetProfile
	p, err := ProfileCSV(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	profiles = append(profiles, p...)
	x, err := ProfileXLSX(xlsxPath)
	if err != nil {
		t.Fatal(err)
	}
	profiles = append(profiles, x...)

	for _, prof := range profiles {
		t.Run(prof.Name, func(t *testing.T) {
			db, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close() //nolint:errcheck
			if _, err := db.Exec(prof.SQL); err != nil {
				t.Errorf("SQL %q does not execute: %v", prof.SQL, err)
			}
		})
	}
}

// TestProfileXLSXMultiSheet pins one profile per sheet, sheet names carried,
// and an entirely empty sheet still producing a valid zero-column profile.
func TestProfileXLSXMultiSheet(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close() //nolint:errcheck
	if _, err := f.NewSheet("Numbers"); err != nil {
		t.Fatal(err)
	}
	if err := f.SetCellValue("Numbers", "A1", "qty"); err != nil {
		t.Fatal(err)
	}
	if err := f.SetCellValue("Numbers", "A2", "3"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.NewSheet("Vacant"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "multi.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}

	profiles, err := ProfileXLSX(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 3 {
		t.Fatalf("want 3 profiles (one per sheet, including empty), got %d", len(profiles))
	}
	wantNames := map[string]bool{"Sheet1": true, "Numbers": true, "Vacant": true}
	for _, p := range profiles {
		if !wantNames[p.Name] {
			t.Errorf("unexpected profile name %q", p.Name)
		}
	}
	for _, p := range profiles {
		if p.Name == "Numbers" {
			if p.Rows != 1 || p.Columns[0].Type != "integer" {
				t.Errorf("Numbers: Rows=%d Type=%q, want 1/integer", p.Rows, p.Columns[0].Type)
			}
		}
		if p.Name == "Vacant" && (p.Rows != 0 || len(p.Columns) != 0) {
			t.Errorf("Vacant: Rows=%d columns=%d, want 0/0", p.Rows, len(p.Columns))
		}
	}
}

// TestDistinctRules: a column keeps distinct values only while the universe
// stays at or below 12 candidates, listing the top 8 by frequency.
func TestDistinctRules(t *testing.T) {
	t.Run("3 distinct, one repeated -> top by frequency", func(t *testing.T) {
		rows := []string{"east", "west", "east", "north", "east", "west"}
		profiles, err := ProfileCSV(writeCSV(t, oneColumnCSV("region", rows)))
		if err != nil {
			t.Fatal(err)
		}
		d := profiles[0].Columns[0].Distinct
		if len(d) != 3 || d[0] != "east" || d[1] != "west" || d[2] != "north" {
			t.Errorf("Distinct = %q, want [east west north]", d)
		}
	})

	t.Run("15 distinct -> nil", func(t *testing.T) {
		rows := make([]string, 0, 15)
		for i := 0; i < 15; i++ {
			rows = append(rows, strings.Repeat("z", i+1))
		}
		profiles, err := ProfileCSV(writeCSV(t, oneColumnCSV("code", rows)))
		if err != nil {
			t.Fatal(err)
		}
		if got := profiles[0].Columns[0].Distinct; got != nil {
			t.Errorf("Distinct = %v, want nil when the universe exceeds 12", got)
		}
	})
}

// TestExtractSheetPages pins the indexer-side mapping: one page per sheet,
// page numbers sequential, page text carrying the rendered profile.
func TestExtractSheetPages(t *testing.T) {
	csvPath := writeCSV(t, "region,qty\neast,3\nwest,5\n")
	pages, err := ExtractSheetPages(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("want 1 page for a CSV, got %d", len(pages))
	}
	if pages[0].PageNum != 1 {
		t.Errorf("PageNum = %d, want 1", pages[0].PageNum)
	}
	for _, want := range []string{`Sheet "Sheet1"`, "2 data rows", "region: text [TEXT]", "qty: integer [INTEGER]", "distinct: east, west"} {
		if !strings.Contains(pages[0].Text, want) {
			t.Errorf("page text missing %q:\n%s", want, pages[0].Text)
		}
	}

	f := excelize.NewFile()
	defer f.Close() //nolint:errcheck
	if _, err := f.NewSheet("Second"); err != nil {
		t.Fatal(err)
	}
	xlsxPath := filepath.Join(t.TempDir(), "pages.xlsx")
	if err := f.SaveAs(xlsxPath); err != nil {
		t.Fatal(err)
	}
	pages, err = ExtractSheetPages(xlsxPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 {
		t.Fatalf("want 2 pages for a two-sheet workbook, got %d", len(pages))
	}
	if pages[1].PageNum != 2 {
		t.Errorf("PageNum = %d, want 2", pages[1].PageNum)
	}
	// The page text carries the sheet name on the docindex pages too.
	if !strings.Contains(pages[1].Text, `Sheet "Second"`) {
		t.Errorf("second page text missing sheet name:\n%s", pages[1].Text)
	}
}

// TestIsSheetExtMatchesTheSharedList pins that the indexer's dispatch agrees
// with the docindex store queries: its extension set is exactly
// docindex.SheetExtensions, and .xls/.ods belong to no list at all — the text
// extractor must not pick them up either.
func TestIsSheetExtMatchesTheSharedList(t *testing.T) {
	for _, ext := range docindex.SheetExtensions {
		if !isSheetExt(ext) {
			t.Errorf("isSheetExt(%q) = false, want true (shared list drifts)", ext)
		}
	}
	for _, ext := range []string{".xls", ".ods", ".pdf"} {
		if isSheetExt(ext) {
			t.Errorf("isSheetExt(%q) = true, want false", ext)
		}
		if IsTextFile(ext) {
			t.Errorf("IsTextFile(%q) = true, want false", ext)
		}
	}
}

// TestExtractSheetValueKeywords pins the value-aware extraction on a rendered
// profile: ISO dates survive whole, comma-grouped amounts lose their
// separators, single-digit noise is dropped and the result is deduplicated.
func TestExtractSheetValueKeywords(t *testing.T) {
	text := `Sheet "Q1": 1204 data rows, 2 columns
Columns:
- Amount: number [REAL], min 12, max 9,950, distinct: 1,234.5, 9,950
- Day: date [TEXT], min 2024-01-02, max 2024-03-31, distinct: 2024-01-02
First 2 rows:
| 1,234.5 | 2024-01-02
`
	got := extractSheetValueKeywords(text)
	for _, want := range []string{"1204", "12", "9950", "1234.5", "2024-01-02", "2024-03-31"} {
		if !containsKeyword(got, want) {
			t.Errorf("extractSheetValueKeywords missing value %q: %v", want, got)
		}
	}
	for _, absent := range []string{"2024", "01", "05", "3", "1204 data"} {
		if containsKeyword(got, absent) {
			t.Errorf("extractSheetValueKeywords leaked %q: %v", absent, got)
		}
	}
}

// TestBuildCorporaCarriesSheetValues pins the real label-agent input: two
// workbook fixtures with identical column names ("Name", "Day", "Amount")
// but different value ranges produce distinguishable keyword corpora — the
// keywords the indexer hands the label agent carry the profile's values —
// while the same profile text without the sheet marker keeps the word-only
// extraction and carries none of the numbers.
func TestBuildCorporaCarriesSheetValues(t *testing.T) {
	pagesA, err := genericSheetFixture(t, "east", "west", 300, "2024-01-02")
	if err != nil {
		t.Fatal(err)
	}
	pagesB, err := genericSheetFixture(t, "east", "west", 9950, "2023-06-10")
	if err != nil {
		t.Fatal(err)
	}

	corpusA := BuildCorpora(pagesA)[0].Keywords
	corpusB := BuildCorpora(pagesB)[0].Keywords

	for _, want := range []string{"2024-01-02", "300"} {
		if !containsKeyword(corpusA, want) {
			t.Errorf("corpus A missing value keyword %q: %v", want, corpusA)
		}
	}
	for _, want := range []string{"2023-06-10", "9950"} {
		if !containsKeyword(corpusB, want) {
			t.Errorf("corpus B missing value keyword %q: %v", want, corpusB)
		}
	}
	for _, absent := range []string{"2023-06-10", "9950"} {
		if containsKeyword(corpusA, absent) {
			t.Errorf("corpus A carries B's value %q: %v", absent, corpusA)
		}
	}
	for _, absent := range []string{"2024-01-02", "300"} {
		if containsKeyword(corpusB, absent) {
			t.Errorf("corpus B carries A's value %q: %v", absent, corpusB)
		}
	}

	// Without the sheet marker, the same profile text yields word keywords
	// only — no value ever reaches a non-sheet page's corpus.
	plain := PageText{PageNum: 1, Text: pagesA[0].Text}
	if got := BuildCorpora([]PageText{plain})[0].Keywords; containsKeyword(got, "9950") || containsKeyword(got, "2024-01-02") {
		t.Errorf("non-sheet page leaked value keywords: %v", got)
	}
}

// genericSheetFixture builds a one-sheet workbook whose three columns are
// identically named in every fixture, with a text column holding the given
// cells, a date column and an Amount column whose two rows are amount/10 and
// amount — so the fixtures differ only in their values.
func genericSheetFixture(t *testing.T, cell1, cell2 string, amount int, day string) ([]PageText, error) {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close() //nolint:errcheck
	for i, header := range []string{"Name", "Day", "Amount"} {
		if err := f.SetCellValue("Sheet1", string(rune('A'+i))+"1", header); err != nil {
			return nil, err
		}
	}
	// Same header names in fixture B too — that is the point of the fixture.
	rows := [][]any{
		{cell1, day, amount / 10},
		{cell2, day, amount},
	}
	for r, row := range rows {
		for ci, v := range row {
			if err := f.SetCellValue("Sheet1", string(rune('A'+ci))+fmt.Sprintf("%d", r+2), v); err != nil {
				return nil, err
			}
		}
	}
	path := filepath.Join(t.TempDir(), "generic.xlsx")
	if err := f.SaveAs(path); err != nil {
		return nil, err
	}
	return ExtractSheetPages(path)
}

// containsKeyword reports whether the list holds the exact keyword.
func containsKeyword(kws []string, want string) bool {
	for _, k := range kws {
		if k == want {
			return true
		}
	}
	return false
}
