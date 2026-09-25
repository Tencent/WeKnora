package core

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
)

// Feishu docx block_type integer enum (subset this connector handles).
// https://open.feishu.cn/document/server-docs/docs/docs/docx-v1/docx-structure
const (
	BlockTypePage      = 1
	BlockTypeText      = 2
	BlockTypeHeading1  = 3
	blockTypeHeading9  = 11
	BlockTypeBullet    = 12
	BlockTypeOrdered   = 13
	BlockTypeCode      = 14
	BlockTypeQuote     = 15
	BlockTypeTodo      = 17
	BlockTypeBitable   = 18
	BlockTypeCallout   = 19
	BlockTypeDivider   = 22
	BlockTypeFile      = 23
	BlockTypeImage     = 27
	BlockTypeSheet     = 30
	BlockTypeTable     = 31
	BlockTypeTableCell = 32
)

// maxDocumentBlocks caps how many blocks a single document contributes, guarding
// against pathological/adversarial documents. Far above any real Feishu doc.
const maxDocumentBlocks = 50000

// TextElement is one inline run inside a text-bearing block.
// TextRun is the text_run payload of a TextElement.
type TextRun struct {
	Content string `json:"content"`
}

type TextElement struct {
	TextRun *TextRun `json:"text_run"`
}

// BlockText is the shared shape of text-bearing blocks (text, headingN, bullet…).
type BlockText struct {
	Elements []TextElement `json:"elements"`
}

// BlockTokenRef is the shared shape of sheet/bitable/image block payloads
// (the JSON tag differs per field; only the token matters).
type BlockTokenRef struct {
	Token string `json:"token"`
}

// BlockFileRef is the file block payload: the attachment token plus its name.
type BlockFileRef struct {
	Token string `json:"token"`
	Name  string `json:"name"`
}

// BlockTableProperty carries the table grid shape.
type BlockTableProperty struct {
	ColumnSize int `json:"column_size"`
}

// BlockTable is the table block payload: cell block IDs plus grid property.
type BlockTable struct {
	Cells    []string            `json:"cells"`
	Property *BlockTableProperty `json:"property"`
}

// DocxBlock is one node in the flat block array returned by the blocks API.
type DocxBlock struct {
	BlockID   string   `json:"block_id"`
	ParentID  string   `json:"parent_id"`
	BlockType int      `json:"block_type"`
	Children  []string `json:"children"`

	Text     *BlockText `json:"text"`
	Heading1 *BlockText `json:"heading1"`
	Heading2 *BlockText `json:"heading2"`
	Heading3 *BlockText `json:"heading3"`
	Heading4 *BlockText `json:"heading4"`
	Heading5 *BlockText `json:"heading5"`
	Heading6 *BlockText `json:"heading6"`
	Heading7 *BlockText `json:"heading7"`
	Heading8 *BlockText `json:"heading8"`
	Heading9 *BlockText `json:"heading9"`
	Bullet   *BlockText `json:"bullet"`
	Ordered  *BlockText `json:"ordered"`
	Code     *BlockText `json:"code"`
	Quote    *BlockText `json:"quote"`
	Todo     *BlockText `json:"todo"`
	Callout  *BlockText `json:"callout"`

	Sheet   *BlockTokenRef `json:"sheet"`
	Bitable *BlockTokenRef `json:"bitable"`
	File    *BlockFileRef  `json:"file"`
	Image   *BlockTokenRef `json:"image"`

	Table *BlockTable `json:"table"`
}

// DocxBlocksData is the data payload of DocxBlocksResponse.
type DocxBlocksData struct {
	Items     []DocxBlock `json:"items"`
	HasMore   bool        `json:"has_more"`
	PageToken string      `json:"page_token"`
}

// DocxBlocksResponse is the response for GET .../documents/:id/blocks.
type DocxBlocksResponse struct {
	ApiResponse
	Data DocxBlocksData `json:"data"`
}

