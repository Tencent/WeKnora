package tools

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

func saveTestXLSX(t *testing.T, name string, build func(wb *excelize.File)) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	wb := excelize.NewFile()
	build(wb)
	require.NoError(t, wb.SaveAs(path))
	require.NoError(t, wb.Close())
	return path
}

func schemaColumnNames(schema *TableSchema) []string {
	names := make([]string, 0, len(schema.Columns))
	for _, c := range schema.Columns {
		names = append(names, c.Name)
	}
	return names
}

type diskFileService struct {
	csvFileService
}

func (s *diskFileService) GetFile(_ context.Context, filePath string) (io.ReadCloser, error) {
	return os.Open(filePath)
}

// TestLoadFromExcel_MergedTitleRowEndToEnd reproduces issue #5627 end-to-end
// against real DuckDB: an .xlsx whose row 1 is a cross-column merged banner
// (A1:D1) above the real header on row 2 must expose all real columns to
// schema inspection and support accurate SQL aggregation through Execute.
func TestLoadFromExcel_MergedTitleRowEndToEnd(t *testing.T) {
	db := newTestDuckDB(t)
	xlsxPath := saveTestXLSX(t, "sales_2026.xlsx", func(wb *excelize.File) {
		require.NoError(t, wb.SetCellValue("Sheet1", "A1", "2026 年度销售统计"))
		require.NoError(t, wb.MergeCell("Sheet1", "A1", "D1"))
		rows := [][]any{
			{"月份", "地区", "销售额", "占比"},
			{"1月", "华东", 100, 0.20},
			{"1月", "华南", 150, 0.30},
			{"2月", "华东", 120, 0.24},
			{"2月", "华南", 80, 0.16},
			{"2月", "华北", 50, 0.10},
		}
		for r, row := range rows {
			for c, val := range row {
				cell, err := excelize.CoordinatesToCellName(c+1, r+2)
				require.NoError(t, err)
				require.NoError(t, wb.SetCellValue("Sheet1", cell, val))
			}
		}
	})

	knowledge := &types.Knowledge{
		ID:       "56270000-0000-4000-8000-000000000001",
		FileType: "xlsx",
		FileName: "sales_2026.xlsx",
		FilePath: xlsxPath,
	}
	tool := NewDataAnalysisTool(
		nil,
		&mapKnowledgeService{docs: map[string]*types.Knowledge{knowledge.ID: knowledge}},
		nil,
		&diskFileService{},
		db,
		"test-5627-e2e",
	)
	ctx := context.Background()
	t.Cleanup(func() { tool.Cleanup(ctx) })

	schema, err := tool.LoadFromKnowledge(ctx, knowledge)
	require.NoError(t, err)
	require.Equal(t, []string{"月份", "地区", "销售额", "占比", excelSheetNameColumn}, schemaColumnNames(schema))
	require.EqualValues(t, 5, schema.RowCount)

	args, err := json.Marshal(DataAnalysisInput{
		KnowledgeID: knowledge.ID,
		SQL: `SELECT "地区", ` +
			`SUM(CAST("销售额" AS DOUBLE PRECISION)) AS "销售额总和", ` +
			`ROUND(SUM(CAST("销售额" AS DOUBLE PRECISION)) * 100.0 / ` +
			`SUM(SUM(CAST("销售额" AS DOUBLE PRECISION))) OVER (), 2) AS "占比" ` +
			`FROM dataset GROUP BY "地区" ORDER BY "地区"`,
	})
	require.NoError(t, err)

	res, err := tool.Execute(ctx, args)
	require.NoError(t, err)
	require.True(t, res.Success)
	require.NotContains(t, res.Output, "Consistency Warnings")
	require.Nil(t, res.Data["warnings"])

	gotRows, ok := res.Data["rows"].([]map[string]string)
	require.True(t, ok)
	require.Equal(t, []map[string]string{
		{"地区": "华东", "销售额总和": "220", "占比": "44"},
		{"地区": "华北", "销售额总和": "50", "占比": "10"},
		{"地区": "华南", "销售额总和": "230", "占比": "46"},
	}, gotRows)
}

