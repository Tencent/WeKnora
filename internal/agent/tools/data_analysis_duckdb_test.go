package tools

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/xuri/excelize/v2"
)

// newTestDuckDB opens an in-memory DuckDB and loads the extensions the
// Data Analysis tool needs. If the extensions can't be installed (e.g. the
// test environment has no network), the test is skipped — we can't validate
// Excel handling without them.
func newTestDuckDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	for _, ext := range []string{"spatial", "excel"} {
		if _, err := db.ExecContext(ctx, fmt.Sprintf("INSTALL %s;", ext)); err != nil {
			t.Skipf("cannot install duckdb %s extension (network required): %v", ext, err)
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf("LOAD %s;", ext)); err != nil {
			t.Skipf("cannot load duckdb %s extension: %v", ext, err)
		}
	}
	return db
}

// writeWorkbook builds a minimal .xlsx with the given sheet -> rows layout.
// Each inner row is a list of cell values; the first row of each sheet is
// treated as the header, matching what read_xlsx expects by default.
func writeWorkbook(t *testing.T, path string, sheets map[string][][]any, sheetOrder []string) {
	t.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	// Start from a clean slate: we'll add our own sheets in the order
	// requested and delete the default Sheet1 at the end if it isn't used.
	for idx, name := range sheetOrder {
		if _, err := f.NewSheet(name); err != nil {
			t.Fatalf("new sheet %q: %v", name, err)
		}
		rows := sheets[name]
		for r, row := range rows {
			for c, val := range row {
				cell, err := excelize.CoordinatesToCellName(c+1, r+1)
				if err != nil {
					t.Fatalf("coord (%d,%d): %v", c, r, err)
				}
				if err := f.SetCellValue(name, cell, val); err != nil {
					t.Fatalf("set cell %s on sheet %q: %v", cell, name, err)
				}
			}
		}
		if idx == 0 {
			sheetIdx, err := f.GetSheetIndex(name)
			if err != nil {
				t.Fatalf("get sheet index %q: %v", name, err)
			}
			f.SetActiveSheet(sheetIdx)
		}
	}

	// Delete the default "Sheet1" that excelize.NewFile() creates, unless
	// the caller explicitly asked for one.
	if _, explicit := sheets["Sheet1"]; !explicit {
		if err := f.DeleteSheet("Sheet1"); err != nil {
			t.Fatalf("delete default Sheet1: %v", err)
		}
	}

	if err := f.SaveAs(path); err != nil {
		t.Fatalf("save workbook: %v", err)
	}
}

// TestLoadFromExcel_MultiSheet is the end-to-end guard for issue #1007:
// given a workbook with 2 sheets of different schemas, every row from every
// sheet must land in the DuckDB table, and the __sheet_name column must
// correctly identify each row's origin.
func TestLoadFromExcel_MultiSheet(t *testing.T) {
	db := newTestDuckDB(t)

	tmp := t.TempDir()
	path := filepath.Join(tmp, "multi_sheet.xlsx")

	writeWorkbook(t, path,
		map[string][][]any{
			"Sales": {
				{"id", "amount"},
				{1, 100},
				{2, 250},
			},
			"Inventory": {
				{"sku", "stock"},
				{"A", 5},
				{"B", 12},
				{"C", 7},
			},
		},
		[]string{"Sales", "Inventory"},
	)

	tool := &DataAnalysisTool{
		BaseTool:  dataAnalysisTool,
		db:        db,
		sessionID: "test-multi-sheet",
	}

	ctx := context.Background()
	schema, err := tool.LoadFromExcel(ctx, path, "t_multi_sheet")
	if err != nil {
		t.Fatalf("LoadFromExcel: %v", err)
	}
	t.Cleanup(func() { tool.Cleanup(ctx) })

	// 2 sheets × (rows - header) = 2 + 3 = 5 rows total.
	if schema.RowCount != 5 {
		t.Fatalf("expected 5 rows total across both sheets, got %d", schema.RowCount)
	}

	// Columns from both sheets plus __sheet_name must all be present.
	colSet := map[string]bool{}
	for _, c := range schema.Columns {
		colSet[c.Name] = true
	}
	for _, want := range []string{"id", "amount", "sku", "stock", excelSheetNameColumn} {
		if !colSet[want] {
			t.Errorf("expected column %q to be present, got columns=%v", want, schema.Columns)
		}
	}

	// Each sheet must contribute its own rows, identifiable via
	// __sheet_name.
	countPerSheet := map[string]int{}
	rows, err := db.QueryContext(ctx,
		fmt.Sprintf("SELECT %s, COUNT(*) FROM \"%s\" GROUP BY %s",
			excelSheetNameColumn, "t_multi_sheet", excelSheetNameColumn),
	)
	if err != nil {
		t.Fatalf("group-by query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var cnt int
		if err := rows.Scan(&name, &cnt); err != nil {
			t.Fatalf("scan: %v", err)
		}
		countPerSheet[name] = cnt
	}
	if rows.Err() != nil {
		t.Fatalf("rows err: %v", rows.Err())
	}

	if countPerSheet["Sales"] != 2 {
		t.Errorf("Sales rows = %d, want 2 (per-sheet breakdown=%v)", countPerSheet["Sales"], countPerSheet)
	}
	if countPerSheet["Inventory"] != 3 {
		t.Errorf("Inventory rows = %d, want 3 (per-sheet breakdown=%v)", countPerSheet["Inventory"], countPerSheet)
	}

	// Cross-sheet schema drift: a column that exists only in "Sales"
	// (amount) must be NULL for rows coming from "Inventory".
	var nullCount int
	err = db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM \"%s\" WHERE %s = 'Inventory' AND amount IS NULL",
			"t_multi_sheet", excelSheetNameColumn),
	).Scan(&nullCount)
	if err != nil {
		t.Fatalf("null-amount query: %v", err)
	}
	if nullCount != 3 {
		t.Errorf("Inventory rows with NULL amount = %d, want 3", nullCount)
	}
}

