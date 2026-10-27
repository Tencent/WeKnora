package chunkcmd_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/cli/cmd"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

func TestChunkList_HasMoreReportsActualLimitTruncation(t *testing.T) {
	cases := []struct {
		name                                       string
		total, limit, pageSize, returned, requests int
		all, more                                  bool
	}{
		{
			name:  "exact default limit",
			total: 50, limit: 50, pageSize: 50, returned: 50, requests: 1, all: true,
		},
		{
			name:  "exact limit across pages",
			total: 4, limit: 4, pageSize: 2, returned: 4, requests: 2, all: true,
		},
		{
			name:  "below limit",
			total: 3, limit: 4, pageSize: 4, returned: 3, requests: 1, all: true,
		},
		{
			name:  "limit leaves known rows",
			total: 4, limit: 3, pageSize: 3, returned: 3, requests: 1, all: true, more: true,
		},
		{
			name:  "limit slices fetched rows",
			total: 4, limit: 3, pageSize: 4, returned: 3, requests: 1, all: true, more: true,
		},
		{
			name:  "single page exact limit",
			total: 3, limit: 3, pageSize: 3, returned: 3, requests: 1,
		},
		{
			name:  "single page window is not limit truncation",
			total: 6, limit: 4, pageSize: 2, returned: 2, requests: 1,
		},
		{
			name:  "empty document",
			total: 0, limit: 4, pageSize: 4, returned: 0, requests: 1, all: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WEKNORA_PROFILE", "")
			out, _ := iostreams.SetForTest(t)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/chunks/doc-1" {
					http.NotFound(w, r)
					return
				}
				requests.Add(1)
				page, err := strconv.Atoi(r.URL.Query().Get("page"))
				if err != nil || page < 1 ||
					r.URL.Query().Get("page_size") != strconv.Itoa(tc.pageSize) {
					http.Error(w, "invalid pagination", http.StatusBadRequest)
					return
				}
				start := (page - 1) * tc.pageSize
				end := start + tc.pageSize
				if end > tc.total {
					end = tc.total
				}
				data := make([]sdk.Chunk, 0)
				for i := start; i < end; i++ {
					data = append(data, sdk.Chunk{ID: fmt.Sprintf("chunk-%d", i), ChunkIndex: i})
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"success": true, "data": data, "total": tc.total,
				})
			}))
			t.Cleanup(server.Close)
			factory := &cmdutil.Factory{
				Client: func() (*sdk.Client, error) { return sdk.NewClient(server.URL), nil },
			}
			root := cmd.NewRootCmd(factory)
			args := []string{
				"chunk", "list", "--doc", "doc-1",
				"--limit", strconv.Itoa(tc.limit),
				"--page-size", strconv.Itoa(tc.pageSize), "--format", "json",
			}
			if tc.all {
				args = append(args, "--all-pages")
			}
			root.SetArgs(args)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			root.SetContext(ctx)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var envelope struct {
				Data []sdk.Chunk `json:"data"`
				Meta struct {
					Count int  `json:"count"`
					Total int  `json:"total_count"`
					More  bool `json:"has_more"`
				} `json:"meta"`
			}
			if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if len(envelope.Data) != tc.returned || envelope.Meta.Count != tc.returned ||
				envelope.Meta.Total != tc.total || envelope.Meta.More != tc.more {
				t.Errorf("data=%d meta=%+v; want count=%d total=%d has_more=%v",
					len(envelope.Data), envelope.Meta, tc.returned, tc.total, tc.more)
			}
			if int(requests.Load()) != tc.requests {
				t.Errorf("requests=%d, want %d", requests.Load(), tc.requests)
			}
			for i, item := range envelope.Data {
				if item.ID != fmt.Sprintf("chunk-%d", i) {
					t.Errorf("chunk order at %d=%q", i, item.ID)
				}
			}
		})
	}
}
