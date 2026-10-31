// Package sheet implements spreadsheet type inference: it profiles XLSX/CSV
// workbooks into per-sheet SheetProfile structures (column types, affinities,
// min/max, distinct values and sample rows) and the SQLite DDL that matches
// them. It is a leaf package — it must not import other project packages —
// so both internal/indexer (document indexing) and internal/tool
// (query_sheet) can consume it without cycles.
package sheet

import (
	"encoding/binary"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/xuri/excelize/v2"
)

// This file turns spreadsheet files into profiled, page-sized text the doc
// index can label. One sheet is one page; the page text is a profile of the
// sheet — name, dimensions, per-column inferred type, min/max, the distinct
// values of low-cardinality columns and a handful of sample rows. query_sheet
// (internal/tool) consumes the same SheetProfile, so the indexed profile and
// the SQL the tool runs can never disagree.

// scanRowCap is how many data rows get the full per-cell classification. Rows
// past the cap still count toward Total; see strideNext for the hardening
// sample.
const scanRowCap = 1000

// The hardening stride: rows 2000, 4000, 8000, ... fold into the same column
// vote as the capped scan, deterministically and without a flag, so a column
// that turns text deep in the file is still caught.
const strideStart = 2000

// sampleRowCount is how many leading data rows are carried in the profile.
const sampleRowCount = 5

// maxDistinctUniverseForList / maxDistinctList: a column keeps its distinct
// values only while there are at most maxDistinctUniverseForList of them; the
// profile then lists the top maxDistinctList by frequency.
const (
	maxDistinctUniverseForList = 12
	maxDistinctList            = 8
)

// sheetExtensions are the extensions the indexer treats as spreadsheets:
// one profiled page per sheet (or the file itself for CSV/TSV). .xls and .ods
// are deliberately out — excelize cannot read BIFF/ODF.
var sheetExtensions = map[string]struct{}{
	".xlsx": {}, ".xlsm": {}, ".csv": {}, ".tsv": {},
}

// isSheetExt reports whether ext (e.g. ".xlsx") is handled as a spreadsheet.
func isSheetExt(ext string) bool {
	_, ok := sheetExtensions[strings.ToLower(ext)]
	return ok
}

// cellKind is the type inferred for one cell value.
type cellKind int

const (
	kindEmpty cellKind = iota
	kindBoolean
	kindInteger
	kindNumber
	kindDate
	kindText
)

// String returns the contract name of the kind as it lands in SheetColumn.Type.
func (k cellKind) String() string {
	switch k {
	case kindEmpty:
		return "empty"
	case kindBoolean:
		return "boolean"
	case kindInteger:
		return "integer"
	case kindNumber:
		return "number"
	case kindDate:
		return "date"
	default:
		return "text"
	}
}

// affinityFor maps an inferred kind to the SQLite column affinity the
// CREATE TABLE in SheetProfile.SQL carries: integer→INTEGER, number→REAL,
// everything else→TEXT.
func affinityFor(k cellKind) string {
	switch k {
	case kindInteger:
		return "INTEGER"
	case kindNumber:
		return "REAL"
	default:
		return "TEXT"
	}
}

// dateLayouts are the fixed non-ISO formats the classifier accepts. Dates
// come out normalized (out layout) and are compared later as ISO strings,
// which sort chronologically.
var dateLayouts = []struct {
	in, out string
}{
	{"2006-01-02", "2006-01-02"},
	{"2006-01-02T15:04", "2006-01-02T15:04"},
	{"01/02/2006", "2006-01-02"},
	{"02 Jan 2006", "2006-01-02"},
	{"15:04", "15:04"},
}