func TestLoadFromExcel_PreambleLayoutsEndToEnd(t *testing.T) {
	db := newTestDuckDB(t)
	ctx := context.Background()

	cases := []struct {
		name         string
		build        func(wb *excelize.File)
		wantCols     []string
		wantRowCount int64
		checkQuery   string
		wantRows     []map[string]string
	}{
		{
			name: "unmerged title and subtitle with blank row",
			build: func(wb *excelize.File) {
				require.NoError(t, wb.SetCellValue("Sheet1", "A1", "2026 年度销售统计"))
				require.NoError(t, wb.SetCellValue("Sheet1", "A2", "单位：万元，统计口径：含税"))
				// Row 3 intentionally blank
				require.NoError(t, wb.SetCellValue("Sheet1", "A4", "月份"))
				require.NoError(t, wb.SetCellValue("Sheet1", "B4", "地区"))
				require.NoError(t, wb.SetCellValue("Sheet1", "C4", "销售额"))
				require.NoError(t, wb.SetCellValue("Sheet1", "A5", "1月"))
				require.NoError(t, wb.SetCellValue("Sheet1", "B5", "华东"))
				require.NoError(t, wb.SetCellValue("Sheet1", "C5", 100))
				require.NoError(t, wb.SetCellValue("Sheet1", "A6", "2月"))
				require.NoError(t, wb.SetCellValue("Sheet1", "B6", "华南"))
				require.NoError(t, wb.SetCellValue("Sheet1", "C6", 200))
			},
			wantCols:     []string{"月份", "地区", "销售额", excelSheetNameColumn},
			wantRowCount: 2,
		},
		{
			name: "title plus two-corner metadata row",
			build: func(wb *excelize.File) {
				require.NoError(t, wb.SetCellValue("Sheet1", "A1", "2026 年度销售统计"))
				require.NoError(t, wb.MergeCell("Sheet1", "A1", "F1"))
				require.NoError(t, wb.SetCellValue("Sheet1", "A2", "部门：销售部"))
				require.NoError(t, wb.SetCellValue("Sheet1", "F2", "单位：万元"))
				for i, h := range []string{"月份", "大区", "省份", "产品", "销售额", "占比"} {
					c, _ := excelize.CoordinatesToCellName(i+1, 3)
					require.NoError(t, wb.SetCellValue("Sheet1", c, h))
				}
				for i, v := range []any{"1月", "华东", "上海", "A", 100, 1.0} {
					c, _ := excelize.CoordinatesToCellName(i+1, 4)
					require.NoError(t, wb.SetCellValue("Sheet1", c, v))
				}
			},
			wantCols:     []string{"月份", "大区", "省份", "产品", "销售额", "占比", excelSheetNameColumn},
			wantRowCount: 1,
		},
		{
			name: "multi-row merged banner A1:D2",
			build: func(wb *excelize.File) {
				require.NoError(t, wb.SetCellValue("Sheet1", "A1", "2026 年度销售统计"))
				require.NoError(t, wb.MergeCell("Sheet1", "A1", "D2"))
				for i, h := range []string{"月份", "地区", "销售额", "占比"} {
					c, _ := excelize.CoordinatesToCellName(i+1, 3)
					require.NoError(t, wb.SetCellValue("Sheet1", c, h))
				}
				for i, v := range []any{"1月", "华东", 100, 0.5} {
					c, _ := excelize.CoordinatesToCellName(i+1, 4)
					require.NoError(t, wb.SetCellValue("Sheet1", c, v))
				}
			},
			wantCols:     []string{"月份", "地区", "销售额", "占比", excelSheetNameColumn},
			wantRowCount: 1,
		},
		{
			name: "merged banner above numeric year headers",
			build: func(wb *excelize.File) {
				require.NoError(t, wb.SetCellValue("Sheet1", "A1", "2024-2026 销售汇总"))
				require.NoError(t, wb.MergeCell("Sheet1", "A1", "D1"))
				require.NoError(t, wb.SetCellValue("Sheet1", "A2", "地区"))
				require.NoError(t, wb.SetCellValue("Sheet1", "B2", 2024))
				require.NoError(t, wb.SetCellValue("Sheet1", "C2", 2025))
				require.NoError(t, wb.SetCellValue("Sheet1", "D2", 2026))
				require.NoError(t, wb.SetCellValue("Sheet1", "A3", "华东"))
				require.NoError(t, wb.SetCellValue("Sheet1", "B3", 100))
				require.NoError(t, wb.SetCellValue("Sheet1", "C3", 120))
				require.NoError(t, wb.SetCellValue("Sheet1", "D3", 150))
			},
			wantCols:     []string{"地区", "2024", "2025", "2026", excelSheetNameColumn},
			wantRowCount: 1,
		},
		{
			name: "offset table starting at column B",
			build: func(wb *excelize.File) {
				require.NoError(t, wb.SetCellValue("Sheet1", "B2", "销售汇总"))
				require.NoError(t, wb.MergeCell("Sheet1", "B2", "E2"))
				for i, h := range []string{"月份", "地区", "销售额", "占比"} {
					c, _ := excelize.CoordinatesToCellName(i+2, 3)
					require.NoError(t, wb.SetCellValue("Sheet1", c, h))
				}
				for i, v := range []any{"1月", "华东", 100, 1.0} {
					c, _ := excelize.CoordinatesToCellName(i+2, 4)
					require.NoError(t, wb.SetCellValue("Sheet1", c, v))
				}
			},
			wantCols:     []string{"月份", "地区", "销售额", "占比", excelSheetNameColumn},
			wantRowCount: 1,
		},
		{
			name: "merged banner with blank top-left pivot header keeps column A",
			build: func(wb *excelize.File) {
				require.NoError(t, wb.SetCellValue("Sheet1", "A1", "销售矩阵"))
				require.NoError(t, wb.MergeCell("Sheet1", "A1", "D1"))
				require.NoError(t, wb.SetCellValue("Sheet1", "B2", "华东"))
				require.NoError(t, wb.SetCellValue("Sheet1", "C2", "华南"))
				require.NoError(t, wb.SetCellValue("Sheet1", "D2", "华北"))
				require.NoError(t, wb.SetCellValue("Sheet1", "A3", "1月"))
				require.NoError(t, wb.SetCellValue("Sheet1", "B3", 100))
				require.NoError(t, wb.SetCellValue("Sheet1", "C3", 200))
				require.NoError(t, wb.SetCellValue("Sheet1", "D3", 300))
			},
			wantCols:     []string{"C0", "华东", "华南", "华北", excelSheetNameColumn},
			wantRowCount: 1,
			checkQuery:   `SELECT "C0", "华东" FROM dataset`,
			wantRows:     []map[string]string{{"C0": "1月", "华东": "100"}},
		},
		{
			name: "footer note after blank row is excluded",
			build: func(wb *excelize.File) {
				require.NoError(t, wb.SetCellValue("Sheet1", "A1", "2026 销售"))
				require.NoError(t, wb.MergeCell("Sheet1", "A1", "C1"))
				require.NoError(t, wb.SetCellValue("Sheet1", "A2", "月份"))
				require.NoError(t, wb.SetCellValue("Sheet1", "B2", "地区"))
				require.NoError(t, wb.SetCellValue("Sheet1", "C2", "销售额"))
				require.NoError(t, wb.SetCellValue("Sheet1", "A3", "1月"))
				require.NoError(t, wb.SetCellValue("Sheet1", "B3", "华东"))
				require.NoError(t, wb.SetCellValue("Sheet1", "C3", 100))
				// Row 4 blank
				require.NoError(t, wb.SetCellValue("Sheet1", "A5", "注：数据仅供参考"))
				require.NoError(t, wb.MergeCell("Sheet1", "A5", "C5"))
			},
			wantCols:     []string{"月份", "地区", "销售额", excelSheetNameColumn},
			wantRowCount: 1,
		},
		{
			name: "rich text merged banner with wide style-only cells",
			build: func(wb *excelize.File) {
				style, err := wb.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
				require.NoError(t, err)
				require.NoError(t, wb.SetCellStyle("Sheet1", "A1", "Z20", style))
				require.NoError(t, wb.SetCellRichText("Sheet1", "A1", []excelize.RichTextRun{
					{Text: "2026 ", Font: &excelize.Font{Bold: true}},
					{Text: "年度销售统计"},
				}))
				require.NoError(t, wb.MergeCell("Sheet1", "A1", "C1"))
				require.NoError(t, wb.SetCellValue("Sheet1", "A2", "月份"))
				require.NoError(t, wb.SetCellValue("Sheet1", "B2", "地区"))
				require.NoError(t, wb.SetCellValue("Sheet1", "C2", "销售额"))
				require.NoError(t, wb.SetCellValue("Sheet1", "A3", "1月"))
				require.NoError(t, wb.SetCellValue("Sheet1", "B3", "华东"))
				require.NoError(t, wb.SetCellValue("Sheet1", "C3", 100))
			},
			wantCols:     []string{"月份", "地区", "销售额", excelSheetNameColumn},
			wantRowCount: 1,
		},
		{
			name: "whitespace-only shared string beside title row",
			build: func(wb *excelize.File) {
				require.NoError(t, wb.SetCellValue("Sheet1", "A1", "2026 年度销售统计"))
				require.NoError(t, wb.SetCellValue("Sheet1", "B1", "   "))
				require.NoError(t, wb.SetCellValue("Sheet1", "A2", "月份"))
				require.NoError(t, wb.SetCellValue("Sheet1", "B2", "地区"))
				require.NoError(t, wb.SetCellValue("Sheet1", "C2", "销售额"))
				require.NoError(t, wb.SetCellValue("Sheet1", "A3", "1月"))
				require.NoError(t, wb.SetCellValue("Sheet1", "B3", "华东"))
				require.NoError(t, wb.SetCellValue("Sheet1", "C3", 100))
			},
			wantCols:     []string{"月份", "地区", "销售额", excelSheetNameColumn},
			wantRowCount: 1,
		},
		{
			name: "multi-sheet mixed merged banner and normal header",
			build: func(wb *excelize.File) {
				require.NoError(t, wb.SetSheetName("Sheet1", "Sales"))
				require.NoError(t, wb.SetCellValue("Sales", "A1", "2026 销售"))
				require.NoError(t, wb.MergeCell("Sales", "A1", "C1"))
				require.NoError(t, wb.SetCellValue("Sales", "A2", "月份"))
				require.NoError(t, wb.SetCellValue("Sales", "B2", "地区"))
				require.NoError(t, wb.SetCellValue("Sales", "C2", "销售额"))
				require.NoError(t, wb.SetCellValue("Sales", "A3", "1月"))
				require.NoError(t, wb.SetCellValue("Sales", "B3", "华东"))
				require.NoError(t, wb.SetCellValue("Sales", "C3", 100))

				_, err := wb.NewSheet("Target")
				require.NoError(t, err)
				require.NoError(t, wb.SetCellValue("Target", "A1", "地区"))
				require.NoError(t, wb.SetCellValue("Target", "B1", "目标"))
				require.NoError(t, wb.SetCellValue("Target", "A2", "华东"))
				require.NoError(t, wb.SetCellValue("Target", "B2", 500))

				_, err = wb.NewSheet("Cover")
				require.NoError(t, err)
				require.NoError(t, wb.SetCellValue("Cover", "A1", "Report Cover"))
			},
			wantCols:     []string{"月份", "地区", "销售额", excelSheetNameColumn, "目标", "Report Cover"},
			wantRowCount: 2,
			checkQuery:   `SELECT "地区", "销售额", "目标", "__sheet_name" FROM dataset ORDER BY "__sheet_name"`,
			wantRows: []map[string]string{
				{"地区": "华东", "销售额": "100", "目标": "<nil>", "__sheet_name": "Sales"},
				{"地区": "华东", "销售额": "<nil>", "目标": "500", "__sheet_name": "Target"},
			},
		},
		{
			name: "plain two-group multi-level header is not skipped",
			build: func(wb *excelize.File) {
				require.NoError(t, wb.SetCellValue("Sheet1", "A1", "上半年"))
				require.NoError(t, wb.MergeCell("Sheet1", "A1", "C1"))
				require.NoError(t, wb.SetCellValue("Sheet1", "D1", "下半年"))
				require.NoError(t, wb.MergeCell("Sheet1", "D1", "F1"))
				for i, h := range []string{"1月", "2月", "3月", "4月", "5月", "6月"} {
					c, _ := excelize.CoordinatesToCellName(i+1, 2)
					require.NoError(t, wb.SetCellValue("Sheet1", c, h))
				}
			},
			wantCols:     []string{"上半年", "C1", "_1", "下半年", excelSheetNameColumn},
			wantRowCount: 1,
		},
		{
			name: "late column beyond sample window is preserved via tail scan",
			build: func(wb *excelize.File) {
				require.NoError(t, wb.SetCellValue("Sheet1", "A1", "2026 销售明细"))
				require.NoError(t, wb.MergeCell("Sheet1", "A1", "C1"))
				require.NoError(t, wb.SetCellValue("Sheet1", "A2", "月份"))
				require.NoError(t, wb.SetCellValue("Sheet1", "B2", "地区"))
				require.NoError(t, wb.SetCellValue("Sheet1", "C2", "销售额"))
				for r := 3; r <= 37; r++ {
					c1, _ := excelize.CoordinatesToCellName(1, r)
					c2, _ := excelize.CoordinatesToCellName(2, r)
					c3, _ := excelize.CoordinatesToCellName(3, r)
					require.NoError(t, wb.SetCellValue("Sheet1", c1, "1月"))
					require.NoError(t, wb.SetCellValue("Sheet1", c2, "华东"))
					require.NoError(t, wb.SetCellValue("Sheet1", c3, r*10))
					if r == 35 {
						c4, _ := excelize.CoordinatesToCellName(4, r)
						require.NoError(t, wb.SetCellValue("Sheet1", c4, "late-note"))
					}
				}
			},
			wantCols:     []string{"月份", "地区", "销售额", "C3", excelSheetNameColumn},
			wantRowCount: 35,
			checkQuery:   `SELECT "C3" FROM dataset WHERE "C3" IS NOT NULL`,
			wantRows:     []map[string]string{{"C3": "late-note"}},
		},
		{
			name: "plain pivot table with blank A1 is not skipped",
			build: func(wb *excelize.File) {
				require.NoError(t, wb.SetCellValue("Sheet1", "B1", "华东"))
				require.NoError(t, wb.SetCellValue("Sheet1", "C1", "华南"))
				require.NoError(t, wb.SetCellValue("Sheet1", "D1", "华北"))
				require.NoError(t, wb.SetCellValue("Sheet1", "A2", "1月"))
				require.NoError(t, wb.SetCellValue("Sheet1", "B2", 100))
				require.NoError(t, wb.SetCellValue("Sheet1", "C2", 200))
				require.NoError(t, wb.SetCellValue("Sheet1", "D2", 300))
			},
			wantCols:     []string{"华东", "华南", "华北", excelSheetNameColumn},
			wantRowCount: 1,
		},
		{
			name: "stacked tables stop at first table header",
			build: func(wb *excelize.File) {
				require.NoError(t, wb.SetCellValue("Sheet1", "A1", "Section 1"))
				require.NoError(t, wb.SetCellValue("Sheet1", "A2", "id"))
				require.NoError(t, wb.SetCellValue("Sheet1", "B2", "val"))
				require.NoError(t, wb.SetCellValue("Sheet1", "A3", "a"))
				require.NoError(t, wb.SetCellValue("Sheet1", "B3", "b"))
				// Row 4 blank; Row 5 starts a wider second table
				require.NoError(t, wb.SetCellValue("Sheet1", "A5", "c1"))
				require.NoError(t, wb.SetCellValue("Sheet1", "B5", "c2"))
				require.NoError(t, wb.SetCellValue("Sheet1", "C5", "c3"))
				require.NoError(t, wb.SetCellValue("Sheet1", "D5", "c4"))
			},
			wantCols:     []string{"id", "val", excelSheetNameColumn},
			wantRowCount: 1,
		},
		{
			name: "stray XFD cell falls back to header width without OOM",
			build: func(wb *excelize.File) {
				require.NoError(t, wb.SetCellValue("Sheet1", "A1", "2026 销售"))
				require.NoError(t, wb.MergeCell("Sheet1", "A1", "C1"))
				require.NoError(t, wb.SetCellValue("Sheet1", "A2", "月份"))
				require.NoError(t, wb.SetCellValue("Sheet1", "B2", "地区"))
				require.NoError(t, wb.SetCellValue("Sheet1", "C2", "销售额"))
				require.NoError(t, wb.SetCellValue("Sheet1", "A3", "1月"))
				require.NoError(t, wb.SetCellValue("Sheet1", "B3", "华东"))
				require.NoError(t, wb.SetCellValue("Sheet1", "C3", 100))
				require.NoError(t, wb.SetCellValue("Sheet1", "XFD3", "stray"))
			},
			wantCols:     []string{"月份", "地区", "销售额", excelSheetNameColumn},
			wantRowCount: 1,
		},
		{
			name: "single column sheet is not skipped",
			build: func(wb *excelize.File) {
				require.NoError(t, wb.SetCellValue("Sheet1", "A1", "sku"))
				require.NoError(t, wb.SetCellValue("Sheet1", "A2", "A-1"))
				require.NoError(t, wb.SetCellValue("Sheet1", "A3", "B-2"))
			},
			wantCols:     []string{"sku", excelSheetNameColumn},
			wantRowCount: 2,
		},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			xlsxPath := saveTestXLSX(t, "case.xlsx", tc.build)
			tableName := "t_hdr_" + string(rune('a'+i))
			tool := &DataAnalysisTool{
				BaseTool:  dataAnalysisTool,
				db:        db,
				sessionID: "test-header-layouts",
			}
			t.Cleanup(func() { tool.Cleanup(ctx) })

			schema, err := tool.LoadFromExcel(ctx, xlsxPath, tableName)
			require.NoError(t, err)
			require.Equal(t, tc.wantCols, schemaColumnNames(schema))
			require.EqualValues(t, tc.wantRowCount, schema.RowCount)

			if tc.checkQuery != "" {
				rows, err := tool.executeSingleQuery(ctx, tc.checkQuery, tableName)
				require.NoError(t, err)
				require.Equal(t, tc.wantRows, rows)
			}
		})
	}
}

