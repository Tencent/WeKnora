package tools

import (
	"archive/zip"
	"context"
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

const docxTestNamespace = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"

// docxCellXML renders one table cell with the given text runs.
func docxCellXML(t *testing.T, runs ...string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(`<w:tc><w:p>`)
	for _, run := range runs {
		b.WriteString(`<w:r><w:t xml:space="preserve">`)
		require.NoError(t, xml.EscapeText(&b, []byte(run)))
		b.WriteString(`</w:t></w:r>`)
	}
	b.WriteString(`</w:p></w:tc>`)
	return b.String()
}

// docxTableXML renders a table from rows of plain cell text.
func docxTableXML(t *testing.T, rows [][]string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(`<w:tbl>`)
	for _, row := range rows {
		b.WriteString(`<w:tr>`)
		for _, cell := range row {
			b.WriteString(docxCellXML(t, cell))
		}
		b.WriteString(`</w:tr>`)
	}
	b.WriteString(`</w:tbl>`)
	return b.String()
}

// writeDocxFile writes a minimal .docx whose body is bodyInner.
func writeDocxFile(t *testing.T, name, bodyInner string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	require.NoError(t, err)
	w := zip.NewWriter(f)
	contentTypes := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.` +
		`wordprocessingml.document.main+xml"/>` +
		`</Types>`
	require.NoError(t, func() error {
		cw, err := w.Create("[Content_Types].xml")
		if err != nil {
			return err
		}
		_, err = cw.Write([]byte(contentTypes))
		return err
	}())
	doc := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="` + docxTestNamespace + `"><w:body>` + bodyInner + `</w:body></w:document>`
	require.NoError(t, func() error {
		dw, err := w.Create("word/document.xml")
		if err != nil {
			return err
		}
		_, err = dw.Write([]byte(doc))
		return err
	}())
	require.NoError(t, w.Close())
	require.NoError(t, f.Close())
	return path
}

func TestExtractDocxTables_SingleTable(t *testing.T) {
	path := writeDocxFile(t, "one.docx",
		`<w:p><w:r><w:t>before</w:t></w:r></w:p>`+
			docxTableXML(t, [][]string{
				{"月份", "地区", "销售额"},
				{"1月", "华东", "100"},
				{"2月", "华南", "150"},
			})+
			`<w:p><w:r><w:t>after</w:t></w:r></w:p>`)
	tables, err := extractDocxTables(path)
	require.NoError(t, err)
	require.Len(t, tables, 1)
	require.Equal(t, [][]string{
		{"月份", "地区", "销售额"},
		{"1月", "华东", "100"},
		{"2月", "华南", "150"},
	}, tables[0].rows)
}

func TestExtractDocxTables_MultipleTablesUnion(t *testing.T) {
	path := writeDocxFile(t, "two.docx",
		docxTableXML(t, [][]string{
			{"月份", "地区", "销售额"},
			{"1月", "华东", "100"},
		})+
			docxTableXML(t, [][]string{
				{"产品", "销量"},
				{"键盘", "30"},
				{"鼠标", "45"},
			}))
	tables, err := extractDocxTables(path)
	require.NoError(t, err)
	require.Len(t, tables, 2)

	out, err := docxTablesToCSV(tables)
	require.NoError(t, err)
	require.Equal(t,
		"月份,地区,销售额,产品,销量,__table_index\n"+
			"1月,华东,100,,,1\n"+
			",,,键盘,30,2\n"+
			",,,鼠标,45,2\n",
		string(out))
}

func TestExtractDocxTables_GridSpanAndNestedTable(t *testing.T) {
	body := `<w:tbl><w:tr>` +
		`<w:tc><w:tcPr><w:gridSpan w:val="2"/></w:tcPr><w:p><w:r><w:t>merged</w:t></w:r></w:p></w:tc>` +
		`<w:tc><w:p><w:r><w:t>third</w:t></w:r></w:p></w:tc>` +
		`</w:tr><w:tr>` +
		// A nested table inside a cell: it must not become a table of its
		// own, and its text joins the enclosing cell.
		`<w:tc><w:p><w:r><w:t>outer</w:t></w:r></w:p>` +
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>inner</w:t></w:r></w:p></w:tc></w:tr></w:tbl>` +
		`</w:tc>` +
		`<w:tc><w:p><w:r><w:t>b</w:t></w:r></w:p></w:tc>` +
		`<w:tc><w:p><w:r><w:t>c</w:t></w:r></w:p></w:tc>` +
		`</w:tr></w:tbl>`
	path := writeDocxFile(t, "span.docx", body)
	tables, err := extractDocxTables(path)
	require.NoError(t, err)
	require.Len(t, tables, 1)
	require.Equal(t, [][]string{
		{"merged", "", "third"},
		{"outer inner", "b", "c"},
	}, tables[0].rows)
}

func TestExtractDocxTables_SkipsHeaderOnlyTablesAndEmptyRows(t *testing.T) {
	path := writeDocxFile(t, "skip.docx",
		docxTableXML(t, [][]string{
			{"only", "header"},
		})+
			docxTableXML(t, [][]string{
				{"a", "b"},
				{"", ""},
				{"1", "2"},
				{"", ""},
			}))
	tables, err := extractDocxTables(path)
	require.NoError(t, err)
	require.Len(t, tables, 1)
	require.Equal(t, [][]string{
		{"a", "b"},
		{"1", "2"},
	}, tables[0].rows)
}

func TestExtractDocxTables_HeaderNormalization(t *testing.T) {
	path := writeDocxFile(t, "headers.docx",
		docxTableXML(t, [][]string{
			{"名称", "", "名称", "  "},
			{"x", "1", "y", "2"},
		}))
	tables, err := extractDocxTables(path)
	require.NoError(t, err)
	require.Len(t, tables, 1)

	out, err := docxTablesToCSV(tables)
	require.NoError(t, err)
	require.Equal(t, "名称,column_2,名称_2,column_4,__table_index\nx,1,y,2,1\n", string(out))
}

func TestExtractDocxTables_IgnoresDeletedAndInstructionText(t *testing.T) {
	body := `<w:tbl><w:tr>` +
		`<w:tc><w:p><w:r><w:t>keep</w:t></w:r>` +
		`<w:del><w:r><w:delText>gone</w:delText></w:r></w:del>` +
		`<w:r><w:instrText>PAGE</w:instrText></w:r></w:p></w:tc>` +
		`<w:tc><w:p><w:r><w:t>ok</w:t></w:r></w:p></w:tc>` +
		`</w:tr><w:tr>` +
		`<w:tc><w:p><w:r><w:t>v1</w:t></w:r></w:p></w:tc>` +
		`<w:tc><w:p><w:r><w:t>v2</w:t></w:r></w:p></w:tc>` +
		`</w:tr></w:tbl>`
	path := writeDocxFile(t, "del.docx", body)
	tables, err := extractDocxTables(path)
	require.NoError(t, err)
	require.Len(t, tables, 1)
	require.Equal(t, [][]string{
		{"keep", "ok"},
		{"v1", "v2"},
	}, tables[0].rows)
}

func TestExtractDocxTables_InvalidFiles(t *testing.T) {
	plain := filepath.Join(t.TempDir(), "plain.docx")
	require.NoError(t, os.WriteFile(plain, []byte("not a zip"), 0o644))
	_, err := extractDocxTables(plain)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a valid .docx file")

	empty := filepath.Join(t.TempDir(), "empty.docx")
	f, err := os.Create(empty)
	require.NoError(t, err)
	w := zip.NewWriter(f)
	require.NoError(t, w.Close())
	require.NoError(t, f.Close())
	_, err = extractDocxTables(empty)
	require.Error(t, err)
	require.Contains(t, err.Error(), "word/document.xml not found")

	_, err = extractDocxTables(filepath.Join(t.TempDir(), "missing.docx"))
	require.Error(t, err)
}

func TestDocxTablesToCSV_Quoting(t *testing.T) {
	out, err := docxTablesToCSV([]docxTable{{rows: [][]string{
		{"name", "note"},
		{"a,b", "say \"hi\"\nagain"},
	}}})
	require.NoError(t, err)
	require.Equal(t, "name,note,__table_index\n\"a,b\",\"say \"\"hi\"\"\nagain\",1\n", string(out))
}

func TestLoadFromDocx_EndToEnd(t *testing.T) {
	db := newPlainDuckDB(t)
	docxPath := writeDocxFile(t, "sales.docx",
		docxTableXML(t, [][]string{
			{"月份", "地区", "销售额"},
			{"1月", "华东", "100"},
			{"1月", "华南", "150"},
			{"2月", "华东", "120"},
			{"2月", "华南", "80"},
			{"2月", "华北", "50"},
		}))
	knowledge := &types.Knowledge{
		ID:       "56260000-0000-4000-8000-000000000001",
		FileType: "docx",
		FileName: "sales.docx",
		FilePath: docxPath,
	}
	tool := NewDataAnalysisTool(
		nil,
		&mapKnowledgeService{docs: map[string]*types.Knowledge{knowledge.ID: knowledge}},
		nil,
		&diskFileService{},
		db,
		"test-5626-e2e",
	)
	ctx := context.Background()
	t.Cleanup(func() { tool.Cleanup(ctx) })

	schema, err := tool.LoadFromKnowledge(ctx, knowledge)
	require.NoError(t, err)
	require.Equal(t, []string{"月份", "地区", "销售额", docxTableIndexColumn}, schemaColumnNames(schema))
	require.EqualValues(t, 5, schema.RowCount)

	args, err := json.Marshal(DataAnalysisInput{
		KnowledgeID: knowledge.ID,
		SQL: `SELECT "地区", ` +
			`SUM(CAST("销售额" AS DOUBLE PRECISION)) AS "销售额总和" ` +
			`FROM dataset GROUP BY "地区" ORDER BY "地区"`,
	})
	require.NoError(t, err)

	res, err := tool.Execute(ctx, args)
	require.NoError(t, err)
	require.True(t, res.Success)
	require.Equal(t, []map[string]string{
		{"地区": "华东", "销售额总和": "220"},
		{"地区": "华北", "销售额总和": "50"},
		{"地区": "华南", "销售额总和": "230"},
	}, res.Data["rows"])
}

func TestLoadFromDocx_MultipleTablesEndToEnd(t *testing.T) {
	db := newPlainDuckDB(t)
	docxPath := writeDocxFile(t, "multi.docx",
		docxTableXML(t, [][]string{
			{"月份", "地区", "销售额"},
			{"1月", "华东", "100"},
			{"2月", "华东", "120"},
		})+
			docxTableXML(t, [][]string{
				{"产品", "销量"},
				{"键盘", "30"},
				{"鼠标", "45"},
			}))
	knowledge := &types.Knowledge{
		ID:       "56260000-0000-4000-8000-000000000002",
		FileType: "docx",
		FileName: "multi.docx",
		FilePath: docxPath,
	}
	tool := NewDataAnalysisTool(
		nil,
		&mapKnowledgeService{docs: map[string]*types.Knowledge{knowledge.ID: knowledge}},
		nil,
		&diskFileService{},
		db,
		"test-5626-multi",
	)
	ctx := context.Background()
	t.Cleanup(func() { tool.Cleanup(ctx) })

	schema, err := tool.LoadFromKnowledge(ctx, knowledge)
	require.NoError(t, err)
	require.Equal(t,
		[]string{"月份", "地区", "销售额", "产品", "销量", docxTableIndexColumn},
		schemaColumnNames(schema))
	require.EqualValues(t, 4, schema.RowCount)

	args, err := json.Marshal(DataAnalysisInput{
		KnowledgeID: knowledge.ID,
		SQL: `SELECT "产品", CAST("销量" AS INTEGER) AS "销量" FROM dataset ` +
			`WHERE "__table_index" = '2' ORDER BY "产品"`,
	})
	require.NoError(t, err)
	res, err := tool.Execute(ctx, args)
	require.NoError(t, err)
	require.True(t, res.Success)
	require.Equal(t, []map[string]string{
		{"产品": "键盘", "销量": "30"},
		{"产品": "鼠标", "销量": "45"},
	}, res.Data["rows"])
}

func TestLoadFromDocx_NoDataTables(t *testing.T) {
	db := newPlainDuckDB(t)
	docxPath := writeDocxFile(t, "notables.docx",
		`<w:p><w:r><w:t>just text, no tables</w:t></w:r></w:p>`)
	knowledge := &types.Knowledge{
		ID:       "56260000-0000-4000-8000-000000000003",
		FileType: "docx",
		FileName: "notables.docx",
		FilePath: docxPath,
	}
	tool := NewDataAnalysisTool(
		nil,
		&mapKnowledgeService{docs: map[string]*types.Knowledge{knowledge.ID: knowledge}},
		nil,
		&diskFileService{},
		db,
		"test-5626-notables",
	)
	ctx := context.Background()
	t.Cleanup(func() { tool.Cleanup(ctx) })

	_, err := tool.LoadFromKnowledge(ctx, knowledge)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no data tables")
}

func TestLoadFromKnowledge_DocStillUnsupported(t *testing.T) {
	db := newPlainDuckDB(t)
	// The file must exist so materialization succeeds and the load reaches
	// the file-type switch; its content is irrelevant because .doc is
	// rejected before it is ever read.
	docPath := filepath.Join(t.TempDir(), "legacy.doc")
	require.NoError(t, os.WriteFile(docPath, []byte("not a real word document"), 0o644))
	knowledge := &types.Knowledge{
		ID:       "56260000-0000-4000-8000-000000000004",
		FileType: "doc",
		FileName: "legacy.doc",
		FilePath: docPath,
	}
	tool := NewDataAnalysisTool(
		nil,
		&mapKnowledgeService{docs: map[string]*types.Knowledge{knowledge.ID: knowledge}},
		nil,
		&diskFileService{},
		db,
		"test-5626-doc",
	)
	ctx := context.Background()
	t.Cleanup(func() { tool.Cleanup(ctx) })

	_, err := tool.LoadFromKnowledge(ctx, knowledge)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported file type: doc (supported types: csv, xlsx, xls, docx)")
}
