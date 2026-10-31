package tool

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	_ "modernc.org/sqlite" // the "sqlite" driver for the in-memory sheet database
)

// SheetColumn and SheetProfile mirror the sheet-profiling contract of
// internal/indexer (same field names, same meanings). They are re-declared
// here because internal/tool cannot import internal/indexer — indexer imports
// agent, which imports tool — so the entry-point packages (server, cli) that
// see both convert the profiles across the seam
// (server.SheetProfilesFromIndexer).
type SheetColumn struct {
	Name     string
	Type     string // "integer" | "number" | "date" | "boolean" | "text" | "empty"
	Affinity string // INTEGER | REAL | TEXT
	Min, Max string
	Distinct []string
}

// SheetProfile is one sheet of one workbook: its shape, the inferred column
// profiles, and the DDL (a bare CREATE TABLE) that executes as-is into an
// in-memory SQLite database. The data rows do NOT ride in the SQL — they
// stream through the SheetRowSource the loader hands back alongside the
// profiles, so a workbook of any size never materializes in the tool.
type SheetProfile struct {
	Name    string
	Rows    int
	Cols    int
	Headers []string
	Columns []SheetColumn
	SQL     string
	Total   int // decoded cell bytes, when the profiler reports them

	// Tool-side build state, not part of the profiling contract. TotalRows is
	// the profiler's TRUE data-row count (Rows is the sampling-capped count);
	// Table, Loaded and Truncated are set by buildSheetDatabase.
	TotalRows int
	Table     string
	Loaded    int
	Truncated bool
}

// SheetRowSource streams one sheet's data rows (header row excluded) to feed
// in reading order, one []string per row, each value normalized exactly as
// the profiler normalized it. A nil source is valid: the workbook loads with
// schema only. A feed error aborts the load.
type SheetRowSource func(sheet string, feed func(rows []string) error) error

// sheetProfileLoader turns a spreadsheet path into one profile per sheet
// plus the row source that streams each sheet's data rows for loading.
type sheetProfileLoader func(path string) ([]SheetProfile, SheetRowSource, error)

// globalSheetProfileLoader is the package-wide loader behind the zero-value
// QuerySheetTool. It stays nil until SetSheetProfileLoader is called at tool
// registration; a build that never wires it reports so on the first call
// rather than misbehaving.
var globalSheetProfileLoader sheetProfileLoader

// SetSheetProfileLoader wires the sheet profiler into the query_sheet tool.
// The entry points call it once, right after RegisterCoreTools, because the
// profiler lives in internal/indexer and this package cannot import it.
func SetSheetProfileLoader(loader sheetProfileLoader) {
	globalSheetProfileLoader = loader
}

// defaultSheetProfiles is what an unwired build answers.
func defaultSheetProfiles(path string) ([]SheetProfile, SheetRowSource, error) {
	return nil, nil, fmt.Errorf("query_sheet has no sheet profiler wired in this build")
}

// QuerySheetTool executes a single read-only SQL query against the sheets of one
// spreadsheet file (CSV, TSV, XLSX or XLSM). The tool is the SQL probe behind
// the spreadsheet index: the file is profiled once (one sheet = one SQLite
// table named after the sheet), the DDL runs and the data rows stream from
// the loader's row source into a per-workbook in-memory database, and the
// query runs against it.
//
// Everything is read-only and deterministic: the connection carries
// PRAGMA query_only=ON, the SQL is screened for writes and side-effecting
// statements, each query gets a 3s context timeout, the result is capped
// at 200 rows, and loading a workbook is bounded by a per-sheet row cap and
// a 30s deadline.
type QuerySheetTool struct {
	// profileLoader turns a file path into its sheet profiles and row source.
	// The zero-value tool uses the package loader seam
	// (globalSheetProfileLoader); tests inject hand-built profiles here, so
	// the suite does not depend on the indexer profiler having been wired.
	profileLoader func(path string) ([]SheetProfile, SheetRowSource, error)
	// queryTimeout is the per-query context timeout. Zero means the default.
	queryTimeout time.Duration
	// loadRowCap bounds the rows streamed into one table during the load
	// phase. Zero means the default (sheetLoadRowCap).
	loadRowCap int
}

