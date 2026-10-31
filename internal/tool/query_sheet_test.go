package tool

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sheetFixtureProfiles is the hand-built workbook behind most tests: DDL
// only, matching the real profiler contract. The data rows stream in through
// sheetFixtureRows via the loader's row source — which doubles as the probe
// that the fill path, not embedded INSERTs, populates the tables.
func sheetFixtureProfiles() []SheetProfile {
	return []SheetProfile{{
		Name:    "Sheet1",
		Rows:    3,
		Cols:    3,
		Headers: []string{"id", "name", "joined"},
		Columns: []SheetColumn{
			{Name: "id", Type: "integer", Affinity: "INTEGER"},
			{Name: "name", Type: "text", Affinity: "TEXT"},
			{Name: "joined", Type: "date", Affinity: "TEXT"},
		},
		SQL:   `CREATE TABLE "Sheet1" ("id" INTEGER, "name" TEXT, "joined" TEXT);`,
		Total: 3 * 3 * 24,
	}}
}

// sheetFixtureRows is the row source data behind sheetFixtureProfiles (and
// the Orders sheet of the multi-sheet fixture), keyed by sheet name.
func sheetFixtureRows() map[string][][]string {
	return map[string][][]string{
		"Sheet1": {{"1", "Alice", "2026-01-02"}, {"2", "Bob", "2026-02-03"}, {"3", "Cara", "2026-03-04"}},
		"Orders": {{"A-1", "5"}, {"B-2", "3"}},
	}
}

// sheetRowSourceFromMaterialized adapts a materialized name→rows map into the
// streaming seam — the shape a loader over a real file provides.
func sheetRowSourceFromMaterialized(rows map[string][][]string) SheetRowSource {
	return func(sheet string, feed func([]string) error) error {
		for _, rec := range rows[sheet] {
			if err := feed(rec); err != nil {
				return err
			}
		}
		return nil
	}
}

// sheetQueryArgs assembles the tool's JSON arguments the way an agent's call
// reaches Execute.
func sheetQueryArgs(t *testing.T, file, sqlText string) json.RawMessage {
	t.Helper()
	args, err := json.Marshal(struct {
		File string `json:"file"`
		SQL  string `json:"sql"`
	}{File: file, SQL: sqlText})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return args
}

// runQuerySheet executes the tool against a fixture-backed workbook and
// returns the Result for assertions.
func runQuerySheet(t *testing.T, qt *QuerySheetTool, file, sqlText string) (Result, error) {
	t.Helper()
	return qt.Execute(context.Background(), sheetQueryArgs(t, file, sqlText), Context{SessionDir: t.TempDir()})
}

// sheetFixtureFile exists so os.Stat (which Execute calls before consulting
// the cache) finds something; the injected loader ignores its contents.
func sheetFixtureFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("placeholder\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func sheetToolFor(profiles []SheetProfile) *QuerySheetTool {
	rows := sheetFixtureRows()
	return &QuerySheetTool{profileLoader: func(string) ([]SheetProfile, SheetRowSource, error) {
		return profiles, sheetRowSourceFromMaterialized(rows), nil
	}}
}

func TestQuerySheetSchemaAndRows(t *testing.T) {
	file := sheetFixtureFile(t, "people.csv")
	res, err := runQuerySheet(t, sheetToolFor(sheetFixtureProfiles()), file, "SELECT * FROM Sheet1 ORDER BY id")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := "Sheet1: 3 cols — id INTEGER, name TEXT, joined TEXT"
	if !strings.Contains(res.Output, want) {
		t.Errorf("output missing schema line %q:\n%s", want, res.Output)
	}
	if !strings.Contains(res.Output, "id,name,joined") ||
		!strings.Contains(res.Output, "1,Alice,2026-01-02") ||
		!strings.Contains(res.Output, "3,Cara,2026-03-04") {
		t.Errorf("output missing csv rows:\n%s", res.Output)
	}
	if !strings.HasSuffix(res.Output, "rows: 3 · cols: 3") {
		t.Errorf("output should end with the row/col count:\n%s", res.Output)
	}
	if res.Title == "" {
		t.Error("expected a non-empty Result title")
	}
}

