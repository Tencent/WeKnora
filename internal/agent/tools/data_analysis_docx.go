package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
)

// docxTableIndexColumn is the name of the synthetic column that identifies
// which Word table a row came from when multiple tables are unioned together.
// It mirrors excelSheetNameColumn for multi-sheet workbooks.
const docxTableIndexColumn = "__table_index"

// maxDocxDocumentXMLBytes bounds the word/document.xml entry read from one
// .docx file, mirroring the bound sourceloc applies to the same entry.
const maxDocxDocumentXMLBytes = 64 << 20

// maxDocxTableRows bounds the total number of data rows extracted from one
// .docx file, mirroring the bound sourceloc applies to structural units.
const maxDocxTableRows = 200000

// docxTable is one Word table. rows[0] is the header row; the remaining rows
// are data rows. Cell values are plain text with whitespace collapsed.
type docxTable struct {
	rows [][]string
}

// LoadFromDocx loads the tables of a Word document into a DuckDB table and
// returns the table schema.
//
// Every table with a header row and at least one data row is loaded;
// tables with a header only are skipped. Like the Excel loader, tables are
// unioned into a single table (UNION ALL BY NAME semantics: a column a table
// does not have becomes empty) and a synthetic '__table_index' column
// (1-based, in document order) identifies the source table so downstream SQL
// can filter or aggregate per table. The first row of each table is treated
// as its header, and every value is loaded as text, matching the CSV and
// Excel loaders (all_varchar=true).
//
// The document is parsed directly from its OOXML container with the standard
// library (archive/zip + encoding/xml); no new dependency is introduced.
// Legacy binary .doc files are not OOXML containers and stay unsupported.
func (t *DataAnalysisTool) LoadFromDocx(ctx context.Context, filename string, tableName string) (*TableSchema, error) {
	logger.Infof(ctx, "[Tool][DataAnalysis] Loading DOCX file '%s' into table '%s' for session %s",
		filename, tableName, t.sessionID)

	if t.loadedTables[tableName] {
		return t.LoadFromTable(ctx, tableName)
	}

	tables, err := extractDocxTables(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to extract tables from DOCX file: %w", err)
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("docx document contains no data tables")
	}

	csvBytes, err := docxTablesToCSV(tables)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize DOCX tables: %w", err)
	}

	tmp, err := os.CreateTemp("", "weknora-data-analysis-*.csv")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp CSV for DOCX tables: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(csvBytes); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("failed to write temp CSV for DOCX tables: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("failed to write temp CSV for DOCX tables: %w", err)
	}

	totalRows := 0
	for _, tb := range tables {
		totalRows += len(tb.rows) - 1
	}
	logger.Infof(ctx,
		"[Tool][DataAnalysis] Extracted %d tables (%d data rows) from DOCX file '%s' for session %s",
		len(tables), totalRows, filename, t.sessionID)

	// Reuse the CSV loader so quoting, header handling and table creation
	// stay identical to the CSV path; the temp file is removed above.
	return t.LoadFromCSV(ctx, tmpPath, tableName)
}

// extractDocxTables returns the data tables of a .docx file in document
// order. Only body-level tables count; a table nested inside a cell
// contributes its text to the enclosing cell instead of becoming a table of
// its own. Deleted text (w:delText) and field instructions (w:instrText) are
// ignored. A cell spanning several grid columns (w:gridSpan) occupies the
// first column with its text and the rest with empty cells.
func extractDocxTables(path string) ([]docxTable, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		return nil, fmt.Errorf("not a valid .docx file: %w", err)
	}
	var doc []byte
	for _, entry := range zr.File {
		if entry.Name != "word/document.xml" {
			continue
		}
		if entry.UncompressedSize64 > maxDocxDocumentXMLBytes {
			return nil, fmt.Errorf("word/document.xml exceeds %d bytes", maxDocxDocumentXMLBytes)
		}
		rc, err := entry.Open()
		if err != nil {
			return nil, err
		}
		doc, err = io.ReadAll(io.LimitReader(rc, maxDocxDocumentXMLBytes))
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		break
	}
	if doc == nil {
		return nil, fmt.Errorf("word/document.xml not found")
	}
	return parseDocxTables(doc)
}