// sheetQueryTimeout is the context timeout for one query.
const sheetQueryTimeout = 3 * time.Second

// sheetQueryRowCap is the maximum number of result rows returned before the
// tool truncates and tells the agent to add LIMIT/OFFSET.
const sheetQueryRowCap = 200

// sheetLoadRowCap bounds the rows streamed into one table: the load phase is
// not covered by the query timeout, so an uncapped load is where a 1M-row
// sheet would burn minutes on its first query. Rows past the cap stay out of
// the table; the result says so.
const sheetLoadRowCap = 100000

// sheetLoadTimeout bounds one workbook's whole load (DDL + row streaming).
// Failed loads are not cached, so the cap — not this deadline — is the
// ordinary bound; the deadline keeps a pathological load from hanging.
const sheetLoadTimeout = 30 * time.Second

// errSheetRowCapReached ends one sheet's row stream when its load cap lands.
// The streamers return feed errors unwrapped, so the fill path maps it to the
// profile's Truncated flag instead of failing the workbook.
var errSheetRowCapReached = errors.New("sheet row cap reached")

// sheetCacheCap and sheetCacheBytesBudget keep the cache modest: four
// workbooks, sixteen megabytes apiece — sixty-four megabytes total, least
// recently used evicted on overflow.
const (
	sheetCacheCap         = 4
	sheetByteBudgetPerDoc = 16 << 20
)

func (QuerySheetTool) ID() string { return "query_sheet" }

func (QuerySheetTool) Description() string {
	return "Run one read-only SQL query against a spreadsheet file (CSV, TSV, XLSX or XLSM). Every sheet becomes a SQLite table named after the sheet (a CSV is loaded as Sheet1), with column types inferred from the data. Use it for filtering, aggregating, counting or sampling structured data — far cheaper than reading a large file whole. The result lists each sheet's schema, then the query output as CSV, capped at 200 rows."
}

