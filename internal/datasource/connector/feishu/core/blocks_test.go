package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestListDocumentBlocks_Paginates(t *testing.T) {
	var gotTokens []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "t", "expire": 7200})
			return
		}
		gotTokens = append(gotTokens, r.URL.Query().Get("page_token"))
		if r.URL.Query().Get("page_size") != "500" {
			t.Errorf("page_size = %q, want 500", r.URL.Query().Get("page_size"))
		}
		if r.URL.Query().Get("page_token") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"items":      []map[string]any{{"block_id": "b1", "block_type": 2}, {"block_id": "b2", "block_type": 2}},
				"has_more":   true,
				"page_token": "p2",
			}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
			"items":    []map[string]any{{"block_id": "b3", "block_type": 2}},
			"has_more": false,
		}})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, appID: "a", appSecret: "s", httpClient: srv.Client()}
	blocks, err := c.listDocumentBlocks(context.Background(), "doc123")
	if err != nil {
		t.Fatalf("listDocumentBlocks: %v", err)
	}
	if len(blocks) != 3 {
		t.Fatalf("got %d blocks, want 3", len(blocks))
	}
	if blocks[0].BlockID != "b1" || blocks[2].BlockID != "b3" {
		t.Errorf("order not preserved: %+v", blocks)
	}
	if len(gotTokens) != 2 || gotTokens[1] != "p2" {
		t.Errorf("page tokens = %v, want ['', 'p2']", gotTokens)
	}
}

func TestReadSheetRange_SplitsTokenAndReadsValues(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "t", "expire": 7200})
			return
		}
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
			"valueRange": map[string]any{"values": [][]any{{"名称", "数量"}, {"苹果", 3}, {"梨", nil}}},
		}})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, appID: "a", appSecret: "s", httpClient: srv.Client()}
	rows, truncated, err := c.readSheetRange(context.Background(), "sht_abc_0")
	if err != nil {
		t.Fatalf("readSheetRange: %v", err)
	}
	if gotPath != "/open-apis/sheets/v2/spreadsheets/sht_abc/values/0" {
		t.Errorf("path = %q", gotPath)
	}
	if truncated {
		t.Errorf("did not expect truncation for 3 rows")
	}
	if len(rows) != 3 || rows[0][0] != "名称" || rows[1][1] != "3" || rows[2][1] != "" {
		t.Errorf("rows = %+v", rows)
	}
}

func TestReadSheetRange_TruncatesLargeTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "t", "expire": 7200})
			return
		}
		values := make([][]any, 600)
		for i := range values {
			values[i] = []any{i}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
			"valueRange": map[string]any{"values": values},
		}})
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, appID: "a", appSecret: "s", httpClient: srv.Client()}
	rows, truncated, err := c.readSheetRange(context.Background(), "sht_x_0")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !truncated {
		t.Errorf("expected truncated=true for 600 rows")
	}
	if len(rows) != 500 {
		t.Errorf("rows capped to %d, want 500", len(rows))
	}
}

func TestBitableCellToString(t *testing.T) {
	// text-segment array: [{type:text, text:"你好"}] → "你好"
	textSeg := []any{map[string]any{"type": "text", "text": "你好"}}
	if got := bitableCellToString(textSeg); got != "你好" {
		t.Errorf("text segment: got %q, want %q", got, "你好")
	}

	// person array: [{id:u1, name:"张三"}] → "张三"
	person := []any{map[string]any{"id": "u1", "name": "张三"}}
	if got := bitableCellToString(person); got != "张三" {
		t.Errorf("person: got %q, want %q", got, "张三")
	}

	// plain number (float64) → "3"
	if got := bitableCellToString(float64(3)); got != "3" {
		t.Errorf("float64: got %q, want %q", got, "3")
	}

	// plain string → passthrough
	if got := bitableCellToString("abc"); got != "abc" {
		t.Errorf("string: got %q, want %q", got, "abc")
	}
}