// TestLoadFromExcel_SingleSheet regression-tests the common single-sheet
// case: no multi-sheet UNION wiring should accidentally break it.
func TestLoadFromExcel_SingleSheet(t *testing.T) {
	db := newTestDuckDB(t)

	tmp := t.TempDir()
	path := filepath.Join(tmp, "single.xlsx")

	writeWorkbook(t, path,
		map[string][][]any{
			"Data": {
				{"name", "value"},
				{"alpha", 1},
				{"beta", 2},
			},
		},
		[]string{"Data"},
	)

	tool := &DataAnalysisTool{
		BaseTool:  dataAnalysisTool,
		db:        db,
		sessionID: "test-single-sheet",
	}

	ctx := context.Background()
	schema, err := tool.LoadFromExcel(ctx, path, "t_single")
	if err != nil {
		t.Fatalf("LoadFromExcel: %v", err)
	}
	t.Cleanup(func() { tool.Cleanup(ctx) })

	if schema.RowCount != 2 {
		t.Fatalf("expected 2 data rows, got %d", schema.RowCount)
	}

	var sheetVal string
	if err := db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT DISTINCT %s FROM \"%s\"", excelSheetNameColumn, "t_single"),
	).Scan(&sheetVal); err != nil {
		t.Fatalf("sheet-name query: %v", err)
	}
	if sheetVal != "Data" {
		t.Errorf("expected all rows tagged with sheet 'Data', got %q", sheetVal)
	}
}

// TestLoadFromExcel_QuotedSheetName makes sure sheet names with single
// quotes survive the SQL round-trip (guards the quote-escaping path).
func TestLoadFromExcel_QuotedSheetName(t *testing.T) {
	db := newTestDuckDB(t)

	tmp := t.TempDir()
	path := filepath.Join(tmp, "quoted.xlsx")

	// Excel disallows some characters in sheet names (e.g. []:*?/) but
	// single quotes are actually permitted; this is the most likely quote
	// bomb for SQL-literal construction.
	sheet := "Q1'24"

	writeWorkbook(t, path,
		map[string][][]any{
			sheet: {
				{"metric", "value"},
				{"revenue", 42},
			},
		},
		[]string{sheet},
	)

	tool := &DataAnalysisTool{
		BaseTool:  dataAnalysisTool,
		db:        db,
		sessionID: "test-quoted",
	}

	ctx := context.Background()
	schema, err := tool.LoadFromExcel(ctx, path, "t_quoted")
	if err != nil {
		t.Fatalf("LoadFromExcel: %v", err)
	}
	t.Cleanup(func() { tool.Cleanup(ctx) })

	if schema.RowCount != 1 {
		t.Fatalf("expected 1 data row, got %d", schema.RowCount)
	}

	var got string
	if err := db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT %s FROM \"%s\" LIMIT 1", excelSheetNameColumn, "t_quoted"),
	).Scan(&got); err != nil {
		t.Fatalf("sheet-name query: %v", err)
	}
	if got != sheet {
		t.Errorf("sheet-name roundtrip mismatch: got %q, want %q", got, sheet)
	}
}