func (QuerySheetTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["file", "sql"],
		"properties": {
			"file": {
				"type": "string",
				"description": "Path to the spreadsheet file (absolute or relative to session directory)"
			},
			"sql": {
				"type": "string",
				"description": "A single read-only SQL statement. Tables are named after the sheets; one CSV file is 'Sheet1'. Writes, ATTACH, PRAGMA and multiple statements are refused."
			}
		}
	}`)
}

// sheetForbiddenSQL matches the write and side-effecting statements the guard
// refuses. Word boundaries keep legitimate reads containing `created_at` or
// `replaced` passing; `REPLACE(` the function is caught, per the guard's rule
// that any write-shaped token is refused rather than parsed.
var sheetForbiddenSQL = regexp.MustCompile(`(?i)\b(ATTACH|DETACH|PRAGMA|VACUUM|INSERT|UPDATE|DELETE|DROP|ALTER|CREATE|REPLACE)\b`)

// findForbiddenSQL returns the first forbidden token in the comment-stripped
// SQL text, or "" when the text is clean. `load_extension` is matched outside
// the word list because its underscore already breaks a word boundary pair.
func findForbiddenSQL(stripped string) string {
	if m := sheetForbiddenSQL.FindString(stripped); m != "" {
		return strings.ToUpper(m)
	}
	if strings.Contains(strings.ToLower(stripped), "load_extension") {
		return "load_extension"
	}
	return ""
}

// stripSQLComments removes `--` line comments and `/* */` block comments so the
// guards look at the statement, not at commentary. Quoted literals are walked
// past (with the ” doubling escape), so a `--` inside a string value does not
// cut the statement in half for the statement counter.
func stripSQLComments(s string) string {
	var b strings.Builder
	inSingle, inDouble, inLine, inBlock := false, false, false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inLine:
			if c == '\n' {
				inLine = false
				b.WriteByte(c)
			}
		case inBlock:
			if c == '*' && i+1 < len(s) && s[i+1] == '/' {
				inBlock = false
				i++
			}
		case inSingle:
			b.WriteByte(c)
			if c == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					b.WriteByte('\'')
					i++
				} else {
					inSingle = false
				}
			}
		case inDouble:
			b.WriteByte(c)
			if c == '"' {
				if i+1 < len(s) && s[i+1] == '"' {
					b.WriteByte('"')
					i++
				} else {
					inDouble = false
				}
			}
		default:
			switch {
			case c == '-' && i+1 < len(s) && s[i+1] == '-':
				inLine = true
				i++
			case c == '/' && i+1 < len(s) && s[i+1] == '*':
				inBlock = true
				i++
			case c == '\'':
				inSingle = true
				b.WriteByte(c)
			case c == '"':
				inDouble = true
				b.WriteByte(c)
			default:
				b.WriteByte(c)
			}
		}
	}
	return b.String()
}

// countSQLStatements reports how many non-empty `;`-terminated segments the
// comment-stripped SQL carries, walking past quoted literals so a `;` inside a
// string value ('a;b') is not counted as a second statement. One call, one
// statement: a second segment is a refuse.
func countSQLStatements(stripped string) int {
	n := 0
	empty := true
	inSingle, inDouble := false, false
	for i := 0; i < len(stripped); i++ {
		c := stripped[i]
		switch {
		case inSingle:
			if c == '\'' {
				if i+1 < len(stripped) && stripped[i+1] == '\'' {
					i++
				} else {
					inSingle = false
				}
			}
		case inDouble:
			if c == '"' {
				if i+1 < len(stripped) && stripped[i+1] == '"' {
					i++
				} else {
					inDouble = false
				}
			}
		case c == '\'':
			inSingle = true
			empty = false
		case c == '"':
			inDouble = true
			empty = false
		case c == ';':
			if !empty {
				n++
				empty = true
			}
		case !unicode.IsSpace(rune(c)):
			empty = false
		}
	}
	if !empty {
		n++
	}
	return n
}

// sheetRefusal renders a guard rejection the way other tools reject bad input:
// a Result with the reason, and no error — the caller saw the message.
func sheetRefusal(output string) (Result, error) {
	return Result{Title: "Query Sheet", Output: output}, nil
}

// buildSheetDatabase creates the in-memory SQLite database for one workbook:
// one connection, every profile's DDL executed verbatim, the data rows
// streamed in through the row source (nil = schema-only tables), and only
// then query_only enforced. The profiles arrive together with their load
// error so both a loader failure and a DDL failure read the same way at the
// call site; tests hand-construct the profiles, which is why the construction
// is split from the file reading. rowCap <= 0 means sheetLoadRowCap.
func buildSheetDatabase(ctx context.Context, profiles []SheetProfile, rows SheetRowSource, profErr error, rowCap int) (*sql.DB, []SheetProfile, error) {
	if profErr != nil {
		return nil, nil, profErr
	}
	if len(profiles) == 0 {
		return nil, nil, fmt.Errorf("no sheets found in the file")
	}
	if rowCap <= 0 {
		rowCap = sheetLoadRowCap
	}
	for i, p := range profiles {
		if strings.TrimSpace(p.SQL) == "" {
			return nil, nil, fmt.Errorf("sheet profile %d (%q) carries no SQL DDL", i, p.Name)
		}
	}

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, nil, fmt.Errorf("open in-memory sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // :memory: lives in one connection; the pool must not open a second, empty one
	db.SetMaxIdleConns(1)

	// The DDL goes in first, then the row data — INSERTs need a writable
	// connection. Once loading is done the single pooled connection is pinned
	// read-only for the queries.
	// Table names resolve before the DDL runs: a case-insensitive collision
	// ("Orders" / "orders") would otherwise fail the second CREATE TABLE and
	// take the whole workbook down with it.
	resolveSheetTableNames(profiles)
	for _, p := range profiles {
		if err := ctx.Err(); err != nil {
			db.Close()
			return nil, nil, err
		}
		if _, err := db.Exec(p.SQL); err != nil {
			db.Close()
			return nil, nil, fmt.Errorf("load sheet %q: %w", p.Name, err)
		}
	}
	if rows != nil {
		if err := fillSheetTables(ctx, db, profiles, rows, rowCap); err != nil {
			db.Close()
			return nil, nil, err
		}
	}
	if _, err := db.Exec("PRAGMA query_only=ON"); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("enforce query_only: %w", err)
	}
	return db, profiles, nil
}

// fillSheetTables streams each profile's data rows into its table through one
// prepared INSERT. Values arrive as the profiler's normalized strings and go
// in as text — SQLite coerces them per the column affinities the DDL already
// declared. Rows are padded to or clipped at the table's column count, so a
// ragged source never fails the load; a columnless (rowid-only) sheet has
// nothing to receive.
//
// The load, unlike the queries, is not covered by the query timeout: a
// per-row ctx check keeps it responsive to cancellation, and rowCap bounds
// the time and memory one sheet can take — rows past the cap stay out of the
// table, with Loaded/Truncated recording the fact for the result text.
func fillSheetTables(ctx context.Context, db *sql.DB, profiles []SheetProfile, rows SheetRowSource, rowCap int) error {
	for pi := range profiles {
		p := &profiles[pi]
		if len(p.Columns) == 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		table := p.Name
		if p.Table != "" {
			table = p.Table
		}
		insert := fmt.Sprintf(`INSERT INTO %s VALUES (%s)`,
			sheetQuoteIdent(table), strings.TrimSuffix(strings.Repeat("?, ", len(p.Columns)), ", "))
		stmt, err := db.Prepare(insert)
		if err != nil {
			return fmt.Errorf("load sheet %q: %w", p.Name, err)
		}
		err = rows(p.Name, func(cells []string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if rowCap > 0 && p.Loaded >= rowCap {
				return errSheetRowCapReached
			}
			vals := make([]any, len(p.Columns))
			for i := range vals {
				if i < len(cells) {
					vals[i] = cells[i]
				} else {
					vals[i] = ""
				}
			}
			if _, err := stmt.Exec(vals...); err != nil {
				return err
			}
			p.Loaded++
			return nil
		})
		if cerr := stmt.Close(); err == nil {
			err = cerr
		}
		if errors.Is(err, errSheetRowCapReached) {
			p.Truncated = true
			continue
		}
		if err != nil {
			return fmt.Errorf("load sheet %q: %w", p.Name, err)
		}
	}
	return nil
}

// sheetQuoteIdent double-quotes a SQLite identifier, doubling embedded
// quotes — the local twin of the profiler's quoteIdent.
func sheetQuoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// resolveSheetTableNames gives each profile its effective SQLite table name,
// in place. SQLite identifiers compare case-insensitively — the DDL for
// "Orders" and the DDL for "orders" collide, and the second CREATE TABLE
// would fail the whole workbook — so a later colliding sheet is renamed with
// a _N suffix. Renaming rewrites the profile's DDL table name and sets
// Table; the schema listing shows the effective name, so the agent always
// queries the name it was shown.
func resolveSheetTableNames(profiles []SheetProfile) {
	seen := make(map[string]bool, len(profiles))
	for i := range profiles {
		p := &profiles[i]
		table := p.Name
		for n := 1; seen[strings.ToLower(table)]; n++ {
			table = fmt.Sprintf("%s_%d", p.Name, n)
		}
		seen[strings.ToLower(table)] = true
		if table == p.Name {
			continue
		}
		if renamed := renameSheetTable(p.SQL, p.Name, table); renamed != "" {
			p.SQL = renamed
			p.Table = table
		}
		// A DDL shape rewriteSheetTable could not touch: both sides keep the
		// original name, and a real collision surfaces as a load error naming
		// the sheet.
	}
}

// renameSheetTable rewrites the quoted table name at the head of the
// profiler's `CREATE TABLE "name" (...)` DDL, or "" when the DDL is not in
// that shape (buildTableSQL's only form).
func renameSheetTable(sqlText, from, to string) string {
	prefix := `CREATE TABLE ` + sheetQuoteIdent(from)
	if !strings.HasPrefix(sqlText, prefix) {
		return ""
	}
	return `CREATE TABLE ` + sheetQuoteIdent(to) + sqlText[len(prefix):]
}

// sheetCacheEntry is one cached workbook database with the identity its
// staleness is judged by.
type sheetCacheEntry struct {
	db       *sql.DB
	profiles []SheetProfile
	modTime  time.Time
	size     int64
	bytes    int64 // an in-memory footprint estimate for the byte budget
	lastUsed time.Time
}

// sheetDatabaseCache caches profiled workbooks keyed by absolute path. An
// entry is valid only while the file's (mtime, size) pair is unchanged — any
// rewrite invalidates it. Eviction is least-recently-used, first past the
// entry cap (sheetCacheCap) and then past the byte budget
// (sheetByteBudgetPerDoc per hit, budgeted as the sum).
type sheetDatabaseCache struct {
	mu      sync.Mutex
	entries map[string]*sheetCacheEntry
	clock   func() time.Time // injectable for LRU tests
}

func newSheetDatabaseCache() *sheetDatabaseCache {
	return &sheetDatabaseCache{entries: make(map[string]*sheetCacheEntry), clock: time.Now}
}

func (c *sheetDatabaseCache) budget() int64 { return sheetCacheCap * sheetByteBudgetPerDoc }

// get returns the cached entry for path when the file still carries the
// (mtime, size) it was built from. A stale or missing entry is dropped.
func (c *sheetDatabaseCache) get(path string, modTime time.Time, size int64) *sheetCacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[path]
	if !ok {
		return nil
	}
	if !e.modTime.Equal(modTime) || e.size != size {
		e.db.Close()
		delete(c.entries, path)
		return nil
	}
	e.lastUsed = c.clock()
	return e
}

// put installs a freshly built entry, evicting least-recently-used entries
// while the cache is past its cap or its byte budget. A workbook whose
// estimate alone busts the budget is not cached: the nil return is the
// caller's signal to close the database itself when the query is done.
func (c *sheetDatabaseCache) put(path string, db *sql.DB, profiles []SheetProfile, modTime time.Time, size int64, bytesEstimate int64) *sheetCacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		if bytesEstimate > sheetByteBudgetPerDoc {
			return nil // one workbook may not exceed its own budget; the caller holds the db
		}
		if old, ok := c.entries[path]; ok {
			old.db.Close()
			delete(c.entries, path)
		}
		total := bytesEstimate
		for _, e := range c.entries {
			total += e.bytes
		}
		if len(c.entries) < sheetCacheCap && total <= c.budget() {
			entry := &sheetCacheEntry{
				db: db, profiles: profiles, modTime: modTime, size: size,
				bytes: bytesEstimate, lastUsed: c.clock(),
			}
			c.entries[path] = entry
			return entry
		}
		if len(c.entries) == 0 {
			return nil // oversized on its own; the caller holds the db
		}
		victim := ""
		var oldest time.Time
		for p, e := range c.entries {
			if victim == "" || e.lastUsed.Before(oldest) {
				victim, oldest = p, e.lastUsed
			}
		}
		c.entries[victim].db.Close()
		delete(c.entries, victim)
	}
}

// sheetProfileBytes estimates a workbook's in-memory footprint. TotalRows
// carries the profiler's true data-row count when it reports one — Rows is
// the sampling-capped count (1000 + strides), so pricing Rows alone let a
// 1M-row workbook estimate ~0.5 MB and slip past its 16 MiB budget. Each
// cell bills a padded 64 bytes in the SQLite btree (record header + rowid +
// page fill, no index) and each sheet 4 KiB of page overhead; the loader's
// Total (decoded bytes, if it reports them) still takes precedence.
func sheetProfileBytes(profiles []SheetProfile) int64 {
	var n int64
	for _, p := range profiles {
		if p.Total > 0 {
			n += int64(p.Total)
			continue
		}
		rows := p.TotalRows
		if rows <= 0 {
			rows = p.Rows
		}
		cols := p.Cols
		if len(p.Columns) > cols {
			cols = len(p.Columns)
		}
		if cols == 0 {
			n += 4096 // a rowid-only sheet is one mostly-empty btree page
			continue
		}
		n += 4096 + int64(rows)*int64(cols)*64
	}
	if n < 1 {
		n = 1
	}
	return n
}

// sheetSchemaLine renders one table's loaded schema: name, column count, and
// the columns with their SQLite affinity — the header the agent reads to know
// the table names and what each holds.
func sheetSchemaLine(p SheetProfile) string {
	parts := make([]string, 0, len(p.Columns))
	for _, c := range p.Columns {
		affinity := c.Affinity
		if affinity == "" {
			affinity = "TEXT"
		}
		parts = append(parts, c.Name+" "+affinity)
	}
	if len(parts) == 0 {
		for _, h := range p.Headers {
			parts = append(parts, h+" TEXT")
		}
	}
	name := p.Name
	if p.Table != "" {
		name = p.Table // the effective table name is what the agent must query
	}
	return fmt.Sprintf("%s: %d cols — %s", name, len(parts), strings.Join(parts, ", "))
}

// sheetSQLValue renders one driver value as CSV cell text. SQL NULL becomes an
// empty cell; everything else prints its Go form.
func sheetSQLValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(t)
	case time.Time:
		return t.UTC().Format(time.RFC3339)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// runSheetQuery executes one query with a context timeout, streams up to
// rowCap rows into compact CSV (header row included), and reports whether the
// cap cut the result short. A timed-out query is an error naming the escape
// hatch (LIMIT/OFFSET), not a truncated result: the partial output of a query
// the engine was still solving is not a sample of anything.
func runSheetQuery(ctx context.Context, db *sql.DB, sqlText string, timeout time.Duration, rowCap int) (csvOut string, rows int, cols int, truncated bool, err error) {
	qctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	rs, err := db.QueryContext(qctx, sqlText)
	if err != nil {
		return "", 0, 0, false, sheetQueryError(err, timeout)
	}
	defer rs.Close()

	names, err := rs.Columns()
	if err != nil {
		return "", 0, 0, false, fmt.Errorf("read result columns: %w", err)
	}
	cols = len(names)

	var buf strings.Builder
	w := csv.NewWriter(&buf)
	if err := w.Write(names); err != nil {
		return "", 0, cols, false, fmt.Errorf("write csv header: %w", err)
	}

	vals := make([]any, cols)
	ptrs := make([]any, cols)
	for rows < rowCap && rs.Next() {
		for i := range ptrs {
			ptrs[i] = &vals[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			return "", rows, cols, false, fmt.Errorf("scan row: %w", err)
		}
		record := make([]string, cols)
		for i, v := range vals {
			record[i] = sheetSQLValue(v)
		}
		if err := w.Write(record); err != nil {
			return "", rows, cols, false, fmt.Errorf("write csv row: %w", err)
		}
		rows++
	}
	if rows == rowCap && rs.Next() {
		truncated = true
	}
	if err := rs.Err(); err != nil {
		return "", rows, cols, truncated, sheetQueryError(err, timeout)
	}
	rs.Close()
	w.Flush()
	if err := w.Error(); err != nil {
		return "", rows, cols, truncated, fmt.Errorf("flush csv: %w", err)
	}
	return buf.String(), rows, cols, truncated, nil
}

// sheetQueryError maps a driver error into an agent-readable error, calling out
// the timeout with its own escape hatch.
func sheetQueryError(err error, timeout time.Duration) error {
	if err == nil {
		return nil
	}
	if qctx := context.DeadlineExceeded; err == qctx || strings.Contains(err.Error(), "context deadline exceeded") || strings.Contains(err.Error(), "interrupted") {
		return fmt.Errorf("query timed out after %s — narrow the scan or add LIMIT/OFFSET", timeout)
	}
	return fmt.Errorf("run query: %w", err)
}

func (t QuerySheetTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var params struct {
		File string `json:"file"`
		SQL  string `json:"sql"`
	}
	if err := DecodeArgs(args, &params); err != nil {
		return Result{}, fmt.Errorf("parse query_sheet args: %w", err)
	}
	if params.File == "" {
		return sheetRefusal("file is required")
	}
	if strings.TrimSpace(params.SQL) == "" {
		return sheetRefusal("sql is required")
	}

	// Guardrails run on the SQL text before anything is built or opened: one
	// statement, no writes, no side-effecting commands.
	stripped := stripSQLComments(params.SQL)
	if countSQLStatements(stripped) == 0 {
		return sheetRefusal("sql rejected: the statement is empty — comments only, no query")
	}
	if token := findForbiddenSQL(stripped); token != "" {
		return sheetRefusal(fmt.Sprintf("sql rejected: %q is not allowed — query_sheet takes read-only SELECT queries only", token))
	}
	if n := countSQLStatements(stripped); n > 1 {
		return sheetRefusal(fmt.Sprintf("sql rejected: one statement per call (found %d) — run the rest as separate query_sheet calls", n))
	}

	path := params.File
	if !filepath.IsAbs(path) {
		path = filepath.Join(tctx.SessionDir, path)
	}

	info, err := os.Stat(path)
	if err != nil {
		return Result{}, fmt.Errorf("open spreadsheet %s: %w", params.File, err)
	}

	loader := t.profileLoader
	if loader == nil {
		loader = globalSheetProfileLoader
		if loader == nil {
			loader = defaultSheetProfiles
		}
	}
	timeout := t.queryTimeout
	if timeout <= 0 {
		timeout = sheetQueryTimeout
	}

	entry := globalSheetCache.get(path, info.ModTime(), info.Size())
	if entry == nil {
		loadCtx, cancelLoad := context.WithTimeout(ctx, sheetLoadTimeout)
		defer cancelLoad()
		profiles, rowSource, perr := loader(path)
		db, profiles, berr := buildSheetDatabase(loadCtx, profiles, rowSource, perr, t.loadRowCap)
		if berr != nil {
			return Result{}, fmt.Errorf("load %s: %w", params.File, berr)
		}
		cached := globalSheetCache.put(
			path, db, profiles, info.ModTime(), info.Size(),
			sheetProfileBytes(profiles),
		)
		if cached == nil { // too big for the cache: still usable for this call
			entry = &sheetCacheEntry{db: db, profiles: profiles}
			defer db.Close()
		} else {
			entry = cached
		}
	}

	// The schema listing covers every sheet the file has — the agent needs the
	// table names as much as the query result.
	var out strings.Builder
	for _, p := range entry.profiles {
		out.WriteString(sheetSchemaLine(p))
		if p.Truncated {
			out.WriteString(fmt.Sprintf("\n  note: only the first %d rows loaded (load cap) — counts and aggregates see the loaded subset", p.Loaded))
		}
		out.WriteByte('\n')
	}

	csvOut, rows, cols, truncated, err := runSheetQuery(ctx, entry.db, params.SQL, timeout, sheetQueryRowCap)
	if err != nil {
		return Result{}, err
	}
	out.WriteByte('\n')
	out.WriteString(csvOut)
	if truncated {
		out.WriteString(fmt.Sprintf("\n… truncated at %d rows — add LIMIT/OFFSET\n", sheetQueryRowCap))
	}
	out.WriteString(fmt.Sprintf("rows: %d · cols: %d", rows, cols))

	return Result{
		Title:  fmt.Sprintf("Query Sheet / %s", filepath.Base(path)),
		Output: out.String(),
	}, nil
}

// globalSheetCache caches workbooks across calls: profiling and DDL loading is
// the expensive part and an agent probes the same file repeatedly
// (schema first, then LIMIT samples, then aggregations).
var globalSheetCache = newSheetDatabaseCache()
