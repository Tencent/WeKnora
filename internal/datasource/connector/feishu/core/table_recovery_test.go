package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReadSheetRange_SplitsOversizedRanges(t *testing.T) {
	for _, tt := range []struct {
		name        string
		sparse      bool
		serverLimit bool
		status      int
	}{
		{"client dense", false, false, 200},
		{"client sparse", true, false, 200},
		{"server dense", false, true, 200},
		{"server sparse", true, true, 200},
		{"server HTTP 400", false, true, 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			want := make([][]string, 12)
			for i := range want {
				want[i] = []string{"", ""}
				if !tt.sparse || i == 0 || i == 1 || i == 2 || i == 8 || i == 11 {
					want[i] = []string{strconv.Itoa(i), strings.Repeat("汉|", 200)}
				}
			}
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/sheets/v3/") {
					_, _ = fmt.Fprint(w,
						`{"code":0,"data":{"sheet":{"grid_properties":{"row_count":12,"column_count":2}}}}`)
					return
				}
				calls++
				var start, end int
				name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
				_, err := fmt.Sscanf(name, "0!A%d:B%d", &start, &end)
				if err != nil || start < 1 || end > len(want) || start > end {
					t.Errorf("invalid range %q", name)
					http.Error(w, "bad range", 400)
					return
				}
				values := want[start-1 : end]
				// The API omits trailing empty rows in each range, but keeps
				// leading/interior rows so cell coordinates remain meaningful.
				for len(values) > 0 && values[len(values)-1][0] == "" {
					values = values[:len(values)-1]
				}
				body, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{
					"valueRange": map[string]any{"values": values},
				}})
				if tt.serverLimit && len(body) > 2048 {
					w.WriteHeader(tt.status)
					_, _ = fmt.Fprint(w, `{"code":90221,"msg":"data exceeded size limit"}`)
					return
				}
				_, _ = w.Write(body)
			}))
			defer srv.Close()
			c := &Client{
				baseURL: srv.URL, httpClient: srv.Client(), tokenCache: "t",
				tokenExpAt: time.Now().Add(time.Hour), jsonLimit: 2048,
			}
			rows, truncated, err := c.readSheetRange(context.Background(), "sht_0")
			if err != nil || truncated || len(rows) != len(want) {
				t.Fatalf("rows=%d truncated=%v err=%v", len(rows), truncated, err)
			}
			for i := range want {
				if !slices.Equal(rows[i], want[i]) {
					t.Fatalf("source row %d moved or changed", i+1)
				}
			}
			if calls < 2 || calls > 2*len(want) {
				t.Fatalf("expected bounded range splitting, got %d calls", calls)
			}
		})
	}
}

func TestReadBitableRecords_ShrinksOversizedLaterPage(t *testing.T) {
	const total = 125
	oversized := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/fields") {
			_, _ = fmt.Fprint(w, `{"code":0,"data":{"items":[{"field_name":"id"},{"field_name":"text"}]}}`)
			return
		}
		size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
		start, _ := strconv.Atoi(r.URL.Query().Get("page_token"))
		end := min(start+size, total)
		items := make([]map[string]any, 0, end-start)
		for i := start; i < end; i++ {
			cell := "ok"
			if i >= 100 {
				cell = strings.Repeat("测", 200)
			}
			items = append(items, map[string]any{"fields": map[string]any{"id": strconv.Itoa(i), "text": cell}})
		}
		body, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{
			"items": items, "has_more": end < total, "page_token": strconv.Itoa(end),
		}})
		if len(body) > 8192 {
			oversized++
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	c := &Client{
		baseURL: srv.URL, httpClient: srv.Client(), tokenCache: "t",
		tokenExpAt: time.Now().Add(time.Hour), jsonLimit: 8192,
	}
	rows, truncated, err := c.readBitableRecords(context.Background(), "app_table")
	if err != nil || truncated || len(rows) != total+1 {
		t.Fatalf("rows=%d truncated=%v err=%v", len(rows), truncated, err)
	}
	for i, row := range rows[1:] {
		wantText := "ok"
		if i >= 100 {
			wantText = strings.Repeat("测", 200)
		}
		if row[0] != strconv.Itoa(i) || row[1] != wantText {
			t.Fatalf("record %d changed or out of order", i)
		}
	}
	if oversized == 0 {
		t.Fatal("fixture did not exercise the response limit")
	}
}