func TestBitableFieldCell_DateColumn(t *testing.T) {
	gmt8 := resolveLocation("") // default GMT+8
	dateOnly := bitableColumn{fieldType: bitableFieldTypeDateTime, dateFormatter: "yyyy/MM/dd"}
	dateTime := bitableColumn{fieldType: bitableFieldTypeDateTime, dateFormatter: "yyyy-MM-dd HH:mm"}
	number := bitableColumn{fieldType: 2}

	// Feishu stores a date as a UTC instant at the table-timezone midnight. The
	// official sample 1711900800000 is 2024-04-01 00:00:00 GMT+8 (== 2024-03-31
	// 16:00:00 UTC); a date-only column must render the GMT+8 calendar date, NOT
	// the UTC-shifted "2024-03-31 16:00:00" the old code produced.
	if got := bitableFieldCell(float64(1711900800000), dateOnly, gmt8); got != "2024-04-01" {
		t.Errorf("date-only cell: got %q, want %q", got, "2024-04-01")
	}

	// A datetime column keeps the time, rendered in the same timezone.
	// 1719802800000 == 2024-07-01 03:00 UTC == 2024-07-01 11:00 GMT+8.
	if got := bitableFieldCell(float64(1719802800000), dateTime, gmt8); got != "2024-07-01 11:00" {
		t.Errorf("datetime cell: got %q, want %q", got, "2024-07-01 11:00")
	}

	// A non-date numeric field is left as its plain value, not a date.
	if got := bitableFieldCell(float64(42), number, gmt8); got != "42" {
		t.Errorf("number cell: got %q, want %q", got, "42")
	}

	// An empty date cell (nil, or epoch 0) renders blank, not "1970-01-01".
	if got := bitableFieldCell(nil, dateOnly, gmt8); got != "" {
		t.Errorf("empty date cell: got %q, want empty", got)
	}
	if got := bitableFieldCell(float64(0), dateOnly, gmt8); got != "" {
		t.Errorf("epoch-0 date cell: got %q, want empty", got)
	}
}

func TestDateFormatterHasTime(t *testing.T) {
	withTime := []string{"yyyy-MM-dd HH:mm", "HH:mm", "yyyy/MM/dd hh:mm", "MM-dd HH:mm"}
	dateOnly := []string{"", "yyyy/MM/dd", "MM-dd", "MM/dd/yyyy", "dd/MM/yyyy"}
	for _, f := range withTime {
		if !dateFormatterHasTime(f) {
			t.Errorf("dateFormatterHasTime(%q) = false, want true", f)
		}
	}
	for _, f := range dateOnly {
		if dateFormatterHasTime(f) {
			t.Errorf("dateFormatterHasTime(%q) = true, want false", f)
		}
	}
}

func TestResolveLocation(t *testing.T) {
	// Empty and unknown names both fall back to a fixed GMT+8, with no dependency
	// on system tzdata (minimal container images may omit it).
	for _, name := range []string{"", "Not/AZone"} {
		ref := time.Date(2024, 4, 1, 0, 0, 0, 0, resolveLocation(name))
		if _, off := ref.Zone(); off != defaultTimezoneOffsetSeconds {
			t.Errorf("resolveLocation(%q) offset = %d, want %d", name, off, defaultTimezoneOffsetSeconds)
		}
	}
}

// TestReadBitableRecords_DateColumnUsesFormatterAndTimezone proves the date fix
// end-to-end: property.date_formatter is parsed from the fields response, a type-5
// column is detected, and the ms instant is rendered in the client timezone
// (GMT+8) — date-only vs datetime distinguished by the formatter.
func TestReadBitableRecords_DateColumnUsesFormatterAndTimezone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "t", "expire": 7200})
		case strings.HasSuffix(r.URL.Path, "/fields"):
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"items": []map[string]any{
					{"field_name": "截止日期", "type": 5, "property": map[string]any{"date_formatter": "yyyy/MM/dd"}},
					{"field_name": "提醒时间", "type": 5, "property": map[string]any{"date_formatter": "yyyy-MM-dd HH:mm"}},
				},
			}})
		case strings.HasSuffix(r.URL.Path, "/records/search"):
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"has_more": false, "items": []map[string]any{
					{"fields": map[string]any{"截止日期": float64(1711900800000), "提醒时间": float64(1719802800000)}},
				},
			}})
		}
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, appID: "a", appSecret: "s", httpClient: srv.Client(), location: resolveLocation("")}
	rows, _, err := c.readBitableRecords(context.Background(), "bascabc_tblxyz")
	if err != nil {
		t.Fatalf("readBitableRecords: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want header + 1 record", rows)
	}
	if rows[1][0] != "2024-04-01" {
		t.Errorf("date-only column = %q, want 2024-04-01 (GMT+8, not UTC-shifted)", rows[1][0])
	}
	if rows[1][1] != "2024-07-01 11:00" {
		t.Errorf("datetime column = %q, want 2024-07-01 11:00 (GMT+8)", rows[1][1])
	}
}

