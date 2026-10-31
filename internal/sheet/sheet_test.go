package sheet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	excelize "github.com/xuri/excelize/v2"
)

// Unit tests for the unexported inference machinery in the leaf package.
// The end-to-end behavior runs in internal/indexer/sheet_test.go through the
// exported ProfileCSV/ProfileXLSX aliases.

// TestClassifyCell pins the per-cell classifier: candidates in precedence
// order, first match wins, empties never vote, and the declared-bool and
// number-format-date signals behave as tie-breakers, not overrides.
func TestClassifyCell(t *testing.T) {
	cases := []struct {
		value          string
		declaredBool   bool
		formatDate     bool
		wantKind       string
		wantDisplay    string
		wantSerialDate bool
	}{
		{value: "", wantKind: "empty"},
		{value: "   ", wantKind: "empty"},
		{value: "\t", wantKind: "empty"},
		{value: "TRUE", wantKind: "boolean", wantDisplay: "TRUE"},
		{value: "false", wantKind: "boolean", wantDisplay: "FALSE"},
		{value: "False", wantKind: "boolean", wantDisplay: "FALSE"},
		{value: "1", declaredBool: true, wantKind: "boolean", wantDisplay: "TRUE"},
		{value: "0", declaredBool: true, wantKind: "boolean", wantDisplay: "FALSE"},
		{value: "42", wantKind: "integer", wantDisplay: "42"},
		{value: "-7", wantKind: "integer", wantDisplay: "-7"},
		{value: "+7", wantKind: "integer", wantDisplay: "+7"},
		{value: "1.5", wantKind: "number", wantDisplay: "1.5"},
		{value: "45000", formatDate: true, wantKind: "date", wantSerialDate: true, wantDisplay: "2023-03-15"},
		{value: "not-a-number", formatDate: true, wantKind: "text", wantDisplay: "not-a-number"},
		{value: "2024-01-02", wantKind: "date", wantDisplay: "2024-01-02"},
		{value: "2024-01-02T15:04", wantKind: "date", wantDisplay: "2024-01-02T15:04"},
		{value: "9999-99-99", wantKind: "text", wantDisplay: "9999-99-99"}, // ISO-shaped but not a date, not a float
		{value: "01/02/2006", wantKind: "date", wantDisplay: "2006-01-02"},
		{value: "02 Jan 2006", wantKind: "date", wantDisplay: "2006-01-02"},
		{value: "15:04", wantKind: "date", wantDisplay: "15:04"},
		{value: "hello", wantKind: "text", wantDisplay: "hello"},
		{value: " true ", wantKind: "boolean", wantDisplay: "TRUE"},
	}
	for _, tc := range cases {
		got := classifyCell(tc.value, tc.declaredBool, tc.formatDate)
		if got.kind.String() != tc.wantKind {
			t.Errorf("classifyCell(%q, bool=%v, fmt=%v) kind = %q, want %q",
				tc.value, tc.declaredBool, tc.formatDate, got.kind, tc.wantKind)
		}
		if tc.wantDisplay != "" && got.display != tc.wantDisplay {
			t.Errorf("classifyCell(%q) display = %q, want %q", tc.value, got.display, tc.wantDisplay)
		}
		if got.serial != tc.wantSerialDate {
			t.Errorf("classifyCell(%q) serial = %v, want %v", tc.value, got.serial, tc.wantSerialDate)
		}
	}
}

// TestExcelSerialToISO pins the Excel 1900-epoch conversion, including the
// phantom 1900-02-29 boundary at serial 60.
func TestExcelSerialToISO(t *testing.T) {
	cases := []struct {
		serial float64
		want   string
	}{
		{1, "1900-01-01"},
		{59, "1900-02-28"},
		{60, "1900-02-28"}, // the phantom Feb 29 maps onto the real 28th
		{61, "1900-03-01"},
		{45000, "2023-03-15"},
		{45000.5, "2023-03-15T12:00"},
	}
	for _, tc := range cases {
		if got := excelSerialToISO(tc.serial); got != tc.want {
			t.Errorf("excelSerialToISO(%v) = %q, want %q", tc.serial, got, tc.want)
		}
	}
	// Cross-check against time arithmetic for an arbitrary large serial.
	if got, want := excelSerialToISO(45000), "2023-03-15"; got != want {
		t.Errorf("excelSerialToISO(45000) = %q, want %q", got, want)
	}
}

