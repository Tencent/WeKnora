package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

func TestPrepareExcelForDuckDBConvertsLegacyXLS(t *testing.T) {
	src := filepath.Join("testdata", "legacy.xls")
	converted, cleanup, err := prepareExcelForDuckDB(src)
	require.NoError(t, err)
	t.Cleanup(cleanup)
	require.NotEqual(t, src, converted)

	book, err := excelize.OpenFile(converted)
	require.NoError(t, err)
	t.Cleanup(func() { _ = book.Close() })

	sales, err := book.GetRows("销售")
	require.NoError(t, err)
	require.Equal(t, []string{"名称", "数量"}, sales[0])
	require.Equal(t, []string{"苹果", "3"}, sales[1])
	require.Equal(t, []string{"梨", "12.5"}, sales[2])

	stock, err := book.GetRows("库存")
	require.NoError(t, err)
	require.Equal(t, [][]string{{"sku"}, {"A-1"}}, stock)
}

func TestPrepareExcelForDuckDBLeavesNonOLEXlsAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not.xls")
	require.NoError(t, os.WriteFile(path, []byte("PK\x03\x04not-an-xls"), 0o644))
	got, cleanup, err := prepareExcelForDuckDB(path)
	require.NoError(t, err)
	cleanup()
	require.Equal(t, path, got)
}

func TestPrepareExcelForDuckDBAcceptsUppercaseExtension(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "legacy.xls"))
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "LEGACY.XLS")
	require.NoError(t, os.WriteFile(path, data, 0o644))

	converted, cleanup, err := prepareExcelForDuckDB(path)
	require.NoError(t, err)
	t.Cleanup(cleanup)

	book, err := excelize.OpenFile(converted)
	require.NoError(t, err)
	t.Cleanup(func() { _ = book.Close() })
	rows, err := book.GetRows("销售")
	require.NoError(t, err)
	require.Equal(t, []string{"苹果", "3"}, rows[1])
}

func TestUniqueExcelSheetNameStripsReservedCharacters(t *testing.T) {
	used := map[string]struct{}{}
	first := uniqueExcelSheetName("a/b:c", 0, used)
	second := uniqueExcelSheetName("a/b:c", 1, used)
	require.Equal(t, "a_b_c", first)
	require.NotEqual(t, first, second)
	require.LessOrEqual(t, len([]rune(second)), 31)
}

func TestPrepareExcelForDuckDBLeavesXLSXAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.xlsx")
	writeWorkbook(t, path, map[string][][]any{
		"Sheet1": {{"a"}, {"b"}},
	}, []string{"Sheet1"})

	got, cleanup, err := prepareExcelForDuckDB(path)
	require.NoError(t, err)
	cleanup()
	require.Equal(t, path, got)
}

func TestLoadFromExcelReadsLegacyXLS(t *testing.T) {
	db := newTestDuckDB(t)
	tool := &DataAnalysisTool{
		BaseTool:  dataAnalysisTool,
		db:        db,
		sessionID: "test-legacy-xls",
	}
	ctx := context.Background()
	schema, err := tool.LoadFromExcel(ctx, filepath.Join("testdata", "legacy.xls"), "t_legacy")
	require.NoError(t, err)
	t.Cleanup(func() { tool.Cleanup(ctx) })

	// Two data rows on 销售 and one on 库存.
	require.Equal(t, int64(3), schema.RowCount)
}
