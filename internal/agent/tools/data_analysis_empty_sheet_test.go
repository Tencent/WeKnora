package tools

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

func TestLoadFromExcelSkipsStyleOnlySheet(t *testing.T) {
	file := filepath.Join(t.TempDir(), "styled.xlsx")
	writeWorkbook(t, file, map[string][][]any{
		"Data": {{"id", "amount"}, {1, 100}}, "Empty": {},
	}, []string{"Empty", "Data"})
	wb, err := excelize.OpenFile(file)
	require.NoError(t, err)
	style, err := wb.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	require.NoError(t, err)
	require.NoError(t, wb.SetCellStyle("Empty", "A1", "B2", style))
	require.NoError(t, wb.SetSheetDimension("Empty", "A1:B2"))
	require.NoError(t, wb.Save())
	require.NoError(t, wb.Close())
	tool := &DataAnalysisTool{BaseTool: dataAnalysisTool, db: newTestDuckDB(t), sessionID: "empty-sheet"}
	schema, err := tool.LoadFromExcel(context.Background(), file, "data")
	require.NoError(t, err)
	require.EqualValues(t, 1, schema.RowCount)
	require.Equal(t, 3, len(schema.Columns), "formatting must not add columns to the data sheet")
	rows, err := tool.executeSingleQuery(context.Background(), `SELECT * FROM dataset`, "data")
	require.NoError(t, err)
	require.Equal(t, []map[string]string{{"id": "1", "amount": "100", "__sheet_name": "Data"}}, rows)
	require.Less(t, len(schema.Description()), 200)
}

func TestLoadFromExcelPreservesHeaderOnlyAndFormulaSheets(t *testing.T) {
	file := filepath.Join(t.TempDir(), "content.xlsx")
	writeWorkbook(t, file, map[string][][]any{
		"Data":    {{"id", "amount"}, {1, 100}},
		"Headers": {{"note", "comment"}},
		"Formula": {{"computed"}},
		"Values":  {{"zero", "flag", "text"}, {0, false, "kept"}},
	}, []string{"Headers", "Formula", "Values", "Data"})
	wb, err := excelize.OpenFile(file)
	require.NoError(t, err)
	require.NoError(t, wb.SetCellFormula("Formula", "A2", "1+1"))
	require.NoError(t, wb.Save())
	require.NoError(t, wb.Close())
	tool := &DataAnalysisTool{BaseTool: dataAnalysisTool, db: newTestDuckDB(t), sessionID: "content"}
	schema, err := tool.LoadFromExcel(context.Background(), file, "content")
	require.NoError(t, err)
	names := make([]string, 0, len(schema.Columns))
	for _, col := range schema.Columns {
		names = append(names, col.Name)
	}
	require.ElementsMatch(t,
		[]string{"id", "amount", "note", "comment", "computed", "zero", "flag", "text", "__sheet_name"}, names)
	require.EqualValues(t, 2, schema.RowCount)
}

func TestLoadFromExcelRejectsAllStyleOnlySheets(t *testing.T) {
	file := filepath.Join(t.TempDir(), "empty.xlsx")
	wb := excelize.NewFile()
	style, err := wb.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	require.NoError(t, err)
	require.NoError(t, wb.SetCellStyle("Sheet1", "A1", "B2", style))
	require.NoError(t, wb.SaveAs(file))
	require.NoError(t, wb.Close())
	tool := &DataAnalysisTool{BaseTool: dataAnalysisTool, db: newTestDuckDB(t), sessionID: "all-empty"}
	_, err = tool.LoadFromExcel(context.Background(), file, "empty")
	require.ErrorContains(t, err, "no non-empty worksheets")
}