func TestReadBitableRecords_SplitsTokenAndBuildsTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "t", "expire": 7200})
		case strings.HasSuffix(r.URL.Path, "/fields"):
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"items": []map[string]any{{"field_name": "任务"}, {"field_name": "状态"}},
			}})
		case strings.HasSuffix(r.URL.Path, "/records/search"):
			// Must use the current Search-records endpoint (POST), not the
			// deprecated GET .../records legacy interface.
			if r.Method != http.MethodPost {
				t.Errorf("records read method = %s, want POST (search endpoint)", r.Method)
			}
			if !strings.Contains(r.URL.Path, "/apps/bascabc/tables/tblxyz/") {
				t.Errorf("bad path %q", r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"has_more": false,
				"items": []map[string]any{
					{"fields": map[string]any{"任务": "写文档", "状态": "进行中"}},
				},
			}})
		}
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, appID: "a", appSecret: "s", httpClient: srv.Client()}
	rows, truncated, err := c.readBitableRecords(context.Background(), "bascabc_tblxyz")
	if err != nil {
		t.Fatalf("readBitableRecords: %v", err)
	}
	if truncated {
		t.Errorf("did not expect truncation")
	}
	if len(rows) != 2 || rows[0][0] != "任务" || rows[0][1] != "状态" || rows[1][0] != "写文档" || rows[1][1] != "进行中" {
		t.Errorf("rows = %+v", rows)
	}
}

// TestReadBitableRecords_PaginatesFieldsAtMax100 guards the real-API constraint
// that the list-fields endpoint caps page_size at 100 (unlike list-records, which
// allows 500). The connector must request page_size=100 and page through fields,
// or a table with >100 columns would silently lose its header tail (or the whole
// bitable would degrade if Feishu rejects page_size=500). Verified against the
// official docs: bitable-v1/app-table-field/list "最大值：100".
func TestReadBitableRecords_PaginatesFieldsAtMax100(t *testing.T) {
	const totalFields = 150 // spans two pages: 100 + 50
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "t", "expire": 7200})
		case strings.HasSuffix(r.URL.Path, "/fields"):
			if ps := r.URL.Query().Get("page_size"); ps != "100" {
				t.Errorf("fields page_size = %q, want 100 (endpoint max)", ps)
			}
			// Page 1 (no token): first 100 fields, has_more. Page 2 (token=f2): last 50.
			var items []map[string]any
			hasMore := false
			nextTok := ""
			if r.URL.Query().Get("page_token") == "" {
				for i := 0; i < 100; i++ {
					items = append(items, map[string]any{"field_name": fieldName(i)})
				}
				hasMore, nextTok = true, "f2"
			} else {
				for i := 100; i < totalFields; i++ {
					items = append(items, map[string]any{"field_name": fieldName(i)})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"items": items, "has_more": hasMore, "page_token": nextTok,
			}})
		case strings.HasSuffix(r.URL.Path, "/records/search"):
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"has_more": false, "items": []map[string]any{},
			}})
		}
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, appID: "a", appSecret: "s", httpClient: srv.Client()}
	rows, _, err := c.readBitableRecords(context.Background(), "bascabc_tblxyz")
	if err != nil {
		t.Fatalf("readBitableRecords: %v", err)
	}
	if len(rows) < 1 {
		t.Fatal("expected at least a header row")
	}
	header := rows[0]
	if len(header) != totalFields {
		t.Fatalf("header has %d fields, want %d (fields pagination lost columns)", len(header), totalFields)
	}
	if header[0] != fieldName(0) || header[totalFields-1] != fieldName(totalFields-1) {
		t.Errorf("header endpoints wrong: first=%q last=%q", header[0], header[totalFields-1])
	}
}

func fieldName(i int) string { return "f" + strconv.Itoa(i) }

// bitableRows builds n bitable record items each carrying a single "col" field.
func bitableRows(n int) []map[string]any {
	items := make([]map[string]any, n)
	for i := range items {
		items[i] = map[string]any{"fields": map[string]any{"col": "v" + strconv.Itoa(i)}}
	}
	return items
}

// TestReadBitableRecords_PaginatesAndTruncatesRecords exercises the record-side
// pagination advance and the >maxTableRows truncation — the boundary logic that
// no prior test reached (every other bitable test returns a single sub-cap page).
func TestReadBitableRecords_PaginatesAndTruncatesRecords(t *testing.T) {
	var secondPageToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "t", "expire": 7200})
		case strings.HasSuffix(r.URL.Path, "/fields"):
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"items": []map[string]any{{"field_name": "col"}},
			}})
		case strings.HasSuffix(r.URL.Path, "/records/search"):
			if tok := r.URL.Query().Get("page_token"); tok == "" {
				// Page 1: 300 records, more to come.
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
					"has_more": true, "page_token": "r2", "items": bitableRows(300),
				}})
			} else {
				secondPageToken = tok
				// Page 2: 250 more → 550 total, capped to maxTableRows.
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
					"has_more": false, "items": bitableRows(250),
				}})
			}
		}
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, appID: "a", appSecret: "s", httpClient: srv.Client()}
	rows, truncated, err := c.readBitableRecords(context.Background(), "bascabc_tblxyz")
	if err != nil {
		t.Fatalf("readBitableRecords: %v", err)
	}
	if secondPageToken != "r2" {
		t.Errorf("second records page must be fetched with page_token=r2, got %q", secondPageToken)
	}
	if len(rows) != maxTableRows+1 { // header + maxTableRows capped data rows
		t.Errorf("rows = %d, want %d (header + %d capped)", len(rows), maxTableRows+1, maxTableRows)
	}
	if !truncated {
		t.Error("a >maxTableRows bitable must report truncated=true")
	}
}