func TestEmbeddedTable_SingleItemTooLarge(t *testing.T) {
	for _, kind := range []string{"sheet", "bitable"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/sheets/v3/") {
					_, _ = fmt.Fprint(w,
						`{"code":0,"data":{"sheet":{"grid_properties":{"row_count":500,"column_count":1}}}}`)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/fields") {
					_, _ = fmt.Fprint(w, `{"code":0,"data":{"items":[{"field_name":"text"}]}}`)
					return
				}
				calls++
				// Even a one-item response cannot fit; splitting must terminate.
				_, _ = fmt.Fprintf(w, `{"value":"%s"}`, strings.Repeat("x", 2048))
			}))
			defer srv.Close()
			c := &Client{
				baseURL: srv.URL, httpClient: srv.Client(), tokenCache: "t",
				tokenExpAt: time.Now().Add(time.Hour), jsonLimit: 1024,
			}
			var rows [][]string
			var err error
			if kind == "sheet" {
				rows, _, err = c.readSheetRange(context.Background(), "sht_0")
			} else {
				rows, _, err = c.readBitableRecords(context.Background(), "app_table")
			}
			if !errors.Is(err, errResponseTooLarge) || rows != nil {
				t.Fatalf("rows=%d err=%v", len(rows), err)
			}
			if calls > 10 {
				t.Fatalf("splitting did not terminate promptly: %d calls", calls)
			}
		})
	}
}

func TestReadBitableRecords_RejectsBrokenPagination(t *testing.T) {
	for _, mode := range []string{"empty", "missing", "repeated", "cycle"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/fields") {
					_, _ = fmt.Fprint(w, `{"code":0,"data":{"items":[{"field_name":"id"}]}}`)
					return
				}
				calls++
				token := "next"
				items := []map[string]any{{"fields": map[string]any{"id": strconv.Itoa(calls)}}}
				switch mode {
				case "empty":
					if calls > 1 {
						items = nil
					}
				case "missing":
					token = ""
				case "cycle":
					if calls == 2 {
						token = "other"
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
					"items": items, "has_more": true, "page_token": token,
				}})
			}))
			defer srv.Close()
			c := &Client{
				baseURL: srv.URL, httpClient: srv.Client(), tokenCache: "t", tokenExpAt: time.Now().Add(time.Hour),
			}
			rows, truncated, err := c.readBitableRecords(context.Background(), "app_table")
			if mode == "empty" || mode == "missing" {
				if err != nil || !truncated || len(rows) != 2 || rows[1][0] != "1" {
					t.Fatalf("lost partial records: rows=%v truncated=%v err=%v", rows, truncated, err)
				}
			} else if err == nil || rows != nil || truncated {
				t.Fatalf("pagination returned partial success: rows=%d truncated=%v err=%v", len(rows), truncated, err)
			}
			if calls > 3 {
				t.Fatalf("failed to stop broken pagination: %d calls", calls)
			}
		})
	}
}

func TestReadSheetRange_TrimsOnlyEmptyTail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/sheets/v3/") {
			_, _ = fmt.Fprint(w,
				`{"code":0,"data":{"sheet":{"grid_properties":{"row_count":1000,"column_count":2}}}}`)
			return
		}
		_, _ = fmt.Fprint(w,
			`{"code":0,"data":{"valueRange":{"values":[["id","value"],["",null],[0,false],[null,""] ,[]]}}}`)
	}))
	defer srv.Close()
	c := &Client{
		baseURL: srv.URL, httpClient: srv.Client(), tokenCache: "t", tokenExpAt: time.Now().Add(time.Hour),
	}
	rows, limited, err := c.readSheetRange(context.Background(), "sht_0")
	if err != nil || !limited || len(rows) != 3 {
		t.Fatalf("rows=%d limited=%v err=%v", len(rows), limited, err)
	}
	if !slices.Equal(rows[1], []string{"", ""}) || !slices.Equal(rows[2], []string{"0", "false"}) {
		t.Fatalf("interior gap or false-valued cells changed: %v", rows)
	}
}

