package dingtalk

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
)

const (
	// sheetRowChunk is how many rows one ranges request asks for. The ranges
	// API takes a single A1-style range, and one request for a whole large
	// sheet would exceed the connector's response size limit, so cells are read
	// in row windows of this height.
	sheetRowChunk = 200

	// maxSheetRows bounds how many rows of one sheet are ingested. It is
	// deliberately far above real data: sheets in this tenant measure up to
	// 2231 rows x 40 columns (and one live workbook holds a 3385-row sheet),
	// and all of that must sync in full. The cap only stops a pathological or
	// runaway sheet from making one sync unbounded.
	maxSheetRows = 10_000

	// maxSheetColumns bounds how many columns of one sheet are ingested, for
	// the same reason. The widest live sheet is 40 columns.
	maxSheetColumns = 256
)

// sheetRead is the outcome of reading one sheet's cells.
type sheetRead struct {
	Rows [][]string

	// DroppedRowsFrom/To and DroppedColumnsFrom/To are the 1-based extents the
	// caps left unread. Zero means the sheet was read in full.
	DroppedRowsFrom, DroppedRowsTo       int
	DroppedColumnsFrom, DroppedColumnsTo int
}

func (r sheetRead) truncated() bool {
	return r.DroppedRowsFrom > 0 || r.DroppedColumnsFrom > 0
}

// droppedExtents names what a cap left behind, for the sync log.
func (r sheetRead) droppedExtents() string {
	var parts []string
	if r.DroppedRowsFrom > 0 {
		parts = append(parts, fmt.Sprintf("rows %d-%d", r.DroppedRowsFrom, r.DroppedRowsTo))
	}
	if r.DroppedColumnsFrom > 0 {
		parts = append(parts, fmt.Sprintf("columns %d-%d", r.DroppedColumnsFrom, r.DroppedColumnsTo))
	}
	return strings.Join(parts, " and ")
}

// renderWorkbook renders one native DingTalk spreadsheet (axls) as a single
// markdown document. A DingTalk workbook is one document to its users and one
// FetchedItem maps to one knowledge entry, so every non-empty sheet is combined
// here under its own heading rather than synced as a separate item.
func renderWorkbook(
	ctx context.Context,
	api dingTalkAPI,
	workbookID string,
	title string,
) (renderResult, error) {
	sheets, err := api.listSheets(ctx, workbookID)
	if err != nil {
		return renderResult{}, err
	}

	var builder strings.Builder
	if heading := headingText(title); heading != "" {
		builder.WriteString("# ")
		builder.WriteString(heading)
		builder.WriteString("\n\n")
	}
	for index, listed := range sheets {
		sheetID := strings.TrimSpace(listed.ID)
		if sheetID == "" {
			continue
		}
		// The listing carries names only; the per-sheet response reports how
		// far the real data reaches.
		info, err := api.sheetInfo(ctx, workbookID, sheetID)
		if err != nil {
			return renderResult{}, err
		}
		read, err := readSheet(ctx, api, workbookID, sheetID, info)
		if err != nil {
			return renderResult{}, err
		}
		if !hasSheetContent(read.Rows) {
			// An empty sheet contributes no heading and no empty table; it is
			// not a failure, there is simply nothing in it.
			continue
		}
		if read.truncated() {
			// Deterministic data loss must be loud: a truncated sheet can never
			// become complete on a later sync, so it is reported instead of
			// being silently shortened.
			logger.Warnf(ctx,
				"[DingTalk] sheet %q in workbook %s exceeds the %d row x %d column ingest cap: "+
					"dropped %s; raise maxSheetRows/maxSheetColumns to sync it in full",
				listed.Name, workbookID, maxSheetRows, maxSheetColumns, read.droppedExtents())
		}

		name := strings.TrimSpace(listed.Name)
		if name == "" {
			name = strings.TrimSpace(info.Name)
		}
		if name == "" {
			name = fmt.Sprintf("Sheet %d", index+1)
		}
		fmt.Fprintf(&builder, "## %s\n\n", headingText(name))
		renderTable(&builder, read.Rows)
	}

	markdown := strings.TrimSpace(builder.String())
	if markdown != "" {
		markdown += "\n"
	}
	return renderResult{Markdown: markdown}, nil
}

// readSheet reads one sheet's populated cells in row windows. The read is
// bounded by the sheet's last non-empty row and column, so no request ever asks
// for rows past the data.
func readSheet(
	ctx context.Context,
	api dingTalkAPI,
	workbookID, sheetID string,
	info sheet,
) (sheetRead, error) {
	lastRow := info.LastNonEmptyRow
	lastColumn := info.LastNonEmptyColumn
	if lastRow <= 0 || lastColumn <= 0 {
		// The API reports -1 for an empty sheet. Falling back to the nominal
		// rowCount/columnCount would request rows past the data, which is
		// exactly what the last-non-empty fields exist to avoid.
		return sheetRead{}, nil
	}

	var read sheetRead
	if lastRow > maxSheetRows {
		read.DroppedRowsFrom, read.DroppedRowsTo = maxSheetRows+1, lastRow
		lastRow = maxSheetRows
	}
	if lastColumn > maxSheetColumns {
		read.DroppedColumnsFrom, read.DroppedColumnsTo = maxSheetColumns+1, lastColumn
		lastColumn = maxSheetColumns
	}

	lastColumnName := columnName(lastColumn)
	for start := 1; start <= lastRow; start += sheetRowChunk {
		end := start + sheetRowChunk - 1
		if end > lastRow {
			end = lastRow
		}
		ranges := fmt.Sprintf("A%d:%s%d", start, lastColumnName, end)
		rows, err := api.sheetRange(ctx, workbookID, sheetID, ranges)
		if err != nil {
			// Hand the failure back untouched: the workbook is reported as a
			// failed item and retried next sync, so half of its sheets are
			// never ingested as if the rest did not exist.
			return sheetRead{}, err
		}
		// A response can only ever describe the window that was requested.
		if window := end - start + 1; len(rows) > window {
			rows = rows[:window]
		}
		read.Rows = append(read.Rows, rows...)
	}
	return read, nil
}

// hasSheetContent reports whether any cell carries text. A sheet whose extent
// fields claim data but whose cells all come back blank is treated as empty
// rather than rendered as a table of blank cells.
func hasSheetContent(rows [][]string) bool {
	for _, row := range rows {
		for _, cell := range row {
			if strings.TrimSpace(cell) != "" {
				return true
			}
		}
	}
	return false
}

// columnName renders a 1-based column index as the A1-style letters the ranges
// API expects (1 → A, 27 → AA, 40 → AN), so sheets wider than column Z are read
// whole instead of being cut at the first window.
func columnName(index int) string {
	if index < 1 {
		index = 1
	}
	name := ""
	for index > 0 {
		index--
		name = string(rune('A'+index%26)) + name
		index /= 26
	}
	return name
}

// headingText collapses inline whitespace so a sheet name can never break out
// of its markdown heading, and escapes it like every other rendered title.
func headingText(value string) string {
	return escapeText(strings.Join(strings.Fields(value), " "))
}