func TestQuerySheetRowCapAndTruncationHint(t *testing.T) {
	file := sheetFixtureFile(t, "wide.csv")
	profiles := []SheetProfile{{
		Name:    "Sheet1",
		Rows:    500,
		Cols:    1,
		Headers: []string{"n"},
		Columns: []SheetColumn{{Name: "n", Type: "integer", Affinity: "INTEGER"}},
		SQL: `CREATE TABLE "Sheet1" ("n" INTEGER);` +
			`WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n < 500) ` +
			`INSERT INTO "Sheet1" SELECT n FROM seq;`,
		Total: 500 * 24,
	}}
	res, err := runQuerySheet(t, sheetToolFor(profiles), file, "SELECT n FROM Sheet1 ORDER BY n")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res.Output, fmt.Sprintf("… truncated at %d rows — add LIMIT/OFFSET", sheetQueryRowCap)) {
		t.Errorf("output missing truncation hint:\n%s", res.Output)
	}
	if !strings.HasSuffix(res.Output, fmt.Sprintf("rows: %d · cols: 1", sheetQueryRowCap)) {
		t.Errorf("expected exactly the capped row count:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "500") {
		t.Errorf("rows past the cap should not appear:\n%s", res.Output)
	}
}

func TestQuerySheetRejectsForbiddenSQL(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want string // expected first forbidden token, uppercased
	}{
		{"insert", "INSERT INTO Sheet1 VALUES (1)", "INSERT"},
		{"attach", "ATTACH DATABASE 'x' AS y", "ATTACH"},
		{"detach", "DETACH y", "DETACH"},
		{"pragma", "PRAGMA query_only=OFF", "PRAGMA"},
		{"vacuum", "VACUUM", "VACUUM"},
		{"update", "UPDATE Sheet1 SET id = 1", "UPDATE"},
		{"delete", "DELETE FROM Sheet1", "DELETE"},
		{"drop", "DROP TABLE Sheet1", "DROP"},
		{"alter", "ALTER TABLE Sheet1 ADD COLUMN z", "ALTER"},
		{"create", "CREATE TABLE extra (a)", "CREATE"},
		{"replace-into", "REPLACE INTO Sheet1 VALUES (1)", "REPLACE"},
		{"load-extension", "SELECT load_extension('/lib/x.so')", "load_extension"},
		{"benign-like-passes", "SELECT name FROM Sheet1 WHERE name LIKE '%li%'", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := sheetFixtureFile(t, "guard.csv")
			res, err := runQuerySheet(t, sheetToolFor(sheetFixtureProfiles()), file, tc.sql)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if tc.want == "" {
				if !strings.Contains(res.Output, "Alice") || !strings.Contains(res.Output, "rows: 1 · cols: 1") {
					t.Errorf("benign case should flow through to the query, got:\n%s", res.Output)
				}
				return
			}
			if !strings.Contains(res.Output, "not allowed") || !strings.Contains(res.Output, tc.want) {
				t.Errorf("expected a refusal naming %q, got:\n%s", tc.want, res.Output)
			}
		})
	}
}

func TestQuerySheetRejectsTwoStatements(t *testing.T) {
	file := sheetFixtureFile(t, "two.csv")
	res, err := runQuerySheet(t, sheetToolFor(sheetFixtureProfiles()), file, "SELECT * FROM Sheet1; SELECT 2")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res.Output, "one statement per call") {
		t.Errorf("expected a refusal, got:\n%s", res.Output)
	}
}

func TestQuerySheetRejectsEmptySQL(t *testing.T) {
	file := sheetFixtureFile(t, "empty.csv")
	res, err := runQuerySheet(t, sheetToolFor(sheetFixtureProfiles()), file, "-- nothing but a comment")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res.Output, "comments only") {
		t.Errorf("expected a refusal for comment-only sql, got:\n%s", res.Output)
	}
}

// The pragma is set at build time on the workbook's only connection, so a
// write through the same handle must fail even though the tool never opened a
// second path.
func TestQuerySheetDatabaseIsReadOnly(t *testing.T) {
	db, _, err := buildSheetDatabase(context.Background(), sheetFixtureProfiles(), nil, nil, 0)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("INSERT INTO Sheet1 VALUES (9, 'Latecomer', '2026-04-01')"); err == nil {
		t.Error("expected INSERT on a query_only database to fail")
	}
}

