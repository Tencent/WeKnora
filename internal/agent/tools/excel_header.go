package tools

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

const (
	// excelHeaderSampleNonEmptyRows bounds how many leading non-empty rows are
	// sampled from each worksheet during header detection.
	excelHeaderSampleNonEmptyRows = 30
	// excelMaxPreambleRows caps how many leading non-empty title/note rows may
	// sit above the real header row.
	excelMaxPreambleRows = 10
	// excelMaxRangeColumns guards DuckDB read_xlsx against OOM on pathological
	// sheets with a stray cell at column XFD (16384).
	excelMaxRangeColumns = 1024
)

type excelProbeCell struct {
	col          int
	isString     bool
	isTextHeader bool
	hasFormula   bool
	sharedIdx    int
	inlineVal    string
}

type excelProbeRow struct {
	rowNum int
	cells  []excelProbeCell
}

type excelMergeRange struct {
	startCol, startRow, endCol, endRow int
}

type excelTailBounds struct {
	hasRows     bool
	firstRowNum int
	minCol      int
	maxCol      int
}

// xmlExcelRichText decodes both <si> (sharedStrings) and <is> (inlineStr),
// ignoring phonetic guide <rPh> elements automatically.
type xmlExcelRichText struct {
	Text string   `xml:"t"`
	Runs []string `xml:"r>t"`
}

func (rt *xmlExcelRichText) String() string {
	if rt == nil {
		return ""
	}
	if len(rt.Runs) == 0 {
		return rt.Text
	}
	var b strings.Builder
	b.WriteString(rt.Text)
	for _, run := range rt.Runs {
		b.WriteString(run)
	}
	return b.String()
}

type xmlExcelCell struct {
	Ref     string            `xml:"r,attr"`
	Type    string            `xml:"t,attr"`
	Value   string            `xml:"v"`
	Formula *struct{}         `xml:"f"`
	Inline  *xmlExcelRichText `xml:"is"`
}

type xmlExcelRow struct {
	Num   int            `xml:"r,attr"`
	Cells []xmlExcelCell `xml:"c"`
}

// detectExcelSheetRanges inspects OOXML worksheets to find leading title/banner
// or note rows above the real header row. It returns a map of sheet name ->
// DuckDB read_xlsx range (e.g. "A2:D") only for sheets whose real header starts
// after at least one non-empty preamble row; normal sheets are omitted so they
// retain the exact legacy read_xlsx SQL form.
func detectExcelSheetRanges(
	ctx context.Context, filename string, sheetNames []string,
) (map[string]string, error) {
	if len(sheetNames) == 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	book, err := zip.OpenReader(filename)
	if err != nil {
		return nil, err
	}
	defer func() { _ = book.Close() }()

	sheetPaths, sharedStringsPath, err := resolveExcelWorkbookParts(book, sheetNames)
	if err != nil {
		return nil, err
	}

	samples := make(map[string][]excelProbeRow, len(sheetNames))
	neededShared := make(map[int]struct{})
	for _, name := range sheetNames {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		partPath := sheetPaths[name]
		if partPath == "" {
			continue
		}
		rows, err := readZipPart(book, partPath, func(r io.Reader) ([]excelProbeRow, error) {
			return scanExcelSheetSample(ctx, r, excelHeaderSampleNonEmptyRows)
		})
		if err != nil {
			return nil, fmt.Errorf("sample worksheet %q: %w", name, err)
		}
		samples[name] = rows
		for _, row := range rows {
			for _, cell := range row.cells {
				if cell.sharedIdx >= 0 {
					neededShared[cell.sharedIdx] = struct{}{}
				}
			}
		}
	}

	var shared map[int]string
	if len(neededShared) > 0 {
		shared, err = loadNeededSharedStrings(ctx, book, sharedStringsPath, neededShared)
		if err != nil {
			return nil, err
		}
	}

	blankShared := make(map[int]bool)
	for idx, val := range shared {
		if strings.TrimSpace(val) == "" {
			blankShared[idx] = true
		}
	}

	ranges := make(map[string]string)
	for _, name := range sheetNames {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rows := resolveProbeRows(samples[name], shared)
		if !sheetNeedsPreambleInspection(rows) {
			continue
		}
		lastSampleRow := rows[len(rows)-1].rowNum
		type scanOut struct {
			merges []excelMergeRange
			tail   excelTailBounds
		}
		out, err := readZipPart(book, sheetPaths[name], func(r io.Reader) (scanOut, error) {
			m, t, scanErr := scanExcelSheetMergesAndTail(ctx, r, lastSampleRow, blankShared)
			return scanOut{merges: m, tail: t}, scanErr
		})
		if err != nil {
			return nil, fmt.Errorf("inspect merges of worksheet %q: %w", name, err)
		}
		if rng := detectExcelHeaderRange(rows, out.merges, out.tail); rng != "" {
			ranges[name] = rng
		}
	}

	if len(ranges) == 0 {
		return nil, nil
	}
	return ranges, nil
}