// TestReadBitableRecords_Exactly500NotTruncated guards the off-by-one at the cap:
// exactly maxTableRows records in a single page must NOT be flagged truncated.
func TestReadBitableRecords_Exactly500NotTruncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "t", "expire": 7200})
		case strings.HasSuffix(r.URL.Path, "/fields"):
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"items": []map[string]any{{"field_name": "col"}},
			}})
		case strings.HasSuffix(r.URL.Path, "/records/search"):
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"has_more": false, "items": bitableRows(maxTableRows),
			}})
		}
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL, appID: "a", appSecret: "s", httpClient: srv.Client()}
	rows, truncated, err := c.readBitableRecords(context.Background(), "bascabc_tblxyz")
	if err != nil {
		t.Fatalf("readBitableRecords: %v", err)
	}
	if truncated {
		t.Error("exactly maxTableRows records must not be flagged truncated")
	}
	if len(rows) != maxTableRows+1 {
		t.Errorf("rows = %d, want %d (header + %d)", len(rows), maxTableRows+1, maxTableRows)
	}
}

// TestReadBitableRecords_EmptyRecordPageTerminates guards the pagination
// zero-progress break: a malformed page (empty items but has_more=true and a
// non-empty page_token) must terminate instead of looping until the task
// deadline. A short context deadline is a safety net so a regression fails fast
// rather than hanging the suite.
func TestReadBitableRecords_EmptyRecordPageTerminates(t *testing.T) {
	recordCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "t", "expire": 7200})
		case strings.HasSuffix(r.URL.Path, "/fields"):
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"items": []map[string]any{{"field_name": "col"}},
			}})
		case strings.HasSuffix(r.URL.Path, "/records/search"):
			recordCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"has_more": true, "page_token": "loop", "items": []map[string]any{},
			}})
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := &Client{baseURL: srv.URL, appID: "a", appSecret: "s", httpClient: srv.Client()}
	rows, _, err := c.readBitableRecords(ctx, "bascabc_tblxyz")
	if err != nil {
		t.Fatalf("empty-page pagination must terminate cleanly, got err: %v", err)
	}
	if recordCalls != 1 {
		t.Errorf("records endpoint called %d times, want 1 (loop must break on the empty page)", recordCalls)
	}
	if len(rows) != 1 { // header only
		t.Errorf("rows = %d, want 1 (header only, no records)", len(rows))
	}
}

// TestReadBitableRecords_EmptyFieldsPageTerminates guards the fields loop, which
// has no size cap of its own — an empty page with has_more=true must still break.
func TestReadBitableRecords_EmptyFieldsPageTerminates(t *testing.T) {
	fieldCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "t", "expire": 7200})
		case strings.HasSuffix(r.URL.Path, "/fields"):
			fieldCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"has_more": true, "page_token": "loop", "items": []map[string]any{},
			}})
		case strings.HasSuffix(r.URL.Path, "/records/search"):
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"has_more": false, "items": []map[string]any{},
			}})
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := &Client{baseURL: srv.URL, appID: "a", appSecret: "s", httpClient: srv.Client()}
	if _, _, err := c.readBitableRecords(ctx, "bascabc_tblxyz"); err != nil {
		t.Fatalf("empty fields page must terminate cleanly, got err: %v", err)
	}
	if fieldCalls != 1 {
		t.Errorf("fields endpoint called %d times, want 1 (loop must break on the empty page)", fieldCalls)
	}
}

// TestListDocumentBlocks_EmptyPageTerminates guards the blocks pagination loop.
func TestListDocumentBlocks_EmptyPageTerminates(t *testing.T) {
	blockCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "t", "expire": 7200})
		case strings.HasSuffix(r.URL.Path, "/blocks"):
			blockCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"has_more": true, "page_token": "loop", "items": []map[string]any{},
			}})
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := &Client{baseURL: srv.URL, appID: "a", appSecret: "s", httpClient: srv.Client()}
	blocks, err := c.listDocumentBlocks(ctx, "doc-1")
	if err != nil {
		t.Fatalf("empty blocks page must terminate cleanly, got err: %v", err)
	}
	if blockCalls != 1 {
		t.Errorf("blocks endpoint called %d times, want 1 (loop must break on the empty page)", blockCalls)
	}
	if len(blocks) != 0 {
		t.Errorf("blocks = %d, want 0", len(blocks))
	}
}
