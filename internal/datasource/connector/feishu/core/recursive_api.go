package core

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// ---------- Bitable ----------

// BitableTableInfo identifies one table in a Bitable app.
type BitableTableInfo struct {
	TableID string `json:"table_id"`
	Name    string `json:"name"`
}

type bitableTablesData struct {
	HasMore   bool               `json:"has_more"`
	PageToken string             `json:"page_token"`
	Items     []BitableTableInfo `json:"items"`
}

type bitableTablesResponse struct {
	ApiResponse
	Data bitableTablesData `json:"data"`
}

// ListBitableTables 列出一个多维表格应用中的所有数据表。
func (c *Client) ListBitableTables(
	ctx context.Context,
	appToken string,
) ([]BitableTableInfo, error) {
	var all []BitableTableInfo
	pageToken := ""
	seenPages := make(map[string]bool)

	for {
		path := fmt.Sprintf(
			"/open-apis/bitable/v1/apps/%s/tables?page_size=100",
			url.PathEscape(appToken),
		)

		if pageToken != "" {
			path += "&page_token=" + url.QueryEscape(pageToken)
		}

		var resp bitableTablesResponse

		if err := c.DoRequest(
			ctx,
			http.MethodGet,
			path,
			nil,
			&resp,
		); err != nil {
			return nil, fmt.Errorf(
				"list bitable tables: %w",
				err,
			)
		}

		if resp.Code != 0 {
			return nil, fmt.Errorf(
				"list bitable tables: code=%d msg=%s",
				resp.Code,
				resp.Msg,
			)
		}

		all = append(all, resp.Data.Items...)

		if !resp.Data.HasMore ||
			resp.Data.PageToken == "" {
			break
		}

		if seenPages[resp.Data.PageToken] || len(seenPages) >= 100 {
			return nil, fmt.Errorf("bitable table pagination repeated or exceeded 100 pages")
		}
		seenPages[resp.Data.PageToken] = true
		pageToken = resp.Data.PageToken
	}

	return all, nil
}

// ReadBitableRawRecords 返回原始 fields。
// 与 readBitableRecords 不同，这里故意不把字段转成字符串，
// 因为递归器需要保留 link/url 等结构化字段。
func (c *Client) ReadBitableRawRecords(
	ctx context.Context,
	appToken string,
	tableID string,
	viewID string,
) ([]map[string]any, error) {
	var rows []map[string]any
	pageToken := ""
	seenPages := make(map[string]bool)

	for {
		path := fmt.Sprintf(
			"/open-apis/bitable/v1/apps/%s/tables/%s/records/search?page_size=500",
			url.PathEscape(appToken),
			url.PathEscape(tableID),
		)

		if pageToken != "" {
			path += "&page_token=" + url.QueryEscape(pageToken)
		}

		body := map[string]any{}
		if viewID != "" {
			body["view_id"] = viewID
		}

		var resp bitableRecordsResponse

		if err := c.DoRequest(
			ctx,
			http.MethodPost,
			path,
			body,
			&resp,
		); err != nil {
			return nil, fmt.Errorf(
				"read bitable records: %w",
				err,
			)
		}

		if resp.Code != 0 {
			return nil, fmt.Errorf(
				"read bitable records: code=%d msg=%s",
				resp.Code,
				resp.Msg,
			)
		}

		for _, item := range resp.Data.Items {
			rows = append(rows, item.Fields)
		}

		if !resp.Data.HasMore ||
			resp.Data.PageToken == "" {
			break
		}

		if seenPages[resp.Data.PageToken] || len(seenPages) >= 100 {
			return nil, fmt.Errorf("bitable record pagination repeated or exceeded 100 pages")
		}
		seenPages[resp.Data.PageToken] = true
		pageToken = resp.Data.PageToken
	}

	return rows, nil
}

// ---------- Sheet ----------

// SheetTabInfo identifies one worksheet in a spreadsheet.
type SheetTabInfo struct {
	SheetID string `json:"sheet_id"`
	Title   string `json:"title"`
}