func readZipPart[T any](book *zip.ReadCloser, partPath string, fn func(io.Reader) (T, error)) (T, error) {
	var zero T
	r, err := book.Open(partPath)
	if err != nil {
		return zero, err
	}
	val, fnErr := fn(r)
	closeErr := r.Close()
	if fnErr != nil {
		return zero, fnErr
	}
	return val, closeErr
}

func loadNeededSharedStrings(
	ctx context.Context, book *zip.ReadCloser, sharedPath string, needed map[int]struct{},
) (map[int]string, error) {
	if sharedPath == "" {
		sharedPath = "xl/sharedStrings.xml"
	}
	r, err := book.Open(sharedPath)
	if err != nil {
		return nil, nil
	}
	shared, readErr := readNeededSharedStrings(ctx, r, needed)
	closeErr := r.Close()
	if readErr != nil {
		return nil, readErr
	}
	return shared, closeErr
}

// readNeededSharedStrings streams xl/sharedStrings.xml and extracts only the
// requested indices, stopping immediately after the highest needed index.
func readNeededSharedStrings(
	ctx context.Context, r io.Reader, needed map[int]struct{},
) (map[int]string, error) {
	maxIdx := -1
	for idx := range needed {
		if idx > maxIdx {
			maxIdx = idx
		}
	}
	if maxIdx < 0 {
		return nil, nil
	}

	result := make(map[int]string, len(needed))
	decoder := xml.NewDecoder(r)
	siIdx := -1
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		token, err := decoder.Token()
		if err == io.EOF {
			return result, nil
		}
		if err != nil {
			return nil, err
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "si" {
			continue
		}
		siIdx++
		if siIdx > maxIdx {
			return result, nil
		}
		if _, want := needed[siIdx]; !want {
			if err := decoder.Skip(); err != nil {
				return nil, err
			}
			continue
		}
		var item xmlExcelRichText
		if err := decoder.DecodeElement(&item, &start); err != nil {
			return nil, err
		}
		result[siIdx] = item.String()
	}
}

func decodeXMLRowToProbe(raw *xmlExcelRow, prevRow int) (excelProbeRow, int) {
	rowNum := raw.Num
	explicitRow := rowNum > 0
	if !explicitRow {
		rowNum = prevRow + 1
	}

	cells := make([]excelProbeCell, 0, len(raw.Cells))
	prevCol := 0
	for _, c := range raw.Cells {
		col := prevCol + 1
		if c.Ref != "" {
			if parsedCol, parsedRow, err := excelize.CellNameToCoordinates(c.Ref); err == nil {
				col = parsedCol
				if !explicitRow && parsedRow > 0 {
					rowNum = parsedRow
					explicitRow = true
				}
			}
		}
		prevCol = col

		cell := excelProbeCell{col: col, sharedIdx: -1, hasFormula: c.Formula != nil}
		hasContent := cell.hasFormula
		switch {
		case c.Inline != nil:
			cell.isString = true
			cell.inlineVal = c.Inline.String()
			hasContent = cell.hasFormula || strings.TrimSpace(cell.inlineVal) != ""
		case c.Type == "s":
			if idx, err := strconv.Atoi(strings.TrimSpace(c.Value)); err == nil && idx >= 0 {
				cell.isString = true
				cell.sharedIdx = idx
				hasContent = true
			}
		case c.Type == "str":
			cell.isString = true
			cell.inlineVal = c.Value
			hasContent = cell.hasFormula || strings.TrimSpace(cell.inlineVal) != ""
		default:
			if strings.TrimSpace(c.Value) != "" {
				hasContent = true
			}
		}
		if hasContent {
			cells = append(cells, cell)
		}
	}
	return excelProbeRow{rowNum: rowNum, cells: cells}, rowNum
}