func TestStripSQLCommentsAndStatementCount(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
	}{
		{"plain", "SELECT 1", 1},
		{"trailing-semicolon", "SELECT 1;", 1},
		{"line-comment", "SELECT 1 -- hides; more\n", 1},
		{"block-comment", "/* note; here */ SELECT 1", 1},
		{"quoted-semicolon", "SELECT 'a;b'", 1},
		{"two", "SELECT 1; SELECT 2", 2},
		{"two-with-comment", "SELECT 1; -- second\nSELECT 2", 2},
	}
	for _, tc := range cases {
		if n := countSQLStatements(stripSQLComments(tc.in)); n != tc.n {
			t.Errorf("%s: countSQLStatements(%q) = %d, want %d", tc.name, tc.in, n, tc.n)
		}
	}
	if s := stripSQLComments("SELECT 'don''t -- stop' FROM t"); strings.Contains(s, "FROM") && !strings.Contains(s, "don''t -- stop") {
		t.Errorf("quoted literal must survive comment stripping: %q", s)
	}
}

func TestQuerySheetMultiSheetSchemaListing(t *testing.T) {
	file := sheetFixtureFile(t, "book.xlsx")
	profiles := []SheetProfile{
		sheetFixtureProfiles()[0],
		{
			Name:    "Orders",
			Rows:    2,
			Cols:    2,
			Headers: []string{"sku", "qty"},
			Columns: []SheetColumn{
				{Name: "sku", Type: "text", Affinity: "TEXT"},
				{Name: "qty", Type: "integer", Affinity: "INTEGER"},
			},
			SQL:   `CREATE TABLE "Orders" ("sku" TEXT, "qty" INTEGER);`,
			Total: 2 * 2 * 24,
		},
	}
	res, err := runQuerySheet(t, sheetToolFor(profiles), file, "SELECT sku FROM Orders WHERE qty > 3")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res.Output, "Sheet1: 3 cols — id INTEGER, name TEXT, joined TEXT") {
		t.Errorf("schema listing should cover every sheet:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "Orders: 2 cols — sku TEXT, qty INTEGER") {
		t.Errorf("schema listing should cover Orders:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "sku\nA-1") {
		t.Errorf("output missing query rows from Orders:\n%s", res.Output)
	}
}

// sheetUseLoader swaps the package-wide loader seam and hands back the
// restore — the way SetSheetProfileLoader is called at registration.
func sheetUseLoader(loader sheetProfileLoader) func() {
	old := globalSheetProfileLoader
	globalSheetProfileLoader = loader
	if old == nil {
		return func() { globalSheetProfileLoader = nil }
	}
	return func() { globalSheetProfileLoader = old }
}

func TestQuerySheetUnwiredLoaderRefuses(t *testing.T) {
	_, _, err := defaultSheetProfiles("data.json")
	if err == nil || !strings.Contains(err.Error(), "no sheet profiler wired") {
		t.Errorf("expected an unwired-loader error, got %v", err)
	}
}

// --- cache ---

func newFixedClock() (func() time.Time, func()) {
	tick := time.Unix(1_700_000_000, 0)
	return func() time.Time { tick = tick.Add(time.Second); return tick }, func() {}
}

func TestSheetCacheInvalidatesOnMtimeAndSize(t *testing.T) {
	cache := newSheetDatabaseCache()
	db, profiles, err := buildSheetDatabase(context.Background(), sheetFixtureProfiles(), nil, nil, 0)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer db.Close()

	// The two closes: an invalidated get drops the entry (and its db); so does
	// a put replacing the same path. The db here is only for identity, so the
	// double-registration is deliberate test bookkeeping.
	mod := time.Unix(1_700_000_100, 0)
	path := "/fixture/book.xlsx"
	entry := cache.put(path, db, profiles, mod, 1234, 64)
	if entry == nil || cache.get(path, mod, 1234) == nil {
		t.Fatalf("expected a hit for an unchanged file")
	}
	if cache.get(path, mod.Add(time.Second), 1234) != nil {
		t.Error("expected an mtime change to invalidate the entry")
	}
	if cache.get(path, mod, 999) != nil {
		t.Error("expected a size change to invalidate the entry")
	}
}

func TestSheetCacheLRUEviction(t *testing.T) {
	cache := newSheetDatabaseCache()
	clock, _ := newFixedClock()
	cache.clock = clock

	mk := func(name string, rows int) (*sql.DB, []SheetProfile) {
		db, profiles, err := buildSheetDatabase(context.Background(), []SheetProfile{{
			Name: name, Rows: rows, Cols: 0, Headers: []string{"n"},
			SQL: fmt.Sprintf("CREATE TABLE %q (\"n\" INTEGER);", name), Total: rows,
		}}, nil, nil, 0)
		if err != nil {
			t.Fatalf("build %s: %v", name, err)
		}
		return db, profiles
	}
	var dbs []*sql.DB
	defer func() {
		for _, db := range dbs {
			db.Close()
		}
	}()
	for i, name := range []string{"A", "B", "C", "D"} {
		db, profiles := mk(name, 8)
		dbs = append(dbs, db)
		cache.put(fmt.Sprintf("/fixture/%s.xlsx", name), db, profiles, time.Unix(int64(i), 0), 10, 8)
	}
	// Refresh A, then add E: the LRU victim must be B, not A.
	cache.get("/fixture/A.xlsx", time.Unix(0, 0), 10)
	db, profiles := mk("E", 8)
	dbs = append(dbs, db)
	cache.put("/fixture/E.xlsx", db, profiles, time.Unix(9, 0), 10, 8)

	for p := range cache.entries {
		if p == "/fixture/B.xlsx" {
			t.Errorf("B should have been the LRU eviction victim; entries are %v", cache.entries)
		}
	}
	if cache.get("/fixture/A.xlsx", time.Unix(0, 0), 10) == nil {
		t.Error("refreshed A was evicted instead of B")
	}
	if len(cache.entries) != sheetCacheCap {
		t.Errorf("cache holds %d entries, want cap %d", len(cache.entries), sheetCacheCap)
	}
}

func TestSheetCacheByteBudget(t *testing.T) {
	cache := newSheetDatabaseCache()
	db, profiles, err := buildSheetDatabase(context.Background(), []SheetProfile{{
		Name: "Huge", Cols: 0, Headers: []string{"n"},
		SQL: `CREATE TABLE "Huge" ("n" INTEGER);`, Total: sheetByteBudgetPerDoc + 1,
	}}, nil, nil, 0)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer db.Close()
	if entry := cache.put("/fixture/huge.xlsx", db, profiles, time.Unix(0, 0), 10, sheetProfileBytes(profiles)); entry != nil {
		t.Error("a workbook whose estimate alone busts the budget should not be cached")
	}
	if len(cache.entries) != 0 {
		t.Errorf("oversized entry parked itself in the cache: %d entries", len(cache.entries))
	}
}

// --- query mechanics ---

func TestQuerySheetTimeout(t *testing.T) {
	file := sheetFixtureFile(t, "slow.csv")
	qt := sheetToolFor(sheetFixtureProfiles())
	qt.queryTimeout = 50 * time.Millisecond
	_, err := runQuerySheet(t, qt, file,
		"WITH RECURSIVE i(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM i) SELECT sum(x) FROM i")
	if err == nil {
		t.Fatal("expected an unbounded recursive aggregate to hit the timeout")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected a timeout error naming the escape hatch, got: %v", err)
	}
}

func TestQuerySheetCSVShape(t *testing.T) {
	db, _, err := buildSheetDatabase(context.Background(), []SheetProfile{{
		Name: "Sheet1", Rows: 2, Cols: 2, Headers: []string{"label", "note"},
		Columns: []SheetColumn{
			{Name: "label", Type: "text", Affinity: "TEXT"},
			{Name: "note", Type: "text", Affinity: "TEXT"},
		},
		SQL: `CREATE TABLE "Sheet1" ("label" TEXT, "note" TEXT);` +
			`INSERT INTO "Sheet1" VALUES ('a,b', 'say "hi"'), (NULL, 'plain');`,
		Total: 2 * 2 * 24,
	}}, nil, nil, 0)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer db.Close()

	out, rows, cols, truncated, err := runSheetQuery(context.Background(), db, "SELECT label, note FROM Sheet1", sheetQueryTimeout, sheetQueryRowCap)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if rows != 2 || cols != 2 || truncated {
		t.Errorf("rows=%d cols=%d truncated=%v, want 2/2/false", rows, cols, truncated)
	}
	r := csv.NewReader(strings.NewReader(out))
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("csv parse: %v\n%s", err, out)
	}
	if len(records) != 3 {
		t.Fatalf("csv should carry a header plus %d rows, got %d:\n%s", rows, len(records)-1, out)
	}
	want := [][]string{{"label", "note"}, {"a,b", "say \"hi\""}, {"", "plain"}}
	for i, rec := range records {
		if strings.Join(rec, "|") != strings.Join(want[i], "|") {
			t.Errorf("csv row %d = %q, want %q", i, rec, want[i])
		}
	}
}