// rewriteExcelParts modifies synthetic fixtures without parsing away malformed
// XML or changing relationship targets back to Excelize's default layout.
func rewriteExcelParts(t *testing.T, filename string, change func(string, []byte) (string, []byte)) {
	t.Helper()
	reader, err := zip.OpenReader(filename)
	require.NoError(t, err)
	dest := filename + ".rewritten"
	f, err := os.Create(dest)
	require.NoError(t, err)
	writer := zip.NewWriter(f)
	for _, part := range reader.File {
		r, err := part.Open()
		require.NoError(t, err)
		data, err := io.ReadAll(r)
		require.NoError(t, err)
		require.NoError(t, r.Close())
		name, data := change(part.Name, data)
		w, err := writer.Create(name)
		require.NoError(t, err)
		_, err = w.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	require.NoError(t, f.Close())
	require.NoError(t, reader.Close())
	require.NoError(t, os.Rename(dest, filename))
}

func TestFilterExcelMalformedSheetIsNotSkipped(t *testing.T) {
	file := filepath.Join(t.TempDir(), "broken.xlsx")
	writeWorkbook(t, file, map[string][][]any{"Data": {{"id"}, {1}}, "Broken": {}}, []string{"Data", "Broken"})
	rewriteExcelParts(t, file, func(name string, data []byte) (string, []byte) {
		if name == "xl/worksheets/sheet3.xml" {
			data = []byte(`<worksheet><sheetData><row r="1"><c r="A1"></row></sheetData></worksheet>`)
		}
		return name, data
	})
	// Exercise the preflight directly too: GDAL may reject/omit malformed sheets
	// before LoadFromExcel receives their names.
	_, err := filterEmptyExcelSheets(context.Background(), file, []string{"Data", "Broken"})
	require.Error(t, err)
}

func TestExcelContentCheckPreservesCellKinds(t *testing.T) {
	for _, cell := range []string{
		`<c><v>0</v></c>`, `<c t="b"><v>0</v></c>`, `<c t="s"><v>0</v></c>`,
		`<c t="inlineStr"><is><t>value</t></is></c>`,
		`<c t="inlineStr"><is><r><t xml:space="preserve"> </t></r></is></c>`,
		`<c><f>1+1</f></c>`, `<c><f t="shared" si="0"/></c>`,
	} {
		input := `<worksheet><sheetData><row>` + cell + `</row></sheetData></worksheet>`
		content, err := excelSheetHasContent(context.Background(), strings.NewReader(input))
		require.NoError(t, err)
		require.True(t, content, cell)
	}
}

func TestFilterExcelRelationshipsAndOrder(t *testing.T) {
	file := filepath.Join(t.TempDir(), "relationships.xlsx")
	writeWorkbook(t, file, map[string][][]any{
		"Data": {{"id"}, {1}}, "Empty": {}, "Headers": {{"note"}},
	}, []string{"Data", "Empty", "Headers"})
	rewriteExcelParts(t, file, func(name string, data []byte) (string, []byte) {
		if name == "xl/worksheets/sheet2.xml" {
			name = "xl/worksheets/custom sheet.xml"
		}
		if name == "xl/_rels/workbook.xml.rels" {
			const target = `Target="/xl/worksheets/custom%20sheet.xml"`
			data = []byte(strings.ReplaceAll(string(data), `Target="/xl/worksheets/sheet2.xml"`, target))
			data = []byte(strings.ReplaceAll(string(data), `Target="worksheets/sheet2.xml"`, target))
		}
		return name, data
	})
	kept, err := filterEmptyExcelSheets(context.Background(), file, []string{"Headers", "Empty", "Data"})
	require.NoError(t, err)
	require.Equal(t, []string{"Headers", "Data"}, kept)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = filterEmptyExcelSheets(ctx, file, []string{"Data"})
	require.ErrorIs(t, err, context.Canceled)
}

func TestExcelContentCheckRequiresValidEmptyXML(t *testing.T) {
	for _, raw := range []string{"", `<notWorksheet/>`, `<worksheet><sheetData>`, `<worksheet/><worksheet/>`} {
		_, err := excelSheetHasContent(context.Background(), strings.NewReader(raw))
		require.Error(t, err, raw)
	}
	content, err := excelSheetHasContent(context.Background(), strings.NewReader(
		`<worksheet><dimension ref="A1:XFD1048576"/><sheetData><row><c s="1"/></row></sheetData></worksheet>`))
	require.NoError(t, err)
	require.False(t, content)
}

func BenchmarkExcelSheetContentCheck(b *testing.B) {
	for _, tc := range []struct{ name, cell string }{
		{"populated", `<c t="inlineStr"><is><t>id</t></is></c>`},
		{"style_only", `<c s="1"/>`},
	} {
		b.Run(tc.name, func(b *testing.B) {
			rows := strings.Repeat(`<row>`+tc.cell+`</row>`, 10000)
			xml := `<worksheet><sheetData>` + rows + `</sheetData></worksheet>`
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_, err := excelSheetHasContent(context.Background(), strings.NewReader(xml))
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
