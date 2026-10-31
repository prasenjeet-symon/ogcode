package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

func sheetLoaderFixture(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

const sheetLoaderCSV = "id,name,score\n1,Ann,9.5\n2,Bo,7.0\n3,Cyd,8.0\n"

func TestSheetProfilesFromIndexerConvertsCSV(t *testing.T) {
	path := sheetLoaderFixture(t, "people.csv", sheetLoaderCSV)
	profiles, rowSource, err := SheetProfilesFromIndexer(path)
	if err != nil {
		t.Fatalf("profile csv: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("expected one sheet profile, got %d", len(profiles))
	}
	p := profiles[0]
	if p.Name != "Sheet1" {
		t.Errorf("csv sheet should be Sheet1, got %q", p.Name)
	}
	if p.Cols != 3 || p.Rows != 3 {
		t.Errorf("want 3x3, got %dx%d", p.Rows, p.Cols)
	}
	if !strings.Contains(p.SQL, `CREATE TABLE "Sheet1"`) {
		t.Errorf("SQL must carry executable DDL, got %q", p.SQL)
	}
	if len(p.Columns) != 3 || p.Columns[0].Name != "id" {
		t.Errorf("columns should convert one-for-one, got %+v", p.Columns)
	}
	if rowSource == nil {
		t.Fatal("converter must return a non-nil row source")
	}
	var got [][]string
	if err := rowSource("Sheet1", func(cells []string) error {
		got = append(got, cells)
		return nil
	}); err != nil {
		t.Fatalf("row source: %v", err)
	}
	if len(got) != 3 || len(got[0]) != 3 || got[0][1] != "Ann" || got[2][2] != "8.0" {
		t.Errorf("row source should stream the 3 data rows, got %v", got)
	}
}

// TestSheetProfilesThroughQuerySheet wires the shim into the tool's global
// loader seam and runs a query end to end on a real CSV — the path the server
// boot takes for real spreadsheet files. The profiler ships DDL-only SQL and
// the row data arrives through tool.SheetRowSource (internal/sheet.StreamRows),
// so this pins that profiles and rows agree: the table carries the fixture's
// three data rows and the query answers from them.
func TestSheetProfilesThroughQuerySheet(t *testing.T) {
	path := sheetLoaderFixture(t, "people.csv", sheetLoaderCSV)
	tool.SetSheetProfileLoader(SheetProfilesFromIndexer)
	defer tool.SetSheetProfileLoader(nil)

	args, err := json.Marshal(map[string]any{
		"file": path,
		"sql":  "SELECT name FROM Sheet1 WHERE id = 2 ORDER BY name",
	})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	res, err := tool.QuerySheetTool{}.Execute(context.Background(), args, tool.Context{SessionDir: t.TempDir()})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"Sheet1: 3 cols — id INTEGER, name TEXT, score REAL"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("output missing %q:\n%s", want, res.Output)
		}
	}
	if !strings.Contains(res.Output, "rows: 1 · cols: 1") {
		t.Errorf("expected the streamed rows to load Sheet1 (rows: 1 · cols: 1 footer):\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "Bo") {
		t.Errorf("query for id=2 should answer from loaded row data:\n%s", res.Output)
	}
}

func TestSheetProfilesUnsupportedExtension(t *testing.T) {
	_, _, err := SheetProfilesFromIndexer("whatever.xls")
	if err == nil || !strings.Contains(err.Error(), "unsupported spreadsheet type") {
		t.Errorf("expected unsupported-ext error, got %v", err)
	}
}

func TestSheetProfilesMissingFileError(t *testing.T) {
	if _, _, err := SheetProfilesFromIndexer(filepath.Join(t.TempDir(), "gone.csv")); err == nil {
		t.Error("profiling a missing file should error")
	}
}
