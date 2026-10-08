package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestDataAnalysisRegistryRetriesFailedCSVLoad(t *testing.T) {
	ctx := context.Background()
	knowledge := &types.Knowledge{ID: "retry-doc", FileType: "csv", FilePath: "retry.csv"}
	files := &csvFileService{files: map[string]string{
		knowledge.FilePath: "id,amount\n1,\xff\n",
	}}
	tool := NewDataAnalysisTool(nil,
		&mapKnowledgeService{docs: map[string]*types.Knowledge{knowledge.ID: knowledge}},
		nil, files, newPlainDuckDB(t), "test-registry-load-retry")
	registry := NewToolRegistry()
	registry.RegisterTool(tool)
	t.Cleanup(func() { tool.Cleanup(ctx) })
	args, err := json.Marshal(DataAnalysisInput{
		KnowledgeID: knowledge.ID,
		SQL:         `SELECT COUNT(*) AS total FROM dataset WHERE amount = '20'`,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := registry.ExecuteTool(ctx, ToolDataAnalysis, args)
	if err == nil || result == nil || result.Success ||
		!strings.Contains(err.Error(), "failed to create table from CSV") {
		t.Fatalf("first call must reach CREATE and fail: result=%+v err=%v", result, err)
	}

	files.files[knowledge.FilePath] = "id,amount\n1,10\n2,20\n"
	result, err = registry.ExecuteTool(ctx, ToolDataAnalysis, args)
	if err != nil || result == nil || !result.Success {
		t.Fatalf("retry must load the repaired source: result=%+v err=%v", result, err)
	}
	if !strings.Contains(result.Output, `"total":"1"`) {
		t.Fatalf("unexpected query output: %s", result.Output)
	}
}

func TestLoadFromExcelRetriesFailedCreate(t *testing.T) {
	ctx := context.Background()
	tool := &DataAnalysisTool{db: newTestDuckDB(t), sessionID: "test-excel-load-retry"}
	t.Cleanup(func() { tool.Cleanup(ctx) })
	path := filepath.Join(t.TempDir(), "retry.xlsx")

	if _, err := tool.LoadFromExcel(ctx, path, "excel_retry"); err == nil ||
		!strings.Contains(err.Error(), "failed to create table from Excel") {
		t.Fatalf("first call must fail during CREATE: %v", err)
	}
	writeWorkbook(t, path, map[string][][]any{
		"Sales": {{"id", "amount"}, {1, 10}, {2, 20}},
	}, []string{"Sales"})
	schema, err := tool.LoadFromExcel(ctx, path, "excel_retry")
	if err != nil {
		t.Fatalf("retry must load the available workbook: %v", err)
	}
	if schema.RowCount != 2 {
		t.Fatalf("row count = %d, want 2", schema.RowCount)
	}
}

func TestDataAnalysisLoadRecoveryPreservesReuseAndCleanup(t *testing.T) {
	for _, fileType := range []string{"csv", "xlsx"} {
		t.Run(fileType, func(t *testing.T) {
			ctx := context.Background()
			tool := &DataAnalysisTool{sessionID: "test-load-lifecycle"}
			path := filepath.Join(t.TempDir(), "data."+fileType)
			var load func(context.Context, string, string) (*TableSchema, error)
			var write func(bool)
			if fileType == "csv" {
				tool.db = newPlainDuckDB(t)
				load = tool.LoadFromCSV
				write = func(extraRow bool) {
					content := "id,amount\n1,10\n2,20\n"
					if extraRow {
						content += "3,30\n"
					}
					if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				tool.db = newTestDuckDB(t)
				load = tool.LoadFromExcel
				write = func(extraRow bool) {
					rows := [][]any{{"id", "amount"}, {1, 10}, {2, 20}}
					if extraRow {
						rows = append(rows, []any{3, 30})
					}
					writeWorkbook(t, path, map[string][][]any{"Sales": rows}, []string{"Sales"})
				}
			}
			t.Cleanup(func() { tool.Cleanup(ctx) })
			write(false)
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := load(canceled, path, "recovering"); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled CREATE must preserve cancellation: %v", err)
			}
			schema, err := load(ctx, path, "recovering")
			if err != nil || schema.RowCount != 2 {
				t.Fatalf("retry after cancellation: schema=%+v err=%v", schema, err)
			}

			// A different table's failed CREATE must not invalidate a ready table.
			if _, err := load(canceled, path, "other"); !errors.Is(err, context.Canceled) {
				t.Fatalf("other table cancellation: %v", err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if _, err := load(canceled, path, "recovering"); !errors.Is(err, context.Canceled) ||
				!strings.Contains(err.Error(), "failed to get table schema") {
				t.Fatalf("reading a ready table must preserve cancellation: %v", err)
			}
			schema, err = load(ctx, path, "recovering")
			if err != nil || schema.RowCount != 2 {
				t.Fatalf("ready table must be reused with its source missing: schema=%+v err=%v", schema, err)
			}

			tool.Cleanup(ctx)
			if _, err := tool.LoadFromTable(ctx, "recovering"); err == nil {
				t.Fatal("cleanup must remove the loaded table")
			}
			tool.Cleanup(ctx)
			write(true)
			schema, err = load(ctx, path, "recovering")
			if err != nil || schema.RowCount != 3 {
				t.Fatalf("load after cleanup must use fresh data: schema=%+v err=%v", schema, err)
			}
		})
	}
}

func TestDataAnalysisLoadRetainsReadyTableAfterSchemaError(t *testing.T) {
	// Inject a database read failure after a successful CREATE. Real DuckDB
	// tests above cover loading; this seam makes the failure timing deterministic.
	for _, format := range []string{"csv", "xlsx"} {
		t.Run(format, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				mock.ExpectClose()
				if err := db.Close(); err != nil {
					t.Errorf("close test database: %v", err)
				}
			})
			tool := &DataAnalysisTool{db: db, sessionID: "test-schema-read-retry"}
			load := tool.LoadFromCSV
			path := "source." + format
			if format == "xlsx" {
				load = tool.LoadFromExcel
				mock.ExpectQuery(`SELECT UNNEST\(layers\).name FROM st_read_meta`).WillReturnRows(
					sqlmock.NewRows([]string{"name"}).AddRow("Sales"))
			}
			readErr := errors.New("temporary schema read failure")
			mock.ExpectExec(`CREATE TABLE "ready" AS SELECT`).WillReturnResult(sqlmock.NewResult(0, 2))
			mock.ExpectQuery(`DESCRIBE "ready"`).WillReturnError(readErr)
			if _, err := load(context.Background(), path, "ready"); !errors.Is(err, readErr) {
				t.Fatalf("first load must return the read error: %v", err)
			}
			// No second CREATE (or DROP): the first one already succeeded.
			mock.ExpectQuery(`DESCRIBE "ready"`).WillReturnRows(
				sqlmock.NewRows([]string{"name", "type", "nullable"}).AddRow("id", "VARCHAR", "YES"))
			mock.ExpectQuery(`SELECT COUNT\(\*\) FROM "ready"`).WillReturnRows(
				sqlmock.NewRows([]string{"count"}).AddRow(2))
			schema, err := load(context.Background(), path, "ready")
			if err != nil || schema.RowCount != 2 {
				t.Fatalf("retry should read the existing table: schema=%+v err=%v", schema, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDataAnalysisCleanupIncludesFailedCreate(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		mock.ExpectClose()
		if err := db.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	tool := &DataAnalysisTool{db: db, sessionID: "test-failed-create-cleanup"}
	createErr := errors.New("creation failed")
	mock.ExpectExec(`CREATE TABLE "uncertain" AS SELECT`).WillReturnError(createErr)
	if _, err := tool.LoadFromCSV(context.Background(), "source.csv", "uncertain"); !errors.Is(err, createErr) {
		t.Fatalf("load must preserve the CREATE error: %v", err)
	}
	mock.ExpectExec(`DROP TABLE IF EXISTS "uncertain"`).WillReturnResult(sqlmock.NewResult(0, 0))
	tool.Cleanup(context.Background())
	tool.Cleanup(context.Background())
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