// scanExcelSheetSample streams the leading non-empty rows of a worksheet XML
// without parsing the rest of the sheet.
func scanExcelSheetSample(ctx context.Context, r io.Reader, maxRows int) ([]excelProbeRow, error) {
	decoder := xml.NewDecoder(r)
	rows := make([]excelProbeRow, 0, min(maxRows, 16))
	inSheetData := false
	prevRow := 0

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		token, err := decoder.Token()
		if err == io.EOF {
			return rows, nil
		}
		if err != nil {
			return nil, err
		}
		switch v := token.(type) {
		case xml.StartElement:
			switch v.Name.Local {
			case "sheetData":
				inSheetData = true
			case "row":
				if !inSheetData {
					continue
				}
				var raw xmlExcelRow
				if err := decoder.DecodeElement(&raw, &v); err != nil {
					return nil, err
				}
				row, nextPrev := decodeXMLRowToProbe(&raw, prevRow)
				prevRow = nextPrev
				if len(row.cells) > 0 {
					rows = append(rows, row)
					if len(rows) >= maxRows {
						return rows, nil
					}
				}
			}
		case xml.EndElement:
			if v.Name.Local == "sheetData" && inSheetData {
				return rows, nil
			}
		}
	}
}

// scanExcelSheetMergesAndTail streams a candidate preamble worksheet once to
// collect merge cells intersecting the sampled top rows and column bounds of
// any contiguous data rows continuing past lastSampleRow.
func scanExcelSheetMergesAndTail(
	ctx context.Context, r io.Reader, lastSampleRow int, blankShared map[int]bool,
) ([]excelMergeRange, excelTailBounds, error) {
	decoder := xml.NewDecoder(r)
	var merges []excelMergeRange
	var tail excelTailBounds

	inSheetData := false
	inMergeCells := false
	prevRow := 0
	lastContiguousRow := lastSampleRow
	tailDone := false

	for {
		if err := ctx.Err(); err != nil {
			return nil, excelTailBounds{}, err
		}
		token, err := decoder.Token()
		if err == io.EOF {
			return merges, tail, nil
		}
		if err != nil {
			return nil, excelTailBounds{}, err
		}
		switch v := token.(type) {
		case xml.StartElement:
			switch v.Name.Local {
			case "sheetData":
				inSheetData = true
			case "mergeCells":
				inMergeCells = true
			case "mergeCell":
				if inMergeCells {
					if m, ok := parseMergeRef(xmlAttr(v.Attr, "ref")); ok && m.startRow <= lastSampleRow {
						merges = append(merges, m)
					}
				}
			case "row":
				if !inSheetData {
					continue
				}
				if tailDone {
					if err := decoder.Skip(); err != nil {
						return nil, excelTailBounds{}, err
					}
					continue
				}
				rowNum := prevRow + 1
				if attr := xmlAttr(v.Attr, "r"); attr != "" {
					if parsed, convErr := strconv.Atoi(attr); convErr == nil && parsed > 0 {
						rowNum = parsed
					}
				}
				prevRow = rowNum
				if rowNum <= lastSampleRow {
					if err := decoder.Skip(); err != nil {
						return nil, excelTailBounds{}, err
					}
					continue
				}
				if rowNum != lastContiguousRow+1 {
					tailDone = true
					if err := decoder.Skip(); err != nil {
						return nil, excelTailBounds{}, err
					}
					continue
				}
				var raw xmlExcelRow
				if err := decoder.DecodeElement(&raw, &v); err != nil {
					return nil, excelTailBounds{}, err
				}
				probe, nextPrev := decodeXMLRowToProbe(&raw, prevRow-1)
				prevRow = nextPrev
				rMin, rMax := 0, 0
				for _, cell := range probe.cells {
					if cell.sharedIdx >= 0 && blankShared[cell.sharedIdx] && !cell.hasFormula {
						continue
					}
					if rMin == 0 || cell.col < rMin {
						rMin = cell.col
					}
					if cell.col > rMax {
						rMax = cell.col
					}
				}
				if rMin == 0 {
					tailDone = true
					continue
				}
				if !tail.hasRows {
					tail = excelTailBounds{hasRows: true, firstRowNum: probe.rowNum, minCol: rMin, maxCol: rMax}
				} else {
					tail.minCol = min(tail.minCol, rMin)
					tail.maxCol = max(tail.maxCol, rMax)
				}
				lastContiguousRow = probe.rowNum
			}
		case xml.EndElement:
			switch v.Name.Local {
			case "sheetData":
				inSheetData = false
			case "mergeCells":
				if inMergeCells {
					return merges, tail, nil
				}
			}
		}
	}
}