var (
	intRe     = regexp.MustCompile(`^[+-]?[0-9]+$`)
	isoDateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}(T\d{2}:\d{2})?$`)
)

// cellClass is the classification of one cell plus everything the column
// accumulators need from it.
type cellClass struct {
	kind    cellKind
	display string // normalized value as shown in distinct lists and samples
	num     float64
	intVal  int64
	iso     string // normalized date value when kind is kindDate
	serial  bool   // classified as a date through the Excel serial-date rule
}

// classifyCell runs the per-cell classifier — candidates in precedence order,
// first match wins. declaredBool is excelize's GetCellType verdict for the
// cell (xlsx only): a tie-breaker for raw "0"/"1" cells, never an override.
// formatDate marks a raw number whose number format carries date codes; it
// promotes the cell to an ISO date via the Excel 1900 epoch. Empty cells
// never vote.
func classifyCell(value string, declaredBool, formatDate bool) cellClass {
	v := strings.TrimSpace(value)
	if v == "" {
		return cellClass{kind: kindEmpty}
	}
	if declaredBool || strings.EqualFold(v, "true") || strings.EqualFold(v, "false") {
		if b, err := strconv.ParseBool(v); err == nil {
			if b {
				return cellClass{kind: kindBoolean, display: "TRUE"}
			}
			return cellClass{kind: kindBoolean, display: "FALSE"}
		}
		if declaredBool {
			return cellClass{kind: kindBoolean, display: "TRUE"} // declared bool with an unparseable raw value: it is set
		}
	}
	// The serial-date rule fires before the numeric candidates: a date cell
	// read raw is "45000", which would otherwise land as integer.
	if formatDate {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			iso := excelSerialToISO(f)
			return cellClass{kind: kindDate, display: iso, iso: iso, serial: true}
		}
	}
	if intRe.MatchString(v) {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return cellClass{kind: kindInteger, display: v, num: float64(n), intVal: n}
		}
	}
	if !isoDateRe.MatchString(v) {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return cellClass{kind: kindNumber, display: v, num: f}
		}
	}
	for _, l := range dateLayouts {
		if t, err := time.Parse(l.in, v); err == nil {
			iso := t.Format(l.out)
			return cellClass{kind: kindDate, display: iso, iso: iso}
		}
	}
	return cellClass{kind: kindText, display: v}
}

// excelSerialToISO converts an Excel 1900-epoch serial to ISO-8601. Excel
// counts a phantom 1900-02-29, so serials ≥ 60 count days from 1899-12-30
// and serials 1..59 from 1899-12-31; the fraction becomes a time-of-day.
func excelSerialToISO(serial float64) string {
	days := int64(math.Floor(serial))
	frac := serial - math.Floor(serial)
	var t time.Time
	if serial < 60 {
		t = time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC)
	} else {
		t = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	}
	t = t.AddDate(0, 0, int(days))
	if frac > 0 {
		t = t.Add(time.Duration(frac * float64(24*time.Hour)))
		return t.Format("2006-01-02T15:04")
	}
	return t.Format("2006-01-02")
}

// columnAccum folds the cells of one column into a vote.
type columnAccum struct {
	votes map[cellKind]int

	sawDateFmt bool // some cell classified via the serial-date rule

	hasInt bool
	intMin int64
	intMax int64

	hasNum bool
	numMin float64
	numMax float64

	hasDate bool
	dateMin string
	dateMax string

	hasStr bool
	strMin string
	strMax string

	distinct         map[string]int
	distinctOrder    []string
	distinctOverflow bool
}

func newColumnAccum() *columnAccum {
	return &columnAccum{votes: make(map[cellKind]int)}
}

// note folds one classified cell in. Empty cells never reach here — feed
// skips them before calling, and note guards anyway.
func (c *columnAccum) note(cl cellClass) {
	if cl.kind == kindEmpty {
		return
	}
	if cl.serial {
		c.sawDateFmt = true
	}
	c.votes[cl.kind]++
	switch cl.kind {
	case kindInteger:
		if !c.hasInt {
			c.hasInt = true
			c.intMin, c.intMax = cl.intVal, cl.intVal
		} else {
			if cl.intVal < c.intMin {
				c.intMin = cl.intVal
			}
			if cl.intVal > c.intMax {
				c.intMax = cl.intVal
			}
		}
	case kindNumber:
		if !c.hasNum {
			c.hasNum = true
			c.numMin, c.numMax = cl.num, cl.num
		} else {
			if cl.num < c.numMin {
				c.numMin = cl.num
			}
			if cl.num > c.numMax {
				c.numMax = cl.num
			}
		}
	case kindDate:
		if !c.hasDate {
			c.hasDate = true
			c.dateMin, c.dateMax = cl.iso, cl.iso
		} else {
			// ISO strings compare chronologically.
			if cl.iso < c.dateMin {
				c.dateMin = cl.iso
			}
			if cl.iso > c.dateMax {
				c.dateMax = cl.iso
			}
		}
	}
	// Lexical extrema from every non-empty value: a column the lattice resolves
	// to text (widening, suspicious date+number mixes) or boolean reports
	// extrema across all its display strings, whatever the individual kinds were.
	d := cl.display
	if !c.hasStr {
		c.hasStr = true
		c.strMin, c.strMax = d, d
	} else {
		if d < c.strMin {
			c.strMin = d
		}
		if d > c.strMax {
			c.strMax = d
		}
	}
	// Distinct values: count while the universe stays small, then give up.
	if !c.distinctOverflow {
		if c.distinct == nil {
			c.distinct = make(map[string]int)
		}
		if _, ok := c.distinct[cl.display]; !ok {
			c.distinctOrder = append(c.distinctOrder, cl.display)
			if len(c.distinctOrder) > maxDistinctUniverseForList {
				c.distinctOverflow = true
				c.distinct = nil
				c.distinctOrder = nil
				return
			}
		}
		c.distinct[cl.display]++
	}
}

// resolve applies the compatibility lattice — not a naive majority:
//
//	all one type            → that type
//	int + number mix        → number
//	anything + text         → text   (the widening rule)
//	date + int/number without the number-format signal → text (suspicious, don't guess)
//	date + int/number with the signal                  → date
//	all empty (no votes)    → empty
func (c *columnAccum) resolve() cellKind {
	if len(c.votes) == 0 {
		return kindEmpty
	}
	if len(c.votes) == 1 {
		for k := range c.votes {
			return k
		}
	}
	if c.votes[kindText] > 0 {
		return kindText // one "N/A" flips the whole column, on purpose
	}
	hasInt := c.votes[kindInteger] > 0
	hasNum := c.votes[kindNumber] > 0
	hasDate := c.votes[kindDate] > 0
	if hasInt && hasNum && !hasDate && c.votes[kindBoolean] == 0 {
		return kindNumber
	}
	if hasDate && (hasInt || hasNum) {
		if c.sawDateFmt {
			return kindDate
		}
		return kindText
	}
	return kindText // boolean mixed with anything numeric, date or not
}

// minMaxFor returns the normalized extrema for the resolved kind, or two
// empty strings when that kind saw no values.
func (c *columnAccum) minMaxFor(k cellKind) (string, string) {
	switch k {
	case kindInteger:
		if c.hasInt {
			return strconv.FormatInt(c.intMin, 10), strconv.FormatInt(c.intMax, 10)
		}
	case kindNumber:
		if c.hasNum || c.hasInt {
			mn, mx := c.numericMinMax()
			return formatFloat(mn), formatFloat(mx)
		}
	case kindDate:
		if c.hasDate {
			return c.dateMin, c.dateMax
		}
	case kindBoolean, kindText:
		if c.hasStr {
			return c.strMin, c.strMax
		}
	}
	return "", ""
}

// numericMinMax merges the exact int64 tracker into the float one — resolved
// "number" columns mix both, and the float form is what lands in the profile.
func (c *columnAccum) numericMinMax() (float64, float64) {
	mn, mx := math.Inf(1), math.Inf(-1)
	if c.hasInt {
		mn, mx = float64(c.intMin), float64(c.intMax)
	}
	if c.hasNum {
		if math.IsInf(mn, 1) || c.numMin < mn {
			mn = c.numMin
		}
		if math.IsInf(mx, -1) || c.numMax > mx {
			mx = c.numMax
		}
	}
	return mn, mx
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// distinctTop8 returns the top values by frequency (ties by first seen) when
// the column held at most maxDistinctUniverseForList distinct non-empty
// values; otherwise nil.
func (c *columnAccum) distinctTop8() []string {
	if c.distinctOverflow || len(c.distinct) == 0 {
		return nil
	}
	if len(c.distinct) > maxDistinctUniverseForList {
		return nil
	}
	type dv struct {
		value string
		count int
		pos   int
	}
	list := make([]dv, 0, len(c.distinct))
	for pos, v := range c.distinctOrder {
		list = append(list, dv{value: v, count: c.distinct[v], pos: pos})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].count != list[j].count {
			return list[i].count > list[j].count
		}
		return list[i].pos < list[j].pos
	})
	if len(list) > maxDistinctList {
		list = list[:maxDistinctList]
	}
	out := make([]string, len(list))
	for i, d := range list {
		out[i] = d.value
	}
	return out
}

// sheetScan accumulates one sheet (or one CSV file) column by column. It is
// fed row by row — the sheet is never materialized.
type sheetScan struct {
	cols        int
	accs        []*columnAccum
	rowsScanned int // data rows folded into the vote (≤ cap, plus stride hits)
	total       int // all data rows seen below the header
	nextStride  int
	sample      [][]string
}

func newSheetScan() *sheetScan {
	return &sheetScan{nextStride: strideStart}
}

func (s *sheetScan) acc(i int) *columnAccum {
	for len(s.accs) <= i {
		s.accs = append(s.accs, newColumnAccum())
	}
	return s.accs[i]
}

// feed folds one data row in. idx is the 1-based data-row number (header
// excluded). meta, when non-nil (xlsx), looks up the per-cell workbook facts
// — declared boolean type and number-format date — for one cell.
func (s *sheetScan) feed(idx int, cells []string, meta func(value string, i int) (bool, bool)) {
	s.total++
	if idx > scanRowCap && idx != s.nextStride {
		return
	}
	if idx > scanRowCap {
		s.nextStride *= 2
	}
	s.rowsScanned++
	if len(cells) > s.cols {
		s.cols = len(cells)
	}
	var displays []string
	if len(s.sample) < sampleRowCount {
		displays = make([]string, len(cells))
	}
	for i, v := range cells {
		var declaredBool, formatDate bool
		if meta != nil {
			declaredBool, formatDate = meta(v, i)
		}
		cl := classifyCell(v, declaredBool, formatDate)
		if cl.kind != kindEmpty {
			s.acc(i).note(cl)
		}
		if displays != nil {
			displays[i] = cl.display
		}
	}
	if displays != nil {
		s.sample = append(s.sample, displays)
	}
}

// profile assembles the SheetProfile: headers padded to the widest row seen,
// missing or blank headers named col_N, the lattice resolved per column and
// the CREATE TABLE emitted.
func (s *sheetScan) profile(name string, headers []string) SheetProfile {
	cols := s.cols
	if len(headers) > cols {
		cols = len(headers)
	}
	names := make([]string, cols)
	seen := make(map[string]int, cols)
	for i := 0; i < cols; i++ {
		h := ""
		if i < len(headers) {
			h = strings.TrimSpace(headers[i])
		}
		if h == "" {
			h = fmt.Sprintf("col_%d", i+1)
		}
		seen[h]++
		if n := seen[h]; n > 1 {
			h = fmt.Sprintf("%s_%d", h, n)
		}
		names[i] = h
	}
	columns := make([]SheetColumn, cols)
	for i := 0; i < cols; i++ {
		a := s.acc(i)
		k := a.resolve()
		mn, mx := a.minMaxFor(k)
		columns[i] = SheetColumn{
			Name:     names[i],
			Type:     k.String(),
			Affinity: affinityFor(k),
			Min:      mn,
			Max:      mx,
			Distinct: a.distinctTop8(),
		}
	}
	return SheetProfile{
		Name:    name,
		Rows:    s.rowsScanned,
		Cols:    cols,
		Headers: names,
		Columns: columns,
		SQL:     buildTableSQL(name, columns),
		Total:   s.total,
		Sample:  s.sample,
	}
}

// SheetColumn is the per-column verdict shared with the query_sheet tool:
// Type is the inferred kind ("integer" | "number" | "date" | "boolean" |
// "text" | "empty"), Affinity the SQLite affinity the emitted SQL carries
// (integer→INTEGER; number→REAL; date/text/boolean/empty→TEXT), Min/Max the
// normalized extrema ("" when unset, dates ISO-8601) and Distinct the top ≤8
// values when the column has ≤12 distinct non-empty values, else nil.
type SheetColumn struct {
	Name     string
	Type     string
	Affinity string
	Min      string
	Max      string
	Distinct []string
}

// SheetProfile is the per-sheet contract shared with the query_sheet tool.
// SQL is complete, executable SQLite DDL on a modernc.org/sqlite connection.
// Sample holds up to sampleRowCount leading data rows as normalized display
// values (extra field beyond the contract, for the indexed page text).
type SheetProfile struct {
	Name    string
	Rows    int
	Cols    int
	Headers []string
	Columns []SheetColumn
	SQL     string
	Total   int
	Sample  [][]string
}

// quoteIdent double-quotes a SQLite identifier, doubling embedded quotes.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// maxPlainDelimitedBytes bounds the whole-file read the CSV/TSV path needs:
// the text is decoded into memory in full before parsing, so a bigger file is
// refused with a note rather than decoded (the XLSX path streams row by row
// and has no such bound).
const maxPlainDelimitedBytes = 256 << 20 // 256 MiB

// buildTableSQL emits `CREATE TABLE "Name" ("col" AFFINITY, ...)` — valid
// SQLite DDL. A sheet with no columns at all still gets an executable
// statement: rowid-as-key marks it empty without inventing a fake column.
func buildTableSQL(name string, columns []SheetColumn) string {
	if len(columns) == 0 {
		return fmt.Sprintf(`CREATE TABLE %s ("rowid" INTEGER PRIMARY KEY)`, quoteIdent(name))
	}
	parts := make([]string, len(columns))
	for i, c := range columns {
		parts[i] = quoteIdent(c.Name) + " " + c.Affinity
	}
	return fmt.Sprintf("CREATE TABLE %s (%s)", quoteIdent(name), strings.Join(parts, ", "))
}

// ProfileXLSX profiles every sheet of an xlsx/xlsm workbook — one profile per
// sheet. Sheets stream through the row iterator (never materialized); the
// number-format date signal is looked up per style id and cached.
func ProfileXLSX(path string) ([]SheetProfile, error) {
	f, err := excelize.OpenFile(path, excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck
	sheets := f.GetSheetList()
	profiles := make([]SheetProfile, 0, len(sheets))
	for _, name := range sheets {
		p, err := profileXLSXSheet(f, name)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, p)
	}
	return profiles, nil
}

func profileXLSXSheet(f *excelize.File, sheet string) (SheetProfile, error) {
	rows, err := f.Rows(sheet)
	if err != nil {
		return SheetProfile{}, fmt.Errorf("open sheet %s: %w", sheet, err)
	}
	ctx := &xlsxCtx{f: f, sheet: sheet, fmtCache: make(map[int]bool)}
	scan := newSheetScan()
	gate := headerGate{}
	rowNo := 0
	for rows.Next() {
		rowNo++
		cells, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			return SheetProfile{}, fmt.Errorf("read sheet %s: %w", sheet, err)
		}
		if gate.skip(cells) {
			continue // blank leading rows, then the header row the profiler picked
		}
		r := rowNo // capture for the meta closure
		scan.feed(rowNo-1, cells, func(value string, i int) (bool, bool) {
			return ctx.meta(r, i, value)
		})
	}
	if err := rows.Close(); err != nil {
		return SheetProfile{}, fmt.Errorf("close sheet %s: %w", sheet, err)
	}
	return scan.profile(sheet, gate.Headers), nil
}

// xlsxCtx caches the expensive per-cell lookups across a sheet's scan.
type xlsxCtx struct {
	f        *excelize.File
	sheet    string
	fmtCache map[int]bool // styleID → number format carries date codes
}

// meta reports the workbook facts for one cell: whether it is declared a
// boolean, and whether its number format carries date codes (making a raw
// number an Excel serial date).
func (x *xlsxCtx) meta(rowNo, colIdx int, value string) (declaredBool, formatDate bool) {
	col, err := excelize.ColumnNumberToName(colIdx + 1)
	if err != nil {
		return false, false
	}
	axis := col + strconv.Itoa(rowNo)
	// A raw boolean comes out as "0"/"1"; without the declared type it is
	// indistinguishable from integer 1. Lookup only on those values.
	if value == "0" || value == "1" {
		if ct, err := x.f.GetCellType(x.sheet, axis); err == nil && ct == excelize.CellTypeBool {
			declaredBool = true
		}
	}
	if id, err := x.f.GetCellStyle(x.sheet, axis); err == nil {
		formatDate = x.styleIsDate(id)
	}
	return declaredBool, formatDate
}

// styleIsDate reports whether the style's number format carries date codes.
// Builtin date ranges are checked first; custom formats by their string.
func (x *xlsxCtx) styleIsDate(styleID int) bool {
	if d, ok := x.fmtCache[styleID]; ok {
		return d
	}
	d := false
	if st, err := x.f.GetStyle(styleID); err == nil {
		d = builtinNumFmtIsDate(st.NumFmt)
		if !d && st.CustomNumFmt != nil && *st.CustomNumFmt != "" {
			l := strings.ToLower(*st.CustomNumFmt)
			d = strings.Contains(l, "yy") || strings.Contains(l, "mm") || strings.Contains(l, "hh")
		}
	}
	x.fmtCache[styleID] = d
	return d
}

// builtinNumFmtIsDate reports whether a builtin Excel number format id is a
// date or time format.
func builtinNumFmtIsDate(n int) bool {
	return (n >= 14 && n <= 22) || (n >= 27 && n <= 36) || (n >= 45 && n <= 47) || (n >= 50 && n <= 58)
}

// sheetRowHasValue reports whether any cell carries a non-blank value.
func sheetRowHasValue(cells []string) bool {
	for _, v := range cells {
		if strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

// headerGate carries the ONE "where does data start" rule the profiler and
// the streamer must agree on: blank leading rows carry no header and are
// skipped, the first row carrying a value IS the header, and every row after
// it is data. Both paths gate their rows through skip, so the first data row
// they emit is the same row by construction.
type headerGate struct {
	placed  bool
	Headers []string
}

// skip reports whether this candidate row belongs to the header block and so
// must not be fed as data: a blank leading row, or — on the row that ends the
// search — the header itself, which it records.
func (g *headerGate) skip(cells []string) bool {
	if g.placed {
		return false
	}
	if !sheetRowHasValue(cells) {
		return true // a wholly blank leading row carries no header
	}
	g.placed = true
	g.Headers = cells
	return true
}

// openDelimited reads a whole .csv/.tsv file and hands back one csv.Reader
// with the BOM decoded and the delimiter sniffed — the same reader both the
// profiler and the streamer scan, so neither can drift from the other.
func openDelimited(path string) (*csv.Reader, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > maxPlainDelimitedBytes {
		return nil, fmt.Errorf("%s is %d MiB, larger than the %d MiB whole-file read the CSV/TSV reader allows; export it as .xlsx or split it", path, len(data)>>20, maxPlainDelimitedBytes>>20)
	}
	text := decodeDelimitedText(data)
	r := csv.NewReader(strings.NewReader(text))
	r.Comma = sniffDelimiter(text)
	r.FieldsPerRecord = -1 // tolerate ragged files
	r.LazyQuotes = true
	return r, nil
}

// ProfileCSV profiles a delimited-text file: delimiter , ; \t sniffed,
// UTF-8/UTF-16 BOM handled, blank leading rows skipped and the first row
// carrying a value taken as the header (headerGate — the rule streamCSV
// streams past), and exactly one profile named "Sheet1".
func ProfileCSV(path string) ([]SheetProfile, error) {
	r, err := openDelimited(path)
	if err != nil {
		return nil, err
	}
	scan := newSheetScan()
	gate := headerGate{}
	idx := 0
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if gate.skip(rec) {
			continue
		}
		idx++
		scan.feed(idx, rec, nil)
	}
	return []SheetProfile{scan.profile("Sheet1", gate.Headers)}, nil
}

// StreamRows feeds the data rows of one sheet (header row excluded) to feed
// in reading order, one []string per row. Each value arrives as the SAME
// normalized display string the profiler folded into its accumulators — the
// row is run through classifyCell with the workbook's own signals — so a
// table loaded from this stream agrees cell for cell with the profile's
// Sample, Min/Max and Distinct values: Excel serial dates come through as
// ISO-8601 text, declared booleans as TRUE/FALSE, everything else verbatim.
// Delimited text has exactly one sheet ("Sheet1") and its sheet argument is
// ignored. An error from feed aborts the stream and is returned as-is.
func StreamRows(path, sheet string, feed func([]string) error) error {
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".xlsx", ".xlsm":
		return streamXLSX(path, sheet, feed)
	case ".csv", ".tsv":
		return streamCSV(path, feed)
	default:
		return fmt.Errorf("unsupported spreadsheet type %q (StreamRows supports .csv, .tsv, .xlsx, .xlsm)", ext)
	}
}

// streamXLSX streams one sheet's data rows out of an xlsx/xlsm workbook with
// the same raw-cell-value options the profiler opens the file with, so the
// two see identical cell text. Blank leading rows are skipped and the header
// located by headerGate — exactly where the profiler placed it — so the first
// data row is the profiler's first sample row. The meta lookups use the same
// 1-based sheet coordinates the profiler does.
func streamXLSX(path, sheet string, feed func([]string) error) error {
	f, err := excelize.OpenFile(path, excelize.Options{RawCellValue: true})
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck
	if idx, err := f.GetSheetIndex(sheet); err != nil || idx < 0 {
		return fmt.Errorf("unknown sheet %q", sheet)
	}
	rows, err := f.Rows(sheet)
	if err != nil {
		return fmt.Errorf("open sheet %s: %w", sheet, err)
	}
	defer rows.Close() //nolint:errcheck
	ctx := &xlsxCtx{f: f, sheet: sheet, fmtCache: make(map[int]bool)}
	gate := headerGate{}
	rowNo := 0
	for rows.Next() {
		rowNo++
		cells, err := rows.Columns()
		if err != nil {
			return fmt.Errorf("read sheet %s: %w", sheet, err)
		}
		if gate.skip(cells) {
			continue // blank leading rows, then the header row the profiler picked
		}
		r := rowNo // capture for the meta closure
		out := make([]string, len(cells))
		for i, v := range cells {
			declaredBool, formatDate := ctx.meta(r, i, v)
			out[i] = classifyCell(v, declaredBool, formatDate).display
		}
		if err := feed(out); err != nil {
			return err
		}
	}
	// rows.Close rides the defer — its error on a fully-consumed iterator is
	// not a read error, and calling it twice here doubled the failure modes.
	return nil
}

// streamCSV streams a delimited-text file's data rows (header excluded),
// reader built by openDelimited and blank leading rows skipped by headerGate
// — exactly the rule ProfileCSV located the header with.
func streamCSV(path string, feed func([]string) error) error {
	r, err := openDelimited(path)
	if err != nil {
		return err
	}
	gate := headerGate{}
	for {
		rec, err := r.Read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if gate.skip(rec) {
			continue // blank leading rows, then the header row the profiler picked
		}
		out := make([]string, len(rec))
		for i, v := range rec {
			out[i] = classifyCell(v, false, false).display
		}
		if err := feed(out); err != nil {
			return err
		}
	}
}

// decodeDelimitedText strips a UTF-8 or UTF-16 BOM and decodes UTF-16 to
// UTF-8 so the CSV reader sees one encoding.
func decodeDelimitedText(data []byte) string {
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE {
		return decodeUTF16(data[2:], binary.LittleEndian)
	}
	if len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF {
		return decodeUTF16(data[2:], binary.BigEndian)
	}
	s := string(data)
	return strings.TrimPrefix(s, "\ufeff")
}

func decodeUTF16(b []byte, bo binary.ByteOrder) string {
	words := make([]uint16, len(b)/2)
	for i := range words {
		words[i] = bo.Uint16(b[i*2:])
	}
	return string(utf16.Decode(words))
}

// sniffDelimiter picks , ; or \t by frequency on the first line; none found
// means a single-column file and the default comma.
func sniffDelimiter(text string) rune {
	line := text
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSuffix(line, "\r")
	best := ','
	bestCount := 0
	for _, d := range []rune{',', ';', '\t'} {
		if n := strings.Count(line, string(d)); n > bestCount {
			best, bestCount = d, n
		}
	}
	return best
}

// ProfileText renders the profile text a sheet page is indexed from: sheet
// name, dimensions, per-column type/affinity, min/max, distinct values and
// the sample rows.
func ProfileText(p SheetProfile) string {
	var b strings.Builder
	sampled := ""
	if p.Total > p.Rows {
		sampled = fmt.Sprintf(" (profiled from first %d rows)", p.Rows)
	}
	fmt.Fprintf(&b, "Sheet %q: %d data rows%s, %d columns\n", p.Name, p.Total, sampled, p.Cols)
	if len(p.Columns) > 0 {
		b.WriteString("Columns:\n")
		for _, c := range p.Columns {
			fmt.Fprintf(&b, "- %s: %s [%s]", c.Name, c.Type, c.Affinity)
			if c.Min != "" {
				fmt.Fprintf(&b, ", min %s, max %s", c.Min, c.Max)
			}
			if len(c.Distinct) > 0 {
				fmt.Fprintf(&b, ", distinct: %s", strings.Join(c.Distinct, ", "))
			}
			b.WriteString("\n")
		}
	}
	if len(p.Sample) > 0 {
		fmt.Fprintf(&b, "First %d rows:\n", len(p.Sample))
		for _, row := range p.Sample {
			fmt.Fprintf(&b, "| %s\n", strings.Join(row, " | "))
		}
	}
	return b.String()
}