// --- end to end, through the wired loader seam ---

// sheetProfilesFromFixtureCSV is a stand-in for the indexer's CSV profiler:
// same signature, same contract (CREATE TABLE DDL only, data rows streamed
// through the row source), built from the fixture file so the seam, schema
// listing, fill path and cache round trip are exercised through
// QuerySheetTool.Execute for real. Columns whose every data cell parses as an
// integer get INTEGER affinity, the rest TEXT.
func sheetProfilesFromFixtureCSV(path string) ([]SheetProfile, SheetRowSource, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open spreadsheet %s: %w", path, err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("read spreadsheet %s: %w", path, err)
	}
	if len(rows) == 0 {
		return nil, nil, fmt.Errorf("no sheets found in the file")
	}
	headers := rows[0]
	integer := make([]bool, len(headers))
	for i := range integer {
		integer[i] = true
	}
	for _, row := range rows[1:] {
		for i, cell := range row {
			if _, err := strconv.Atoi(cell); err != nil {
				integer[i] = false
			}
		}
	}
	cols := make([]SheetColumn, len(headers))
	declared := make([]string, len(headers))
	for i, h := range headers {
		affinity := "TEXT"
		if integer[i] {
			affinity = "INTEGER"
		}
		q := `"` + strings.ReplaceAll(h, `"`, `""`) + `"`
		cols[i] = SheetColumn{Name: h, Type: strings.ToLower(affinity), Affinity: affinity}
		declared[i] = q + " " + affinity
	}
	ddl := fmt.Sprintf(`CREATE TABLE "Sheet1" (%s);`, strings.Join(declared, ", "))
	rowSource := func(sheet string, feed func([]string) error) error {
		for _, row := range rows[1:] {
			if err := feed(row); err != nil {
				return err
			}
		}
		return nil
	}
	return []SheetProfile{{
		Name:    "Sheet1",
		Rows:    len(rows) - 1,
		Cols:    len(headers),
		Headers: headers,
		Columns: cols,
		SQL:     ddl,
		Total:   len(rows) * len(headers) * 24,
	}}, rowSource, nil
}