func resolveProbeRows(rows []excelProbeRow, shared map[int]string) []excelProbeRow {
	if len(rows) == 0 {
		return nil
	}
	resolved := make([]excelProbeRow, 0, len(rows))
	for _, row := range rows {
		cells := make([]excelProbeCell, 0, len(row.cells))
		for _, cell := range row.cells {
			if cell.sharedIdx >= 0 {
				if val, ok := shared[cell.sharedIdx]; ok {
					trimmed := strings.TrimSpace(val)
					if trimmed == "" && !cell.hasFormula {
						continue
					}
					cell.isTextHeader = trimmed != "" && !isNumericString(trimmed)
				} else {
					cell.isTextHeader = true
				}
			} else if cell.isString {
				trimmed := strings.TrimSpace(cell.inlineVal)
				if trimmed == "" && !cell.hasFormula {
					continue
				}
				cell.isTextHeader = trimmed != "" && !isNumericString(trimmed)
			}
			cells = append(cells, cell)
		}
		if len(cells) > 0 {
			resolved = append(resolved, excelProbeRow{rowNum: row.rowNum, cells: cells})
		}
	}
	return resolved
}

// sheetNeedsPreambleInspection is the fast-path gate: normal worksheets whose
// first non-empty row already matches or exceeds the width of subsequent rows
// (or single-column sheets) skip merge/tail scanning completely.
func sheetNeedsPreambleInspection(rows []excelProbeRow) bool {
	if len(rows) < 2 {
		return false
	}
	maxLater := 0
	for _, row := range rows[1:] {
		if len(row.cells) > maxLater {
			maxLater = len(row.cells)
		}
	}
	return maxLater >= 2 && len(rows[0].cells) < maxLater
}

// detectExcelHeaderRange returns the DuckDB read_xlsx range (e.g. "A2:D") when
// at least one non-empty preamble row sits above the true header row, or ""
// when the worksheet should keep the default header=true load path.
func detectExcelHeaderRange(
	rows []excelProbeRow, merges []excelMergeRange, tail excelTailBounds,
) string {
	if len(rows) < 2 {
		return ""
	}
	headerIdx := findExcelHeaderRowIndex(rows, merges)
	if headerIdx <= 0 {
		return ""
	}

	headerRow := rows[headerIdx]
	headerMinCol, headerMaxCol := rowColBounds(headerRow)
	if headerMinCol <= 0 || headerMaxCol <= headerMinCol {
		return ""
	}

	minCol, maxCol := headerMinCol, headerMaxCol
	prevRowNum := headerRow.rowNum
	contiguousToEnd := true
	for i := headerIdx + 1; i < len(rows); i++ {
		if rows[i].rowNum != prevRowNum+1 {
			contiguousToEnd = false
			break
		}
		rMin, rMax := rowColBounds(rows[i])
		if rMin > 0 && rMin < minCol {
			minCol = rMin
		}
		if rMax > maxCol {
			maxCol = rMax
		}
		prevRowNum = rows[i].rowNum
	}
	if contiguousToEnd && tail.hasRows && tail.firstRowNum == prevRowNum+1 {
		if tail.minCol > 0 && tail.minCol < minCol {
			minCol = tail.minCol
		}
		if tail.maxCol > maxCol {
			maxCol = tail.maxCol
		}
	}

	if maxCol-minCol+1 > excelMaxRangeColumns {
		minCol, maxCol = headerMinCol, headerMaxCol
		if maxCol-minCol+1 > excelMaxRangeColumns {
			return ""
		}
	}

	startColName, err1 := excelize.ColumnNumberToName(minCol)
	endColName, err2 := excelize.ColumnNumberToName(maxCol)
	if err1 != nil || err2 != nil {
		return ""
	}
	return fmt.Sprintf("%s%d:%s", startColName, headerRow.rowNum, endColName)
}

func findExcelHeaderRowIndex(rows []excelProbeRow, merges []excelMergeRange) int {
	limit := min(len(rows)-1, excelMaxPreambleRows)

	hasMergedBanner := false
	h := 0
	for h < limit {
		row := rows[h]
		switch len(row.cells) {
		case 1:
			if rowHasNonBannerVerticalMerge(row, merges) {
				goto done
			}
			nextHasMerged := hasMergedBanner || rowHasHorizontalBannerMerge(row, merges)
			if !hasHeaderTargetAfterSingleCell(rows, h, limit, merges, nextHasMerged) {
				goto done
			}
			hasMergedBanner = nextHasMerged
			h++
		case 2:
			if !isSparseMetadataRow(row, rows[h+1], merges, h > 0) {
				goto done
			}
			h++
		default:
			goto done
		}
	}

done:
	if h == 0 || !isValidHeaderCandidate(rows[h], hasMergedBanner) {
		return 0
	}
	return h
}

