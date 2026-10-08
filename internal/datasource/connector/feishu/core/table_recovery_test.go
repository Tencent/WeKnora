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
	}{
		{"client dense", false, false},
		{"client sparse", true, false},
		{"server dense", false, true},
		{"server sparse", true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			want := make([][]string, 12)
			for i := range want {
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
				for len(values) > 0 && len(values[len(values)-1]) == 0 {
					values = values[:len(values)-1]
				}
				body, _ := json.Marshal(map[string]any{"code": 0, "data": map[string]any{
					"valueRange": map[string]any{"values": values},
				}})
				if tt.serverLimit && len(body) > 2048 {
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
			if err == nil || rows != nil || truncated {
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