// parseDocxTables streams word/document.xml and extracts its body-level
// tables. It follows the same token-walking style as sourceloc's docxUnits.
func parseDocxTables(doc []byte) ([]docxTable, error) {
	dec := xml.NewDecoder(bytes.NewReader(doc))
	var (
		tables   []docxTable
		table    *docxTable
		row      []string
		inRow    bool
		cell     strings.Builder
		cellSpan int
		inCell   bool
		inText   bool
		skipText int
		depth    int // element depth below w:body; sdt wrappers do not count
		inBody   bool
		tblDepth int
		dataRows int
	)
	flushCell := func() {
		if !inCell {
			return
		}
		value := normalizeDocxCellText(cell.String())
		cell.Reset()
		row = append(row, value)
		for i := 1; i < cellSpan; i++ {
			row = append(row, "")
		}
		inCell = false
	}
	flushRow := func() {
		if !inRow {
			return
		}
		inRow = false
		empty := true
		for _, c := range row {
			if c != "" {
				empty = false
				break
			}
		}
		if empty || table == nil {
			return
		}
		table.rows = append(table.rows, row)
		dataRows++
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse word/document.xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := t.Name.Local
			if !inBody {
				if name == "body" {
					inBody = true
				}
				continue
			}
			if name == "sdt" || name == "sdtContent" || name == "sdtPr" || name == "sdtEndPr" {
				if name == "sdtPr" || name == "sdtEndPr" {
					skipText++
				}
				continue
			}
			depth++
			switch name {
			case "tbl":
				tblDepth++
				if tblDepth == 1 && depth == 1 {
					table = &docxTable{}
				}
			case "tr":
				if tblDepth == 1 && table != nil {
					row = nil
					inRow = true
				}
			case "tc":
				if tblDepth == 1 && inRow {
					cell.Reset()
					cellSpan = 1
					inCell = true
				}
			case "gridSpan":
				if inCell {
					cellSpan = docxGridSpan(t.Attr)
				}
			case "t":
				if inCell && skipText == 0 {
					inText = true
				}
			case "tab", "br", "cr":
				if inCell {
					cell.WriteByte(' ')
				}
			case "delText", "instrText":
				skipText++
			}
		case xml.EndElement:
			name := t.Name.Local
			if !inBody {
				continue
			}
			if name == "body" {
				inBody = false
				continue
			}
			if name == "sdt" || name == "sdtContent" || name == "sdtPr" || name == "sdtEndPr" {
				if name == "sdtPr" || name == "sdtEndPr" {
					skipText--
				}
				continue
			}
			switch name {
			case "t":
				inText = false
			case "delText", "instrText":
				skipText--
			case "p":
				if inCell {
					cell.WriteByte(' ')
				}
			case "tc":
				if inCell && tblDepth == 1 {
					flushCell()
				}
			case "tr":
				if tblDepth == 1 {
					flushRow()
				}
			case "tbl":
				tblDepth--
				if tblDepth == 0 && table != nil {
					if len(table.rows) >= 2 {
						tables = append(tables, *table)
					}
					table = nil
				}
			}
			depth--
		case xml.CharData:
			if inText && inCell && skipText == 0 {
				cell.Write(t)
			}
		}
		if dataRows > maxDocxTableRows {
			return nil, fmt.Errorf("docx document has more than %d table rows", maxDocxTableRows)
		}
	}
	return tables, nil
}

// docxGridSpan reads the w:val of a w:gridSpan element; anything missing or
// malformed counts as a single column.
func docxGridSpan(attrs []xml.Attr) int {
	for _, attr := range attrs {
		if attr.Name.Local != "val" {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(attr.Value)); err == nil && n > 0 {
			return n
		}
		return 1
	}
	return 1
}

// normalizeDocxCellText trims a cell and collapses its internal whitespace
// to single spaces so multi-run and multi-paragraph cells stay comparable.
func normalizeDocxCellText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// docxTableWidth is the widest row of a table; data rows longer than the
// header extend it so no cell is dropped.
func docxTableWidth(tb docxTable) int {
	width := 0
	for _, row := range tb.rows {
		if len(row) > width {
			width = len(row)
		}
	}
	return width
}

// normalizeDocxTableHeader turns a raw header row into unique column names:
// cells are trimmed, empty cells become column_<position> (1-based), and
// repeated names get a _2, _3, ... suffix until unique.
func normalizeDocxTableHeader(cells []string, width int) []string {
	header := make([]string, 0, width)
	seen := make(map[string]struct{}, width)
	for i := 0; i < width; i++ {
		base := ""
		if i < len(cells) {
			base = strings.TrimSpace(cells[i])
		}
		if base == "" {
			base = fmt.Sprintf("column_%d", i+1)
		}
		name := base
		for suffix := 2; ; suffix++ {
			if _, ok := seen[name]; !ok {
				break
			}
			name = fmt.Sprintf("%s_%d", base, suffix)
		}
		seen[name] = struct{}{}
		header = append(header, name)
	}
	return header
}

// docxTablesToCSV serializes tables into one CSV document: the header is the
// union of the tables' normalized headers in first-appearance order, rows a
// table does not cover stay empty, and a trailing __table_index column holds
// the 1-based table number. encoding/csv quotes cells that need it.
func docxTablesToCSV(tables []docxTable) ([]byte, error) {
	var header []string
	index := make(map[string]int)
	perTable := make([][]string, len(tables))
	for i, tb := range tables {
		normalized := normalizeDocxTableHeader(tb.rows[0], docxTableWidth(tb))
		perTable[i] = normalized
		for _, name := range normalized {
			if _, ok := index[name]; !ok {
				index[name] = len(header)
				header = append(header, name)
			}
		}
	}
	indexColumn := docxTableIndexColumn
	for suffix := 2; ; suffix++ {
		if _, ok := index[indexColumn]; !ok {
			break
		}
		indexColumn = fmt.Sprintf("%s_%d", docxTableIndexColumn, suffix)
	}
	header = append(header, indexColumn)

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(header); err != nil {
		return nil, err
	}
	for i, tb := range tables {
		for _, row := range tb.rows[1:] {
			rec := make([]string, len(header))
			for j, name := range perTable[i] {
				if j < len(row) {
					rec[index[name]] = row[j]
				}
			}
			rec[len(header)-1] = strconv.Itoa(i + 1)
			if err := w.Write(rec); err != nil {
				return nil, err
			}
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