func hasHeaderTargetAfterSingleCell(
	rows []excelProbeRow, idx, limit int, merges []excelMergeRange, hasMergedBanner bool,
) bool {
	for t := idx + 1; t <= limit && t < len(rows); t++ {
		cand := rows[t]
		if len(cand.cells) == 1 {
			if rowHasNonBannerVerticalMerge(cand, merges) {
				return false
			}
			if rowHasHorizontalBannerMerge(cand, merges) {
				hasMergedBanner = true
			}
			continue
		}
		if len(cand.cells) == 2 && t+1 <= limit && t+1 < len(rows) &&
			isSparseMetadataRow(cand, rows[t+1], merges, true) {
			return true
		}
		return isValidHeaderCandidate(cand, hasMergedBanner)
	}
	return false
}

// isSparseMetadataRow identifies a 2-cell corner metadata line (e.g. "部门：..."
// on the left and "单位：万元" on the right) sitting directly above a wide text
// header row, while never matching multi-level grouped headers or normal tables.
func isSparseMetadataRow(
	row, next excelProbeRow, merges []excelMergeRange, prevSkippedBanner bool,
) bool {
	if len(row.cells) != 2 || len(next.cells) < 4 || !isTextHeaderRow(next) {
		return false
	}
	if rowHasNonBannerVerticalMerge(row, merges) || rowCoveredCols(row, merges) > 2 {
		return false
	}
	minCol, maxCol := rowColBounds(row)
	return prevSkippedBanner || maxCol-minCol+1 > len(row.cells)
}

func isValidHeaderCandidate(row excelProbeRow, hasMergedBanner bool) bool {
	return len(row.cells) >= 2 && (hasMergedBanner || isTextHeaderRow(row))
}

func isTextHeaderRow(row excelProbeRow) bool {
	textCount := 0
	for _, cell := range row.cells {
		if cell.isTextHeader {
			textCount++
		}
	}
	return textCount*2 > len(row.cells)
}

func rowHasHorizontalBannerMerge(row excelProbeRow, merges []excelMergeRange) bool {
	if len(row.cells) != 1 {
		return false
	}
	col := row.cells[0].col
	for _, m := range merges {
		if m.startRow <= row.rowNum && row.rowNum <= m.endRow &&
			m.startCol <= col && col <= m.endCol && m.endCol > m.startCol {
			return true
		}
	}
	return false
}

func rowHasNonBannerVerticalMerge(row excelProbeRow, merges []excelMergeRange) bool {
	singleCell := len(row.cells) == 1
	for _, m := range merges {
		if m.startRow == row.rowNum && m.endRow > row.rowNum {
			if singleCell && m.endCol > m.startCol {
				continue
			}
			return true
		}
	}
	return false
}

func rowCoveredCols(row excelProbeRow, merges []excelMergeRange) int {
	covered := make(map[int]struct{}, len(row.cells))
	for _, cell := range row.cells {
		covered[cell.col] = struct{}{}
		for _, m := range merges {
			if m.startRow <= row.rowNum && row.rowNum <= m.endRow &&
				m.startCol <= cell.col && cell.col <= m.endCol {
				for c := m.startCol; c <= m.endCol; c++ {
					covered[c] = struct{}{}
				}
			}
		}
	}
	return len(covered)
}

func rowColBounds(row excelProbeRow) (int, int) {
	if len(row.cells) == 0 {
		return 0, 0
	}
	minCol, maxCol := row.cells[0].col, row.cells[0].col
	for _, cell := range row.cells[1:] {
		minCol = min(minCol, cell.col)
		maxCol = max(maxCol, cell.col)
	}
	return minCol, maxCol
}

func isNumericString(s string) bool {
	cleaned := strings.TrimSpace(s)
	cleaned = strings.TrimLeft(cleaned, "$¥€£")
	cleaned = strings.TrimSuffix(cleaned, "%")
	cleaned = strings.ReplaceAll(cleaned, ",", "")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return false
	}
	val, err := strconv.ParseFloat(cleaned, 64)
	return err == nil && !math.IsNaN(val) && !math.IsInf(val, 0)
}

func xmlAttr(attrs []xml.Attr, local string) string {
	for _, attr := range attrs {
		if attr.Name.Local == local {
			return attr.Value
		}
	}
	return ""
}

func parseMergeRef(ref string) (excelMergeRange, bool) {
	parts := strings.Split(strings.TrimSpace(ref), ":")
	if len(parts) != 2 {
		return excelMergeRange{}, false
	}
	c1, r1, err1 := excelize.CellNameToCoordinates(parts[0])
	c2, r2, err2 := excelize.CellNameToCoordinates(parts[1])
	if err1 != nil || err2 != nil {
		return excelMergeRange{}, false
	}
	if c1 > c2 {
		c1, c2 = c2, c1
	}
	if r1 > r2 {
		r1, r2 = r2, r1
	}
	return excelMergeRange{startCol: c1, startRow: r1, endCol: c2, endRow: r2}, true
}