func TestBuildExcelCreateTableSQLWithRanges(t *testing.T) {
	// Empty ranges reproduce the legacy SQL byte-for-byte.
	legacy := buildExcelCreateTableSQL("tbl", "/tmp/a.xlsx", []string{"Sheet1", "Sheet2"})
	withEmpty := buildExcelCreateTableSQLWithRanges("tbl", "/tmp/a.xlsx", []string{"Sheet1", "Sheet2"}, nil)
	require.Equal(t, legacy, withEmpty)

	// Per-sheet range adds both range and stop_at_empty=true only to sheets that need it.
	mixed := buildExcelCreateTableSQLWithRanges(
		"tbl", "/tmp/a.xlsx", []string{"Sales", "Target"}, map[string]string{"Sales": "A2:D"},
	)
	require.Contains(t, mixed,
		"read_xlsx('/tmp/a.xlsx', sheet = 'Sales', range = 'A2:D', header=true, all_varchar=true, stop_at_empty=true)")
	require.Contains(t, mixed,
		"read_xlsx('/tmp/a.xlsx', sheet = 'Target', header=true, all_varchar=true)")
	require.NotContains(t, mixed, "sheet = 'Target', range")
}

func TestDataAnalysisExecute_ConsistencyWarnings(t *testing.T) {
	db := newPlainDuckDB(t)
	ctx := context.Background()

	runQuery := func(t *testing.T, docID, csvContent, sql string) *types.ToolResult {
		t.Helper()
		knowledge := &types.Knowledge{ID: docID, FileType: "csv", FilePath: docID + ".csv"}
		tool := NewDataAnalysisTool(
			nil,
			&mapKnowledgeService{docs: map[string]*types.Knowledge{docID: knowledge}},
			nil,
			&csvFileService{files: map[string]string{docID + ".csv": csvContent}},
			db,
			"consistency-"+docID,
		)
		t.Cleanup(func() { tool.Cleanup(ctx) })

		args, err := json.Marshal(DataAnalysisInput{KnowledgeID: docID, SQL: sql})
		require.NoError(t, err)
		res, err := tool.Execute(ctx, args)
		require.NoError(t, err)
		require.True(t, res.Success)
		return res
	}

	t.Run("warns when total row disagrees with detail sum", func(t *testing.T) {
		csv := "地区,销售额,单价\n华东,100,10\n华南,200,12\n合计,450,11\n"
		res := runQuery(t, "tot-bad", csv, `SELECT "地区", "销售额", "单价" FROM dataset`)
		require.Contains(t, res.Output, "=== Consistency Warnings ===")
		require.Contains(t, res.Output, `Column "销售额"`)
		require.NotContains(t, res.Output, `Column "单价"`, "non-additive price column must not warn")
		warnings, ok := res.Data["warnings"].([]string)
		require.True(t, ok)
		require.Len(t, warnings, 1)
	})

	t.Run("no warning when total row matches detail sum", func(t *testing.T) {
		csv := "地区,销售额,单价\n华东,100,10\n华南,200,12\n合计,300,11\n"
		res := runQuery(t, "tot-ok", csv, `SELECT "地区", "销售额", "单价" FROM dataset`)
		require.NotContains(t, res.Output, "Consistency Warnings")
		require.Nil(t, res.Data["warnings"])
	})

	t.Run("warns when share column does not sum to 1 or 100 percent", func(t *testing.T) {
		csv := "地区,占比\n华东,0.50\n华南,0.35\n"
		res := runQuery(t, "share-bad", csv, `SELECT "地区", "占比" FROM dataset`)
		require.Contains(t, res.Output, "=== Consistency Warnings ===")
		require.Contains(t, res.Output, `Column "占比"`)
		require.Contains(t, res.Output, "0.85")

		csvPct := "地区,占比\n华东,50%\n华南,35%\n"
		resPct := runQuery(t, "share-pct-bad", csvPct, `SELECT "地区", "占比" FROM dataset`)
		require.Contains(t, resPct.Output, "85%")
		require.Contains(t, resPct.Output, "~100%")
	})

	t.Run("no false alarm on filtered or limited share queries or non-share rates and weights", func(t *testing.T) {
		csv := "地区,占比,同比增长率,weight\n华东,0.50,0.15,12.5\n华南,0.35,0.20,8.3\n华北,0.15,0.10,4.2\n"
		// Filtered subset: WHERE skips share-sum warning
		resFiltered := runQuery(t, "share-where", csv, `SELECT "地区", "占比" FROM dataset WHERE "地区" <> '华北'`)
		require.Nil(t, resFiltered.Data["warnings"])
		// Limited subset: LIMIT skips share-sum warning
		resLimited := runQuery(t, "share-limit", csv, `SELECT "地区", "占比" FROM dataset LIMIT 2`)
		require.Nil(t, resLimited.Data["warnings"])
		// Full table with valid share sum + growth rate + weight: zero warnings
		resFull := runQuery(t, "share-full", csv, `SELECT "地区", "占比", "同比增长率", "weight" FROM dataset`)
		require.Nil(t, resFull.Data["warnings"])
	})

	t.Run("existing SQL security validation remains enforced", func(t *testing.T) {
		knowledge := &types.Knowledge{ID: "sec-doc", FileType: "csv", FilePath: "sec.csv"}
		tool := NewDataAnalysisTool(
			nil,
			&mapKnowledgeService{docs: map[string]*types.Knowledge{knowledge.ID: knowledge}},
			nil,
			&csvFileService{files: map[string]string{"sec.csv": "id,amount\n1,10\n"}},
			db,
			"consistency-sec",
		)
		t.Cleanup(func() { tool.Cleanup(ctx) })

		for _, badSQL := range []string{
			`DROP TABLE dataset`,
			`SELECT * FROM read_xlsx('/etc/passwd')`,
			`SELECT * FROM other_table`,
		} {
			args, err := json.Marshal(DataAnalysisInput{KnowledgeID: knowledge.ID, SQL: badSQL})
			require.NoError(t, err)
			res, err := tool.Execute(ctx, args)
			require.Error(t, err, badSQL)
			require.False(t, res.Success, badSQL)
		}
	})
}

