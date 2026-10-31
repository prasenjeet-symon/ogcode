package server

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/indexer"
	"github.com/prasenjeet-symon/ogcode/internal/sheet"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// SheetProfilesFromIndexer adapts the indexer's sheet profiler to the
// query_sheet loader seam. internal/tool cannot import internal/indexer (the
// cycle tool → indexer → agent → tool), so this package — the only place that
// sees both — converts the profiles across the boundary and hands back the
// row source the tool streams its table rows from (internal/sheet.StreamRows,
// which normalizes cells identically). The cli wires the same function.
func SheetProfilesFromIndexer(path string) ([]tool.SheetProfile, tool.SheetRowSource, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".xlsx", ".xlsm", ".csv", ".tsv":
	default:
		return nil, nil, fmt.Errorf("unsupported spreadsheet type %q (query_sheet supports .csv, .tsv, .xlsx, .xlsm)", ext)
	}
	var (
		profiles []indexer.SheetProfile
		err      error
	)
	switch ext {
	case ".xlsx", ".xlsm":
		profiles, err = indexer.ProfileXLSX(path)
	case ".csv", ".tsv":
		profiles, err = indexer.ProfileCSV(path)
	}
	if err != nil {
		return nil, nil, err
	}
	rows := func(name string, feed func([]string) error) error {
		return sheet.StreamRows(path, name, feed)
	}
	out := make([]tool.SheetProfile, len(profiles))
	for i, p := range profiles {
		tp := tool.SheetProfile{
			Name:    p.Name,
			Rows:    p.Rows,
			Cols:    p.Cols,
			Headers: p.Headers,
			SQL:     p.SQL,
			// NOTE: Total is NOT copied here — the sheet profiler's Total is a
			// data-row count, and the tool's Total field means bytes; copying it
			// would feed a ~rows-sized number into a byte budget. The true row
			// count rides TotalRows instead, which the tool's estimator bills.
			TotalRows: p.Total,
		}
		if len(p.Columns) > 0 {
			tp.Columns = make([]tool.SheetColumn, len(p.Columns))
			for j, c := range p.Columns {
				tp.Columns[j] = tool.SheetColumn{
					Name:     c.Name,
					Type:     c.Type,
					Affinity: c.Affinity,
					Min:      c.Min,
					Max:      c.Max,
					Distinct: c.Distinct,
				}
			}
		}
		out[i] = tp
	}
	return out, rows, nil
}