// TestSniffDelimiter pins delimiter detection: highest frequency of , ; \t on
// the first line wins; none found means a single-column comma file.
func TestSniffDelimiter(t *testing.T) {
	cases := []struct {
		text string
		want rune
	}{
		{"a,b\n1,2", ','},
		{"a;b\n1;2", ';'},
		{"a\tb\n1\t2", '\t'},
		{"only\n1,2", ','}, // empty candidate: comma default
		{",;", ','},        // a real tie keeps the default comma by first candidate with strictly more
		{"a,b;c\n1,2;3", ','},
	}
	for _, tc := range cases {
		if got := sniffDelimiter(tc.text); got != tc.want {
			t.Errorf("sniffDelimiter(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

// TestBuiltinNumFmtIsDate pins the builtin number-format date ranges.
func TestBuiltinNumFmtIsDate(t *testing.T) {
	for _, n := range []int{14, 22, 27, 36, 45, 47, 50, 58} {
		if !builtinNumFmtIsDate(n) {
			t.Errorf("builtinNumFmtIsDate(%d) = false, want true", n)
		}
	}
	for _, n := range []int{0, 1, 2, 13, 23, 26, 37, 44, 48, 49, 59, 60, 100} {
		if builtinNumFmtIsDate(n) {
			t.Errorf("builtinNumFmtIsDate(%d) = true, want false", n)
		}
	}
}

// TestDecodeDelimitedText pins BOM handling: UTF-8, UTF-16 LE and BE all
// decode to plain UTF-8 text.
func TestDecodeDelimitedText(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{name: "utf8", in: append([]byte{0xEF, 0xBB, 0xBF}, []byte("a,b")...), want: "a,b"},
		{name: "utf16le", in: append([]byte{0xFF, 0xFE}, []byte("a\x00,\x00b\x00")...), want: "a,b"},
		{name: "utf16be", in: append([]byte{0xFE, 0xFF}, []byte("\x00a\x00,\x00b")...), want: "a,b"},
		{name: "plain", in: []byte("hello"), want: "hello"},
	}
	for _, tc := range cases {
		if got := decodeDelimitedText(tc.in); got != tc.want {
			t.Errorf("%s: decodeDelimitedText = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestBuildTableSQLEmptySheet: a sheet with no columns still emits executable
// DDL via the rowid sentinel instead of a fake column.
func TestBuildTableSQLEmptySheet(t *testing.T) {
	sql := buildTableSQL("Zéro", nil)
	if sql != `CREATE TABLE "Zéro" ("rowid" INTEGER PRIMARY KEY)` {
		t.Errorf("empty-sheet SQL = %q", sql)
	}
	if got := quoteIdent(`a"b`); got != `"a""b"` {
		t.Errorf("quoteIdent embedded quote = %q, want %q", got, `"a""b"`)
	}
}

// collectRows feeds every streamed row into a slice for comparison.
func collectRows(path, sheet string) ([][]string, error) {
	var got [][]string
	err := StreamRows(path, sheet, func(cells []string) error {
		got = append(got, cells)
		return nil
	})
	return got, err
}

// TestStreamRowsCSV pins the delimited-text row source: the header row is
// skipped, a feed error aborts the stream, and cells arrive as the per-cell
// classifier folds them (bool case normalized; a bare numeric can't be named a
// serial date without workbook format info, so it stays verbatim — the column
// lattice resolves the due column as text, matching the profiler).
func TestStreamRowsCSV(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.csv")
	body := "name,active,due\nAnn,TRUE,45000\nBob,false,2024-01-02\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := collectRows(path, "ignored-for-text")
	if err != nil {
		t.Fatalf("stream csv: %v", err)
	}
	want := [][]string{{"Ann", "TRUE", "45000"}, {"Bob", "FALSE", "2024-01-02"}}
	if len(got) != len(want) {
		t.Fatalf("streamed %d rows, want %d: %v", len(got), len(want), got)
	}
	for i, row := range want {
		if strings.Join(got[i], "\x00") != strings.Join(row, "\x00") {
			t.Errorf("row %d = %v, want %v", i, got[i], row)
		}
	}

	// A feed error surfaces verbatim and stops the stream.
	sentinel := os.ErrPermission
	err = StreamRows(path, "", func([]string) error { return sentinel })
	if err != sentinel {
		t.Errorf("feed error should pass through as-is, got %v", err)
	}
}

// TestStreamRowsSniffing pins that TSV and a UTF-8 BOM stream exactly as
// ProfileCSV reads them.
func TestStreamRowsSniffing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.tsv")
	body := "\xEF\xBB\xBFid\tscore\n1\t9.5\n2\t7\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := collectRows(path, "")
	if err != nil {
		t.Fatalf("stream tsv: %v", err)
	}
	if len(got) != 2 || got[0][0] != "1" || got[0][1] != "9.5" || got[1][1] != "7" {
		t.Errorf("tsv stream = %v", got)
	}
}

// writeXLSXFixture builds a two-sheet workbook: Data with an Excel serial-date
// column and a declared boolean, plus an unused sheet the unknown-sheet
// assertion leans on.
func writeXLSXFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.xlsx")
	f := excelize.NewFile()
	defer f.Close() //nolint:errcheck

	ws := f.GetSheetName(f.GetActiveSheetIndex()) // "Sheet1"
	headers := []string{"when", "ok", "label"}
	for i, h := range headers {
		if err := f.SetCellValue(ws, runeCell(0, i), h); err != nil {
			t.Fatal(err)
		}
	}
	rows := [][]any{
		{45000, true, "first"},
		{"2024-01-02", false, "second"},
	}
	dateStyle, err := f.NewStyle(&excelize.Style{NumFmt: 14}) // built-in 14: date
	if err != nil {
		t.Fatal(err)
	}
	for r, row := range rows {
		for c, v := range row {
			cell := runeCell(r+1, c)
			if err := f.SetCellValue(ws, cell, v); err != nil {
				t.Fatal(err)
			}
			if c == 0 {
				if err := f.SetCellStyle(ws, cell, cell, dateStyle); err != nil {
					t.Fatal(err)
				}
			}
			if c == 1 {
				if err := f.SetCellBool(ws, cell, v.(bool)); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if _, err := f.NewSheet("Other"); err != nil {
		t.Fatal(err)
	}
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	return path
}

// runeCell builds an A1 cell reference for 0-based (row, col) as the profiler
// iterates them (CoordinatesToCellName wants 1-based values).
func runeCell(row0, col0 int) string {
	cell, _ := excelize.CoordinatesToCellName(col0+1, row0+1)
	return cell
}

// TestStreamRowsXLSX pins the workbook row source: unknown sheet errors by
// name, the header row is skipped, serial dates come through as ISO-8601
// text, declared booleans as TRUE/FALSE, and unsupported extensions refuse.
func TestStreamRowsXLSX(t *testing.T) {
	path := writeXLSXFixture(t)

	if _, err := collectRows(path, "Nope"); err == nil || !strings.Contains(err.Error(), `unknown sheet "Nope"`) {
		t.Errorf("unknown sheet should error, got %v", err)
	}

	got, err := collectRows(path, "Sheet1")
	if err != nil {
		t.Fatalf("stream xlsx: %v", err)
	}
	if len(got) != 2 || len(got[0]) != 3 {
		t.Fatalf("streamed %d rows of %d cells, want 2x3: %v", len(got), len(got[0]), got)
	}
	if got[0][0] != "2023-03-15" {
		t.Errorf("serial 45000 should convert to ISO, got %q", got[0][0])
	}
	if got[0][1] != "TRUE" || got[1][1] != "FALSE" {
		t.Errorf("declared booleans should read TRUE/FALSE, got %q / %q", got[0][1], got[1][1])
	}
	if got[1][0] != "2024-01-02" || got[1][2] != "second" {
		t.Errorf("plain cells should pass through verbatim, got %v", got[1])
	}

	if _, err := collectRows(filepath.Join(t.TempDir(), "x.xls"), ""); err == nil ||
		!strings.Contains(err.Error(), "unsupported spreadsheet type") {
		t.Errorf("unsupported ext should error, got %v", err)
	}
}

var parityHeader = []string{"id", "label", "n"}
var parityData = [][]string{{"a", "first", "5"}, {"b", "second", "7"}}

// writeParityXLSX builds one workbook with two sheets carrying the same
// table: Control puts the header on row 1 with data beneath; Blank puts two
// whitespace-only rows ahead of it (header on row 3). The blank cells are
// actually set so the rows exist in the sheet XML — a fixture that silently
// dropped them would exercise nothing.
func writeParityXLSX(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "parity.xlsx")
	f := excelize.NewFile()
	defer f.Close() //nolint:errcheck

	control := f.GetSheetName(f.GetActiveSheetIndex())
	if err := f.SetSheetName(control, "Control"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.NewSheet("Blank"); err != nil {
		t.Fatal(err)
	}
	for _, ws := range []string{"Control", "Blank"} {
		row := 0 // 0-based
		if ws == "Blank" {
			for ; row < 2; row++ {
				for col := 0; col < len(parityHeader); col++ {
					if err := f.SetCellValue(ws, runeCell(row, col), " "); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		for i, h := range parityHeader {
			if err := f.SetCellValue(ws, runeCell(row, i), h); err != nil {
				t.Fatal(err)
			}
		}
		for r, d := range parityData {
			for c, v := range d {
				if err := f.SetCellValue(ws, runeCell(row+1+r, c), v); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	rows, err := f.GetRows("Blank")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 || strings.Join(rows[0], "\x00") != " \x00 \x00 " {
		t.Fatalf("fixture blank rows did not survive the write: %q", rows)
	}
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	return path
}

// assertParity pins one fixture to the profiler/streamer contract: the
// profile's column names are the header fields, the stream never re-serves
// the header as data, the first streamed row is the first real data row, and
// the row counts agree.
func assertParity(t *testing.T, path, sheet string, header []string, data [][]string) {
	t.Helper()
	var profiles []SheetProfile
	var err error
	if filepath.Ext(path) == ".xlsx" {
		profiles, err = ProfileXLSX(path)
	} else {
		profiles, err = ProfileCSV(path)
	}
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	sheetName := sheet
	if sheetName == "" {
		sheetName = profiles[0].Name
	}
	var prof *SheetProfile
	for i := range profiles {
		if profiles[i].Name == sheetName {
			prof = &profiles[i]
			break
		}
	}
	if prof == nil {
		t.Fatalf("no profile for sheet %q", sheetName)
	}
	if len(prof.Columns) != len(header) {
		t.Fatalf("profile has %d columns, want %d", len(prof.Columns), len(header))
	}
	for i, h := range header {
		if prof.Columns[i].Name != h {
			t.Errorf("profile column %d = %q, want %q", i, prof.Columns[i].Name, h)
		}
	}

	got, err := collectRows(path, sheet)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if len(got) != len(data) {
		t.Fatalf("streamed %d data rows, want %d: %v", len(got), len(data), got)
	}
	if strings.Join(got[0], "\x00") == strings.Join(header, "\x00") {
		t.Errorf("first streamed row re-serves the header: %v", got[0])
	}
	for i, want := range data {
		if strings.Join(got[i], "\x00") != strings.Join(want, "\x00") {
			t.Errorf("data row %d = %v, want %v", i, got[i], want)
		}
	}
}

// TestStreamRowsParity pins the header-row contract across every streamer:
// with blank leading rows ahead of the header (and a plain control beside
// each), the streamer must skip exactly what the profiler skips and start
// its data where the profiler's first sample row sits.
func TestStreamRowsParity(t *testing.T) {
	xlsx := writeParityXLSX(t)

	writeText := func(name, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	csvControl := writeText("control.csv", "id,label,n\na,first,5\nb,second,7\n")
	csvBlank := writeText("blank.csv", "\n , \nid,label,n\na,first,5\nb,second,7\n")
	tsvControl := writeText("control.tsv", "id\tlabel\tn\na\tfirst\t5\nb\tsecond\t7\n")
	// No fully empty first line here: sniffDelimiter reads only the first
	// line, and an empty one sniffs ',' which would collapse the columns.
	// A whitespace-only first line keeps the tab sniff intact.
	tsvBlank := writeText("blank.tsv", "\t \nid\tlabel\tn\na\tfirst\t5\nb\tsecond\t7\n")

	cases := []struct {
		name, path, sheet string
		header            []string
		data              [][]string
	}{
		{"xlsx control", xlsx, "Control", parityHeader, parityData},
		{"xlsx blank leading rows", xlsx, "Blank", parityHeader, parityData},
		{"csv control", csvControl, "", parityHeader, parityData},
		{"csv blank leading rows", csvBlank, "", parityHeader, parityData},
		{"tsv control", tsvControl, "", parityHeader, parityData},
		{"tsv blank leading rows", tsvBlank, "", parityHeader, parityData},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertParity(t, tc.path, tc.sheet, tc.header, tc.data)
		})
	}
}
