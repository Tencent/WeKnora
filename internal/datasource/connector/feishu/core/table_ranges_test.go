package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestReadSheetRange_BoundsResponseBeforeReading(t *testing.T) {
	const responseLimit = 80 << 10
	cell := strings.Repeat("x", 128)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = fmt.Fprint(w, `{"code":0,"tenant_access_token":"t","expire":7200}`)
		case r.URL.Path == "/open-apis/sheets/v3/spreadsheets/sht_abc/sheets/0":
			_, _ = fmt.Fprint(w, `{"code":0,"data":{"sheet":{"grid_properties":{"row_count":1000,"column_count":28}}}}`)
		case strings.HasPrefix(r.URL.Path, "/open-apis/sheets/v2/spreadsheets/sht_abc/values/"):
			var end int
			rangeName := strings.TrimPrefix(r.URL.Path, "/open-apis/sheets/v2/spreadsheets/sht_abc/values/")
			if _, err := fmt.Sscanf(rangeName, "0!A1:AB%d", &end); err != nil || end > maxTableRows {
				t.Errorf("unexpected range %q", rangeName)
				http.Error(w, "invalid range", http.StatusBadRequest)
				return
			}
			values := make([][]any, end)
			for i := range values {
				values[i] = []any{strconv.Itoa(i), cell}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"valueRange": map[string]any{"values": values},
			}})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &Client{baseURL: srv.URL, appID: "a", appSecret: "s", httpClient: srv.Client(), jsonLimit: responseLimit}
	rows, truncated, err := c.readSheetRange(context.Background(), "sht_abc_0")
	if err != nil {
		t.Fatalf("bounded sheet read failed: %v", err)
	}
	if len(rows) != maxTableRows || !truncated {
		t.Fatalf("rows=%d truncated=%v, want %d and true", len(rows), truncated, maxTableRows)
	}
	for i, row := range rows {
		if row[0] != strconv.Itoa(i) || row[1] != cell {
			t.Fatalf("row %d was changed or reordered", i)
		}
	}
}

func TestReadSheetRange_DimensionsAndTruncation(t *testing.T) {
	for _, tt := range []struct {
		name       string
		gridRows   int
		columns    int
		values     int
		lastColumn string
	}{
		{"empty", 10, 1, 0, "A"},
		{"small", 3, 26, 3, "Z"},
		{"below cap", 499, 27, 499, "AA"},
		{"at cap", 500, 702, 500, "ZZ"},
		{"above cap", 1000, 703, 500, "AAA"},
		{"sparse range with rows beyond cap", 1000, 2, 3, "B"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
					_, _ = fmt.Fprint(w, `{"code":0,"tenant_access_token":"t","expire":7200}`)
				case strings.Contains(r.URL.Path, "/sheets/v3/"):
					_, _ = fmt.Fprintf(w,
						`{"code":0,"data":{"sheet":{"grid_properties":{"row_count":%d,"column_count":%d}}}}`,
						tt.gridRows, tt.columns)
				default:
					wantPath := fmt.Sprintf("/open-apis/sheets/v2/spreadsheets/sht/values/0!A1:%s%d",
						tt.lastColumn, min(tt.gridRows, maxTableRows))
					if r.Method != http.MethodGet || r.URL.Path != wantPath ||
						r.URL.Query().Get("valueRenderOption") != "ToString" {
						t.Errorf("unexpected values request: %s %s", r.Method, r.URL)
					}
					values := make([][]any, tt.values)
					for i := range values {
						values[i] = []any{i}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
						"valueRange": map[string]any{"values": values},
					}})
				}
			}))
			defer srv.Close()
			c := &Client{baseURL: srv.URL, appID: "a", appSecret: "s", httpClient: srv.Client()}
			rows, truncated, err := c.readSheetRange(context.Background(), "sht_0")
			if err != nil || len(rows) != min(tt.values, maxTableRows) || truncated != (tt.gridRows > maxTableRows) {
				t.Fatalf("rows=%d truncated=%v err=%v", len(rows), truncated, err)
			}
		})
	}
}