// listDocumentBlocks returns every block of a docx document as a flat,
// pre-order array. Paginates at 500 blocks/page. documentID is the obj_token.
func (c *Client) listDocumentBlocks(ctx context.Context, documentID string) ([]DocxBlock, error) {
	var all []DocxBlock
	pageToken := ""
	for {
		path := fmt.Sprintf("/open-apis/docx/v1/documents/%s/blocks?page_size=500&document_revision_id=-1",
			url.PathEscape(documentID))
		if pageToken != "" {
			path += "&page_token=" + url.QueryEscape(pageToken)
		}
		var resp DocxBlocksResponse
		if err := c.DoRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return nil, fmt.Errorf("list document blocks: %w", err)
		}
		if resp.Code != 0 {
			return nil, fmt.Errorf("list document blocks error: code=%d msg=%s", resp.Code, resp.Msg)
		}
		all = append(all, resp.Data.Items...)
		if len(resp.Data.Items) == 0 {
			// Defensive: a malformed page (empty items but has_more=true and a
			// non-empty page_token) would otherwise loop until the task deadline,
			// burning API quota. There is nothing more to collect, so stop.
			break
		}
		if len(all) >= maxDocumentBlocks {
			logger.Warnf(ctx, "[Feishu] document %s exceeded %d blocks; truncating", documentID, maxDocumentBlocks)
			break
		}
		if !resp.Data.HasMore || resp.Data.PageToken == "" {
			break
		}
		pageToken = resp.Data.PageToken
	}
	return all, nil
}

// maxTableRows caps how many rows of an embedded sheet/bitable are rendered,
// protecting chunking from pathologically large tables. Beyond this the table
// is truncated and the caller annotates the omission.
const maxTableRows = 500

// sheetValueRange is the valueRange payload of sheetValuesData.
type sheetValueRange struct {
	Values [][]any `json:"values"`
}

// sheetValuesData is the data payload of sheetValuesResponse.
type sheetValuesData struct {
	ValueRange sheetValueRange `json:"valueRange"`
}

// sheetValuesResponse is the response for sheets-v2 values read.
type sheetValuesResponse struct {
	ApiResponse
	Data sheetValuesData `json:"data"`
}

// readSheetRange reads the cell values of an embedded spreadsheet block.
// embedToken is the block's sheet.token, formatted "spreadsheetToken_sheetId".
// Cells are stringified (display value) for RAG text retrieval. Rows are capped
// at maxTableRows; truncated is true when the source had more rows than that.
func (c *Client) readSheetRange(ctx context.Context, embedToken string) ([][]string, bool, error) {
	idx := strings.LastIndex(embedToken, "_")
	if idx < 0 {
		return nil, false, fmt.Errorf("invalid sheet embed token: %q", embedToken)
	}
	spreadsheetToken, sheetID := embedToken[:idx], embedToken[idx+1:]
	path := fmt.Sprintf("/open-apis/sheets/v2/spreadsheets/%s/values/%s?valueRenderOption=ToString",
		url.PathEscape(spreadsheetToken), url.PathEscape(sheetID))
	var resp sheetValuesResponse
	if err := c.DoRequest(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, false, fmt.Errorf("read sheet range: %w", err)
	}
	if resp.Code != 0 {
		return nil, false, fmt.Errorf("read sheet range error: code=%d msg=%s", resp.Code, resp.Msg)
	}
	raw, truncated := capRows(resp.Data.ValueRange.Values)
	return stringifyMatrix(raw), truncated, nil
}

// capRows limits rows to maxTableRows, reporting whether truncation happened. It
// is generic so the sheet ([][]any) and bitable ([][]string) read paths share one
// truncation rule instead of each inlining their own.
func capRows[T any](rows []T) ([]T, bool) {
	if len(rows) > maxTableRows {
		return rows[:maxTableRows], true
	}
	return rows, false
}

// stringifyMatrix renders arbitrary cell values to strings; nil → "".
func stringifyMatrix(in [][]any) [][]string {
	out := make([][]string, len(in))
	for i, row := range in {
		cells := make([]string, len(row))
		for j, v := range row {
			cells[j] = cellToString(v)
		}
		out[i] = cells
	}
	return out
}

// cellToString renders a single JSON cell value to a string; nil → "".
func cellToString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// bitableFieldTypeDateTime is the Feishu bitable field type for a date/datetime
// column. Its cell value is a bare Unix-millisecond number, so without type-aware
// formatting it would render as a 13-digit integer instead of a readable date.
// https://open.feishu.cn/document/server-docs/docs/bitable-v1/app-table-field/guide
const bitableFieldTypeDateTime = 5