func TestQuerySheetEndToEndCSV(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leads.csv")
	if err := os.WriteFile(path, []byte("id,name,score\n1,Alice,91.5\n2,\"Big, Corp\",72\n3,Cara,87\n"), 0o644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	restore := sheetUseLoader(sheetProfilesFromFixtureCSV)
	defer restore()

	qt := &QuerySheetTool{} // zero-value tool: reads through the package loader seam
	res, err := runQuerySheet(t, qt, path, "SELECT name FROM Sheet1 WHERE id > 2 ORDER BY name")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := res.Output
	if !strings.Contains(out, "Sheet1: 3 cols — id INTEGER, name TEXT, score TEXT") {
		t.Errorf("expected a Sheet1 schema line with the inferred affinity:\n%s", out)
	}
	if !strings.Contains(out, "Cara") || strings.Contains(out, "Alice") {
		t.Errorf("expected only the qualifying row:\n%s", out)
	}
	if !strings.Contains(out, "rows: 1 · cols: 1") {
		t.Errorf("expected one data row, one column:\n%s", out)
	}
	// A second call rides the cache and answers again with fresh output.
	res2, err := runQuerySheet(t, qt, path, "SELECT count(*) FROM Sheet1")
	if err != nil {
		t.Fatalf("cached execute: %v", err)
	}
	if !strings.Contains(res2.Output, "rows: 1") || !strings.Contains(res2.Output, "3") {
		t.Errorf("expected one aggregate row counting the three leads:\n%s", res2.Output)
	}
}

// TestSheetProfileBytesUsesTrueRowCount pins the estimator to the TRUE data
// row count (TotalRows), not the sampling-capped Rows — a 1M-row workbook
// profiled through a 1000-row sample must still cost like 1M rows.
func TestSheetProfileBytesUsesTrueRowCount(t *testing.T) {
	sampled := SheetProfile{Name: "s", Rows: 1000, TotalRows: 1000000, Cols: 10, Columns: make([]SheetColumn, 10)}
	if got := sheetProfileBytes([]SheetProfile{sampled}); got != 4096+1000000*10*64 {
		t.Errorf("estimate for sampled 1M-row sheet = %d, want %d", got, 4096+1000000*10*64)
	}
	noCols := SheetProfile{Name: "s"}
	if got := sheetProfileBytes([]SheetProfile{noCols}); got != 4096 {
		t.Errorf("estimate for header-less sheet = %d, want 4096", got)
	}
}

// TestSheetTableNameCollision pins case-insensitive suffix renaming: two
// sheets named "Orders"/"orders" are the same SQLite identifier, so the
// second gets an _2 suffix and its DDL is rewritten to match.
func TestSheetTableNameCollision(t *testing.T) {
	profiles := []SheetProfile{
		{
			Name: "Orders", Rows: 1, Cols: 2, Headers: []string{"a", "b"},
			Columns: []SheetColumn{{Name: "a", Type: "text", Affinity: "TEXT"}, {Name: "b", Type: "text", Affinity: "TEXT"}},
			SQL:     `CREATE TABLE "Orders" ("a" TEXT, "b" TEXT);`,
		},
		{
			Name: "orders", Rows: 1, Cols: 1, Headers: []string{"a"},
			Columns: []SheetColumn{{Name: "a", Type: "text", Affinity: "TEXT"}},
			SQL:     `CREATE TABLE "orders" ("a" TEXT);`,
		},
	}
	rows := sheetRowSourceFromMaterialized(map[string][][]string{
		"Orders": {{"1", "x"}},
		"orders": {{"2"}},
	})
	db, profiles, err := buildSheetDatabase(context.Background(), profiles, rows, nil, 0)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer db.Close()
	if profiles[1].Table != "orders_1" {
		t.Errorf("colliding sheet table = %q, want \"orders_1\"", profiles[1].Table)
	}
	if profiles[1].SQL != `CREATE TABLE "orders_1" ("a" TEXT);` {
		t.Errorf("colliding sheet DDL not rewritten: %s", profiles[1].SQL)
	}
	for _, check := range []struct {
		table string
		want  int
	}{
		{`"Orders"`, 1}, {`"orders_1"`, 1},
	} {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + check.table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", check.table, err)
		}
		if n != check.want {
			t.Errorf("COUNT(*) FROM %s = %d, want %d", check.table, n, check.want)
		}
	}
}