func TestReadSheetRange_PropagatesErrors(t *testing.T) {
	for _, tt := range []struct {
		name     string
		metadata string
		values   string
		want     string
	}{
		{"metadata API error", `{"code":1310213,"msg":"permission denied"}`, "", "1310213"},
		{"metadata HTTP error", "http error", "", "403"},
		{"missing dimensions", `{"code":0,"data":{"sheet":{}}}`, "", "invalid sheet dimensions"},
		{"values API error", "", `{"code":1310213,"msg":"permission denied"}`, "1310213"},
		{"single row exceeds server limit", "", `{"code":90221,"msg":"data exceeded size limit"}`, "90221"},
		{"bounded response still too large", "", strings.Repeat("x", 2049), "response exceeds maximum size"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			valueCalls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
					_, _ = fmt.Fprint(w, `{"code":0,"tenant_access_token":"t","expire":7200}`)
				case strings.Contains(r.URL.Path, "/sheets/v3/"):
					if tt.metadata == "http error" {
						http.Error(w, "permission denied", http.StatusForbidden)
					} else if tt.metadata != "" {
						_, _ = fmt.Fprint(w, tt.metadata)
					} else {
						_, _ = fmt.Fprint(w,
							`{"code":0,"data":{"sheet":{"grid_properties":{"row_count":500,"column_count":1}}}}`)
					}
				default:
					valueCalls++
					_, _ = fmt.Fprint(w, tt.values)
				}
			}))
			defer srv.Close()
			c := &Client{baseURL: srv.URL, appID: "a", appSecret: "s", httpClient: srv.Client(), jsonLimit: 2048}
			rows, truncated, err := c.readSheetRange(context.Background(), "sht_0")
			if err == nil || !strings.Contains(err.Error(), tt.want) || rows != nil || truncated {
				t.Fatalf("rows=%v truncated=%v err=%v, want error containing %q", rows, truncated, err, tt.want)
			}
			if tt.metadata != "" && valueCalls != 0 {
				t.Error("metadata failure must not fall back to an unbounded read")
			}
			if valueCalls > 10 {
				t.Errorf("range reduction did not stop at one row: %d requests", valueCalls)
			}
		})
	}
}

func TestReadBitableRecords_LaterPageError(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		t.Run(strconv.FormatBool(oversized), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
					_, _ = fmt.Fprint(w, `{"code":0,"tenant_access_token":"t","expire":7200}`)
				case strings.HasSuffix(r.URL.Path, "/fields"):
					_, _ = fmt.Fprint(w, `{"code":0,"data":{"items":[{"field_name":"col"}]}}`)
				case r.URL.Query().Get("page_token") == "":
					_, _ = fmt.Fprint(w,
						`{"code":0,"data":{"items":[{"fields":{"col":"value"}}],"has_more":true,"page_token":"next"}}`)
				case oversized:
					_, _ = fmt.Fprint(w, strings.Repeat("x", 2049))
				default:
					_, _ = fmt.Fprint(w, `{"code":1254302,"msg":"permission denied"}`)
				}
			}))
			defer srv.Close()
			c := &Client{baseURL: srv.URL, appID: "a", appSecret: "s", httpClient: srv.Client(), jsonLimit: 2048}
			rows, truncated, err := c.readBitableRecords(context.Background(), "app_table")
			if err == nil || rows != nil || truncated {
				t.Fatalf("later page failure returned partial success: rows=%v truncated=%v err=%v",
					rows, truncated, err)
			}
			if oversized && !errors.Is(err, errResponseTooLarge) {
				t.Fatalf("size error not preserved: %v", err)
			}
			if !oversized && !strings.Contains(err.Error(), "1254302") {
				t.Fatalf("API error not preserved: %v", err)
			}
		})
	}
}