func TestReadSheetRange_EmptyFirstPageRendersLaterRows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/sheets/v3/") {
			_, _ = fmt.Fprint(w, `{"code":0,"data":{"sheet":{"grid_properties":{"row_count":4,"column_count":2}}}}`)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "A1:B4"):
			_, _ = fmt.Fprint(w, `{"code":90221}`)
		case strings.HasSuffix(r.URL.Path, "A1:B2"):
			_, _ = fmt.Fprint(w, `{"code":0,"data":{"valueRange":{"values":[]}}}`)
		case strings.HasSuffix(r.URL.Path, "A3:B4"):
			_, _ = fmt.Fprint(w, `{"code":0,"data":{"valueRange":{"values":[["later","value"]]}}}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &Client{
		baseURL: srv.URL, httpClient: srv.Client(), tokenCache: "t", tokenExpAt: time.Now().Add(time.Hour),
	}
	got := inlineTable(context.Background(), c, "sht_0", "sheet")
	if !strings.Contains(got, "| later | value |") || strings.Count(got, "\n") != 3 {
		t.Fatalf("later rows disappeared or moved: %q", got)
	}
}

func TestEmbeddedTable_CumulativeContentBudget(t *testing.T) {
	for _, kind := range []string{"sheet", "bitable"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			// Each page fits the response cap; their accumulated content does not.
			cell := strings.Repeat("|", 3<<20)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/sheets/v3/") {
					_, _ = fmt.Fprint(w,
						`{"code":0,"data":{"sheet":{"grid_properties":{"row_count":8,"column_count":1}}}}`)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/fields") {
					_, _ = fmt.Fprint(w, `{"code":0,"data":{"items":[{"field_name":"text"}]}}`)
					return
				}
				calls++
				if kind == "sheet" {
					var start, end int
					name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
					if _, err := fmt.Sscanf(name, "0!A%d:A%d", &start, &end); err != nil {
						t.Error(err)
						return
					}
					if end > start {
						_, _ = fmt.Fprint(w, `{"code":90221}`)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{
						"data": map[string]any{"valueRange": map[string]any{"values": [][]string{{cell}}}},
					})
				} else {
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
						"items":    []any{map[string]any{"fields": map[string]any{"text": cell}}},
						"has_more": calls < 8, "page_token": strconv.Itoa(calls),
					}})
				}
			}))
			defer srv.Close()
			c := &Client{
				baseURL: srv.URL, httpClient: srv.Client(), tokenCache: "t", tokenExpAt: time.Now().Add(time.Hour),
			}
			// Bound the fixture even when exercising the buggy reader.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var rows [][]string
			var truncated bool
			var err error
			if kind == "sheet" {
				rows, truncated, err = c.readSheetRange(ctx, "sht_0")
			} else {
				rows, truncated, err = c.readBitableRecords(ctx, "app_table")
			}
			if err != nil || !truncated || len(rows) < 1 {
				t.Fatalf("rows=%d truncated=%v err=%v", len(rows), truncated, err)
			}
			rendered := markdownTable(rows)
			if len(rendered) > maxTableBytes {
				t.Fatalf("retained table exceeds budget: %d bytes", len(rendered))
			}
			if calls > 12 {
				t.Fatalf("continued fetching past content budget: %d calls", calls)
			}
		})
	}
}

func TestEmbeddedTable_PageSizeRecoversAfterLargeItem(t *testing.T) {
	for _, kind := range []string{"sheet", "bitable"} {
		t.Run(kind, func(t *testing.T) {
			const total = 200
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/sheets/v3/") {
					_, _ = fmt.Fprintf(w,
						`{"data":{"sheet":{"grid_properties":{"row_count":%d,"column_count":1}}}}`, total)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/fields") {
					_, _ = fmt.Fprint(w, `{"data":{"items":[{"field_name":"id"}]}}`)
					return
				}
				calls++
				var start, end int
				if kind == "sheet" {
					name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
					if _, err := fmt.Sscanf(name, "0!A%d:A%d", &start, &end); err != nil {
						t.Error(err)
						return
					}
					start--
				} else {
					start, _ = strconv.Atoi(r.URL.Query().Get("page_token"))
					size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
					end = min(start+size, total)
				}
				// Simulate a first item which only fits when requested alone.
				if start == 0 && end > 1 {
					if kind == "sheet" {
						_, _ = fmt.Fprint(w, `{"code":90221}`)
					} else {
						_, _ = fmt.Fprint(w, strings.Repeat("x", 8193))
					}
					return
				}
				if kind == "sheet" {
					values := make([][]string, 0, end-start)
					for i := start; i < end; i++ {
						values = append(values, []string{strconv.Itoa(i)})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{
						"data": map[string]any{"valueRange": map[string]any{"values": values}},
					})
				} else {
					items := make([]any, 0, end-start)
					for i := start; i < end; i++ {
						items = append(items, map[string]any{"fields": map[string]any{"id": strconv.Itoa(i)}})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
						"items": items, "has_more": end < total, "page_token": strconv.Itoa(end),
					}})
				}
			}))
			defer srv.Close()
			c := &Client{
				baseURL: srv.URL, httpClient: srv.Client(), jsonLimit: 8192,
				tokenCache: "t", tokenExpAt: time.Now().Add(time.Hour),
			}
			var rows [][]string
			var truncated bool
			var err error
			if kind == "sheet" {
				rows, truncated, err = c.readSheetRange(context.Background(), "sht_0")
			} else {
				rows, truncated, err = c.readBitableRecords(context.Background(), "app_table")
				if len(rows) > 0 {
					rows = rows[1:]
				}
			}
			if err != nil || truncated || len(rows) != total {
				t.Fatalf("rows=%d truncated=%v err=%v", len(rows), truncated, err)
			}
			for i, row := range rows {
				if len(row) != 1 || row[0] != strconv.Itoa(i) {
					t.Fatalf("row %d lost, duplicated or reordered", i)
				}
			}
			if calls > 20 {
				t.Fatalf("a single large item left subsequent reads too small: %d calls", calls)
			}
		})
	}
}