func TestDataAnalysisExcelHeader_SupportsLegacyXLSWithTitleRow(t *testing.T) {
	// Convert a workbook with an unmerged title row (the exact structure produced
	// when convertLegacyXLSToXLSX converts a BIFF .xls file with a merged banner)
	// and verify LoadFromExcel recovers the real columns cleanly.
	db := newTestDuckDB(t)
	ctx := context.Background()
	xlsxPath := saveTestXLSX(t, "converted_legacy.xlsx", func(wb *excelize.File) {
		require.NoError(t, wb.SetCellValue("Sheet1", "A1", "2026 年度销售统计"))
		require.NoError(t, wb.SetCellValue("Sheet1", "A2", "月份"))
		require.NoError(t, wb.SetCellValue("Sheet1", "B2", "地区"))
		require.NoError(t, wb.SetCellValue("Sheet1", "C2", "销售额"))
		require.NoError(t, wb.SetCellValue("Sheet1", "A3", "1月"))
		require.NoError(t, wb.SetCellValue("Sheet1", "B3", "华东"))
		require.NoError(t, wb.SetCellValue("Sheet1", "C3", "100"))
	})

	// Also verify a renamed .xlsx carrying a .xls extension runs header detection.
	renamedXLS := filepath.Join(t.TempDir(), "renamed_merged.xls")
	raw, err := os.ReadFile(xlsxPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(renamedXLS, raw, 0o600))

	tool := &DataAnalysisTool{BaseTool: dataAnalysisTool, db: db, sessionID: "legacy-title"}
	t.Cleanup(func() { tool.Cleanup(ctx) })

	schema, err := tool.LoadFromExcel(ctx, renamedXLS, "t_renamed_xls")
	require.NoError(t, err)
	require.Equal(t, []string{"月份", "地区", "销售额", excelSheetNameColumn}, schemaColumnNames(schema))
	require.EqualValues(t, 1, schema.RowCount)
	require.False(t, strings.Contains(schema.Description(), "2026 年度销售统计"))
}