func TestReadBitableRecords_BoundsResponsesAndPreservesRows(t *testing.T) {
	for _, total := range []int{0, 1, 499, 500, 550} {
		t.Run(strconv.Itoa(total), func(t *testing.T) {
			const responseLimit = 32 << 10
			cell := strings.Repeat("x", 128)
			readRecords := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
					_, _ = fmt.Fprint(w, `{"code":0,"tenant_access_token":"t","expire":7200}`)
				case strings.HasSuffix(r.URL.Path, "/fields"):
					_, _ = fmt.Fprint(w, `{"code":0,"data":{"items":[{"field_name":"id"},{"field_name":"text"}]}}`)
				case strings.HasSuffix(r.URL.Path, "/records/search"):
					size, err := strconv.Atoi(r.URL.Query().Get("page_size"))
					if err != nil || size <= 0 {
						t.Error("missing positive page_size")
						http.Error(w, "invalid page size", http.StatusBadRequest)
						return
					}
					start := 0
					if token := r.URL.Query().Get("page_token"); token != "" {
						start, err = strconv.Atoi(strings.TrimPrefix(token, "next+"))
						if err != nil {
							t.Errorf("invalid page token %q", token)
							http.Error(w, "invalid token", http.StatusBadRequest)
							return
						}
					}
					end := min(start+size, total)
					items := make([]map[string]any, end-start)
					for i := range items {
						items[i] = map[string]any{"fields": map[string]any{"id": strconv.Itoa(start + i), "text": cell}}
					}
					readRecords += len(items)
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
						"items": items, "has_more": end < total, "page_token": fmt.Sprintf("next+%d", end),
					}})
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			c := &Client{
				baseURL: srv.URL, appID: "a", appSecret: "s", httpClient: srv.Client(), jsonLimit: responseLimit,
			}
			rows, truncated, err := c.readBitableRecords(context.Background(), "bascabc_tblxyz")
			if err != nil {
				t.Fatalf("bounded bitable read failed: %v", err)
			}
			want := min(total, maxTableRows)
			if len(rows) != want+1 || truncated != (total > maxTableRows) || readRecords != want {
				t.Fatalf("rows=%d truncated=%v read=%d; want %d, %v, %d",
					len(rows), truncated, readRecords, want+1, total > maxTableRows, want)
			}
			if rows[0][0] != "id" || rows[0][1] != "text" {
				t.Fatalf("header changed: %v", rows[0])
			}
			for i, row := range rows[1:] {
				if row[0] != strconv.Itoa(i) || row[1] != cell {
					t.Fatalf("record %d was changed or reordered", i)
				}
			}
		})
	}
}

func TestAppendTableRow_BudgetMatchesRenderedContent(t *testing.T) {
	want := [][]string{{"列|名", "value"}, {"中\n文|", ""}}
	// The builder accounts for the final newline, trimmed by markdownTable.
	budget := len(markdownTable(want)) + 1
	for _, delta := range []int{-1, 0, 1} {
		t.Run(strconv.Itoa(delta), func(t *testing.T) {
			remaining := budget + delta
			var rows [][]string
			if !appendTableRow(&rows, want[0], 2, &remaining) {
				t.Fatal("header did not fit")
			}
			accepted := appendTableRow(&rows, want[1][:1], 2, &remaining)
			if accepted != (delta >= 0) || remaining < 0 {
				t.Fatalf("accepted=%v remaining=%d", accepted, remaining)
			}
			if accepted && markdownTable(rows) != markdownTable(want) {
				t.Fatal("padding or escaping changed the table")
			}
		})
	}
}

func TestEmbeddedTable_UnrenderableBudgetStillShowsNote(t *testing.T) {
	for _, mode := range []string{"wide grid", "escaped first row", "bitable header"} {
		t.Run(mode, func(t *testing.T) {
			recordCalls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
					_, _ = fmt.Fprint(w, `{"tenant_access_token":"t","expire":7200}`)
				case strings.Contains(r.URL.Path, "/sheets/v3/"):
					columns := 1
					if mode == "wide grid" {
						columns = maxTableBytes
					}
					_, _ = fmt.Fprintf(w,
						`{"data":{"sheet":{"grid_properties":{"row_count":1,"column_count":%d}}}}`, columns)
				case strings.HasSuffix(r.URL.Path, "/fields"):
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
						"items": []any{map[string]any{"field_name": strings.Repeat("|", maxTableBytes/2)}},
					}})
				case strings.HasSuffix(r.URL.Path, "/records/search"):
					recordCalls++
				default:
					cell := "value"
					if mode == "escaped first row" {
						cell = strings.Repeat("|", maxTableBytes/2)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
						"valueRange": map[string]any{"values": [][]string{{cell}}},
					}})
				}
			}))
			defer srv.Close()
			c := &Client{baseURL: srv.URL, httpClient: srv.Client()}
			kind := "sheet"
			if mode == "bitable header" {
				kind = "bitable"
			}
			got := inlineTable(context.Background(), c, "token_0", kind)
			if !strings.Contains(got, "未完整读取") || len(got) > 200 || recordCalls != 0 {
				t.Fatalf("missing budget note or continued reading: bytes=%d records=%d", len(got), recordCalls)
			}
		})
	}
}