// bitableColumn describes one bitable field: its name, integer type, and (for a
// date column) its date_formatter, which distinguishes a date-only column
// ("yyyy/MM/dd") from a datetime one ("yyyy-MM-dd HH:mm").
type bitableColumn struct {
	name          string
	fieldType     int
	dateFormatter string
}

// bitableFieldCell renders one bitable cell. Date columns carry a Unix-millisecond
// UTC instant; the calendar date a user sees is that instant in the table's
// timezone, so it is formatted in loc (rendering in UTC would shift the date, e.g.
// a GMT+8 "2024-04-01" would show as "2024-03-31 16:00:00"). A date-only formatter
// yields "2006-01-02"; a datetime formatter adds the "15:04" time. Every other
// type falls through to bitableCellToString.
func bitableFieldCell(v any, col bitableColumn, loc *time.Location) string {
	if col.fieldType == bitableFieldTypeDateTime {
		// Empty date cells arrive as nil (→ fall through to blank); ms==0 (epoch) is
		// not a real Feishu date value either — render blank, not "1970-01-01" or "0".
		if ms, ok := v.(float64); ok {
			if ms == 0 {
				return ""
			}
			t := time.UnixMilli(int64(ms)).In(loc)
			if dateFormatterHasTime(col.dateFormatter) {
				return t.Format("2006-01-02 15:04")
			}
			return t.Format("2006-01-02")
		}
	}
	return bitableCellToString(v)
}

// dateFormatterHasTime reports whether a Feishu date_formatter includes a time
// component. Feishu formatters are Java-style: 'H'/'h' hour, lowercase 'm' minute,
// uppercase 'M' month — so a time part is present iff the string carries an hour or
// a lowercase-'m' minute token (e.g. "yyyy-MM-dd HH:mm"). An empty formatter
// (Feishu's default "yyyy/MM/dd") is date-only.
func dateFormatterHasTime(f string) bool {
	return strings.ContainsAny(f, "Hh") || strings.Contains(f, "m")
}

// bitableCellToString renders a bitable field value — which may be text segments,
// a person/link object, or an attachment/multi-select array — to a readable
// string for RAG. Scalars delegate to cellToString.
func bitableCellToString(v any) string {
	switch t := v.(type) {
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			if s := bitableCellToString(e); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	case map[string]any:
		for _, k := range []string{"text", "name", "en_name", "link"} {
			if s, ok := t[k].(string); ok && s != "" {
				return s
			}
		}
		return ""
	default:
		return cellToString(v)
	}
}

// bitableFieldProperty carries a field's type-specific settings.
type bitableFieldProperty struct {
	// DateFormatter distinguishes a date-only column from a datetime one.
	DateFormatter string `json:"date_formatter"`
}

// bitableField is one entry of bitableFieldsData.Items.
type bitableField struct {
	FieldName string                `json:"field_name"`
	Type      int                   `json:"type"`
	Property  *bitableFieldProperty `json:"property"`
}

// bitableFieldsData is the data payload of bitableFieldsResponse.
type bitableFieldsData struct {
	HasMore   bool           `json:"has_more"`
	PageToken string         `json:"page_token"`
	Items     []bitableField `json:"items"`
}

type bitableFieldsResponse struct {
	ApiResponse
	Data bitableFieldsData `json:"data"`
}

// maxBitableFieldPageSize is the documented per-page cap for the bitable
// list-fields endpoint (100). Unlike list-records (max 500), passing a larger
// page_size here is rejected, so fields must be fetched 100 at a time and paged.
const maxBitableFieldPageSize = 100

// bitableRecord is one entry of bitableRecordsData.Items.
type bitableRecord struct {
	Fields map[string]any `json:"fields"`
}

// bitableRecordsData is the data payload of bitableRecordsResponse.
type bitableRecordsData struct {
	HasMore   bool            `json:"has_more"`
	PageToken string          `json:"page_token"`
	Items     []bitableRecord `json:"items"`
}

type bitableRecordsResponse struct {
	ApiResponse
	Data bitableRecordsData `json:"data"`
}

