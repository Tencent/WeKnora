package tools

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestLoadFromExcelWithoutSpatial(t *testing.T) {
	for _, firstSheet := range []string{"Cover", "Empty", "Data"} {
		t.Run(firstSheet, func(t *testing.T) {
			db, err := sql.Open("duckdb", ":memory:")
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			// Production can continue after spatial fails to load. Disable
			// autoloading so this test does not depend on the extension cache.
			_, err = db.Exec("SET autoload_known_extensions=false")
			require.NoError(t, err)
			if _, err = db.Exec("INSTALL excel; LOAD excel"); err != nil {
				t.Skipf("cannot install/load DuckDB excel extension: %v", err)
			}
			var spatialLoaded bool
			err = db.QueryRow(
				"SELECT loaded FROM duckdb_extensions() WHERE extension_name = 'spatial'",
			).Scan(&spatialLoaded)
			require.NoError(t, err)
			require.False(t, spatialLoaded)

			file := filepath.Join(t.TempDir(), "workbook.xlsx")
			sheets := map[string][][]any{
				"Cover":    {{"Report title"}},
				"Empty":    {},
				"Data":     {{"id", "amount"}, {1, 100}},
				"Q1'24 数据": {{"id", "amount"}, {2, 250}},
			}
			order := []string{firstSheet}
			if firstSheet != "Data" {
				order = append(order, "Data")
			}
			order = append(order, "Q1'24 数据")
			writeWorkbook(t, file, sheets, order)
			tool := &DataAnalysisTool{BaseTool: dataAnalysisTool, db: db, sessionID: "sheet-discovery"}
			schema, err := tool.LoadFromExcel(context.Background(), file, "workbook")
			require.NoError(t, err)
			require.EqualValues(t, 2, schema.RowCount, "all data sheets must be loaded even without spatial")
			rows, err := tool.executeSingleQuery(context.Background(),
				"SELECT id, amount, __sheet_name FROM dataset ORDER BY id", "workbook")
			require.NoError(t, err)
			require.Equal(t, []map[string]string{
				{"id": "1", "amount": "100", "__sheet_name": "Data"},
				{"id": "2", "amount": "250", "__sheet_name": "Q1'24 数据"},
			}, rows)
		})
	}
}

func TestListExcelSheetsRecoversFromEmptyMetadata(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("SELECT UNNEST").WillReturnRows(sqlmock.NewRows([]string{"name"}))
	file := filepath.Join(t.TempDir(), "metadata.xlsx")
	writeWorkbook(t, file, map[string][][]any{
		"Second": {{"id"}, {2}}, "First": {{"id"}, {1}},
	}, []string{"Second", "First"})
	tool := &DataAnalysisTool{db: db}
	names, err := tool.listExcelSheets(context.Background(), file)
	require.NoError(t, err)
	require.Equal(t, []string{"Second", "First"}, names)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLoadFromExcelDoesNotFallBackToFirstSheetAfterDiscoveryFailure(t *testing.T) {
	for _, raw := range []string{
		`<workbook><sheets>`,
		`<workbook><sheets/></workbook>`,
		`<notWorkbook/>`,
		`<workbook><sheets><sheet name=""/></sheets></workbook>`,
	} {
		t.Run(raw, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			mock.ExpectQuery("SELECT UNNEST").WillReturnError(errors.New("spatial unavailable"))
			file := filepath.Join(t.TempDir(), "broken.xlsx")
			writeWorkbook(t, file, map[string][][]any{"Data": {{"id"}, {1}}}, []string{"Data"})
			rewriteExcelParts(t, file, func(name string, data []byte) (string, []byte) {
				if name == "xl/workbook.xml" {
					data = []byte(raw)
				}
				return name, data
			})
			tool := &DataAnalysisTool{db: db}
			_, err = tool.LoadFromExcel(context.Background(), file, "broken")
			require.ErrorContains(t, err, "enumerate Excel worksheets")
			require.False(t, strings.Contains(err.Error(), "was not expected"),
				"discovery failure must not attempt CREATE TABLE from the first sheet")
			require.False(t, tool.loadedTables["broken"])
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