type sheetTabsData struct {
	Sheets []SheetTabInfo `json:"sheets"`
}

type sheetTabsResponse struct {
	ApiResponse
	Data sheetTabsData `json:"data"`
}

// ListSheetTabs 列出电子表格中的所有工作表。
func (c *Client) ListSheetTabs(
	ctx context.Context,
	spreadsheetToken string,
) ([]SheetTabInfo, error) {
	path := fmt.Sprintf(
		"/open-apis/sheets/v3/spreadsheets/%s/sheets/query",
		url.PathEscape(spreadsheetToken),
	)

	var resp sheetTabsResponse

	if err := c.DoRequest(
		ctx,
		http.MethodGet,
		path,
		nil,
		&resp,
	); err != nil {
		return nil, fmt.Errorf(
			"list sheet tabs: %w",
			err,
		)
	}

	if resp.Code != 0 {
		return nil, fmt.Errorf(
			"list sheet tabs: code=%d msg=%s",
			resp.Code,
			resp.Msg,
		)
	}

	return resp.Data.Sheets, nil
}

// ReadSheetRawValues 保留原始 JSON 单元格数据，
// 便于后续从单元格中寻找 URL。
func (c *Client) ReadSheetRawValues(
	ctx context.Context,
	spreadsheetToken string,
	sheetID string,
) ([][]any, error) {
	path := fmt.Sprintf(
		"/open-apis/sheets/v2/spreadsheets/%s/values/%s",
		url.PathEscape(spreadsheetToken),
		url.PathEscape(sheetID),
	)

	var resp sheetValuesResponse

	if err := c.DoRequest(
		ctx,
		http.MethodGet,
		path,
		nil,
		&resp,
	); err != nil {
		return nil, fmt.Errorf(
			"read sheet values: %w",
			err,
		)
	}

	if resp.Code != 0 {
		return nil, fmt.Errorf(
			"read sheet values: code=%d msg=%s",
			resp.Code,
			resp.Msg,
		)
	}

	return resp.Data.ValueRange.Values, nil
}

// ---------- URL discovery ----------

var recursiveURLPattern = regexp.MustCompile(
	`https?://[^\s<>"'\]\)]+`,
)

func collectURLs(value any, out *[]string) {
	switch v := value.(type) {
	case nil:
		return

	case string:
		for _, found := range recursiveURLPattern.FindAllString(v, -1) {
			found = strings.TrimRight(
				found,
				".,;，；。",
			)

			if found != "" {
				*out = append(*out, found)
			}
		}

	// JSON 解码后的普通数组。
	case []any:
		for _, item := range v {
			collectURLs(item, out)
		}

	// Bitable ReadBitableRawRecords 的顶层真实类型。
	case []map[string]any:
		for _, item := range v {
			collectURLs(item, out)
		}

	// Sheet ReadSheetRawValues 的顶层真实类型。
	case [][]any:
		for _, row := range v {
			collectURLs(row, out)
		}

	case []string:
		for _, item := range v {
			collectURLs(item, out)
		}

	case map[string]any:
		for _, item := range v {
			collectURLs(item, out)
		}

	case map[string]string:
		for _, item := range v {
			collectURLs(item, out)
		}
	}
}

// DiscoverLinkedResources 从任意 Bitable/Sheet 原始数据中提取飞书链接。
func DiscoverLinkedResources(
	value any,
) []LinkedResource {
	var rawURLs []string
	collectURLs(value, &rawURLs)

	seen := make(map[string]struct{})
	result := make([]LinkedResource, 0)

	for _, raw := range rawURLs {
		resource, ok := ParseLinkedResourceURL(raw)
		if !ok {
			continue
		}

		key := strings.Join(
			[]string{
				resource.Type,
				resource.Token,
				resource.TableID,
				resource.ViewID,
			},
			"|",
		)

		if _, exists := seen[key]; exists {
			continue
		}

		seen[key] = struct{}{}
		result = append(result, resource)
	}

	return result
}