// readBitableRecords reads an embedded bitable block as a table: a header row of
// field names followed by one row per record. embedToken is the block's
// bitable.token, formatted "appToken_tableId". Record rows are capped at
// maxTableRows; truncated is true when the source had more records than that.
func (c *Client) readBitableRecords(ctx context.Context, embedToken string) ([][]string, bool, error) {
	idx := strings.LastIndex(embedToken, "_")
	if idx < 0 {
		return nil, false, fmt.Errorf("invalid bitable embed token: %q", embedToken)
	}
	appToken, tableID := embedToken[:idx], embedToken[idx+1:]

	var cols []bitableColumn
	baseFPath := fmt.Sprintf("/open-apis/bitable/v1/apps/%s/tables/%s/fields?page_size=%d",
		url.PathEscape(appToken), url.PathEscape(tableID), maxBitableFieldPageSize)
	fieldPageToken := ""
	for {
		fpath := baseFPath
		if fieldPageToken != "" {
			fpath += "&page_token=" + url.QueryEscape(fieldPageToken)
		}
		var fieldsResp bitableFieldsResponse
		if err := c.DoRequest(ctx, http.MethodGet, fpath, nil, &fieldsResp); err != nil {
			return nil, false, fmt.Errorf("read bitable fields: %w", err)
		}
		if fieldsResp.Code != 0 {
			return nil, false, fmt.Errorf("read bitable fields error: code=%d msg=%s", fieldsResp.Code, fieldsResp.Msg)
		}
		for _, f := range fieldsResp.Data.Items {
			formatter := ""
			if f.Property != nil {
				formatter = f.Property.DateFormatter
			}
			cols = append(cols, bitableColumn{name: f.FieldName, fieldType: f.Type, dateFormatter: formatter})
		}
		if len(fieldsResp.Data.Items) == 0 {
			// Defensive: an empty page with has_more=true would loop forever (this
			// loop has no size cap of its own). Nothing more to read, so stop.
			break
		}
		if !fieldsResp.Data.HasMore || fieldsResp.Data.PageToken == "" {
			break
		}
		fieldPageToken = fieldsResp.Data.PageToken
	}

	header := make([]string, len(cols))
	for i, col := range cols {
		header[i] = col.name
	}
	loc := c.tz()

	var dataRows [][]string
	truncated := false
	// Use the Search-records endpoint (POST .../records/search). The legacy
	// GET .../records is officially deprecated ("已不推荐使用，可使用[查询记录]替代").
	// An empty body queries the table's default view, paginated via page_token
	// (we page to the end below), so a default view with a filter would omit the
	// filtered-out records — acceptable for RAG, but not literally "all records".
	baseRPath := fmt.Sprintf("/open-apis/bitable/v1/apps/%s/tables/%s/records/search?page_size=500",
		url.PathEscape(appToken), url.PathEscape(tableID))
	pageToken := ""
	for {
		rpath := baseRPath
		if pageToken != "" {
			rpath += "&page_token=" + url.QueryEscape(pageToken)
		}
		var rec bitableRecordsResponse
		if err := c.DoRequest(ctx, http.MethodPost, rpath, map[string]any{}, &rec); err != nil {
			return nil, false, fmt.Errorf("search bitable records: %w", err)
		}
		if rec.Code != 0 {
			return nil, false, fmt.Errorf("read bitable records error: code=%d msg=%s", rec.Code, rec.Msg)
		}
		for _, item := range rec.Data.Items {
			row := make([]string, len(cols))
			for i, col := range cols {
				row[i] = bitableFieldCell(item.Fields[col.name], col, loc)
			}
			dataRows = append(dataRows, row)
		}
		if len(rec.Data.Items) == 0 {
			// Defensive: an empty page with has_more=true never advances dataRows,
			// so the maxTableRows cap below would never trip — stop instead of
			// looping until the task deadline.
			break
		}
		if len(dataRows) >= maxTableRows {
			if len(dataRows) > maxTableRows || rec.Data.HasMore {
				truncated = true
			}
			break
		}
		if !rec.Data.HasMore || rec.Data.PageToken == "" {
			break
		}
		pageToken = rec.Data.PageToken
	}
	dataRows, capped := capRows(dataRows)
	truncated = truncated || capped
	rows := append([][]string{header}, dataRows...)
	return rows, truncated, nil
}