// TestQuerySheetLoadRowCapTruncates pins the per-sheet load cap: rows past
// the cap never enter the table, and the result carries the truncation note.
func TestQuerySheetLoadRowCapTruncates(t *testing.T) {
	profiles := []SheetProfile{{
		Name: "Big", Rows: 5, Cols: 1, Headers: []string{"n"},
		Columns: []SheetColumn{{Name: "n", Type: "integer", Affinity: "INTEGER"}},
		SQL:     `CREATE TABLE "Big" ("n" INTEGER);`,
	}}
	rows := sheetRowSourceFromMaterialized(map[string][][]string{
		"Big": {{"1"}, {"2"}, {"3"}, {"4"}, {"5"}},
	})
	qt := &QuerySheetTool{profileLoader: func(string) ([]SheetProfile, SheetRowSource, error) {
		return profiles, rows, nil
	}, loadRowCap: 2}
	res, err := runQuerySheet(t, qt, sheetFixtureFile(t, "cap.xlsx"), "SELECT COUNT(*) FROM Big")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res.Output, "load cap") {
		t.Errorf("output missing the load-cap note:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "\n2\n") && !strings.HasSuffix(res.Output, "\n2") {
		t.Errorf("expected COUNT(*) over the capped table to be 2:\n%s", res.Output)
	}
}
