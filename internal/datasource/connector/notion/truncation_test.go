package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/logger"
)

// fakeChildrenPages serves GET /v1/blocks/{blockID}/children in pages of
// pageSize blocks, emulating Notion's pagination contract. The client does not
// pass page_size, so the real API defaults to 100 — the same default used here.
// It returns the number of children requests it saw via the second result.
func fakeChildrenPages(t *testing.T, blockID string, total, pageSize int) (*httptest.Server, *int32) {
	t.Helper()

	var requests int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/blocks/"+blockID+"/children", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)

		offset := 0
		if cursor := r.URL.Query().Get("start_cursor"); cursor != "" {
			n, err := strconv.Atoi(strings.TrimPrefix(cursor, "cursor-"))
			if err != nil {
				http.Error(w, "malformed start_cursor: "+cursor, http.StatusBadRequest)
				return
			}
			offset = n
		}

		end := offset + pageSize
		if end > total {
			end = total
		}
		results := make([]map[string]interface{}, 0, end-offset)
		for i := offset; i < end; i++ {
			results = append(results, map[string]interface{}{
				"object":       "block",
				"id":           fmt.Sprintf("blk-%d", i+1),
				"type":         "paragraph",
				"has_children": false,
				"paragraph":    map[string]interface{}{"rich_text": []interface{}{}},
			})
		}

		hasMore := end < total
		var nextCursor interface{}
		if hasMore {
			nextCursor = fmt.Sprintf("cursor-%d", end)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"object":      "list",
			"results":     results,
			"has_more":    hasMore,
			"next_cursor": nextCursor,
		})
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &requests
}

// captureNotionLogs redirects the project logger into a buffer for the duration
// of the test. ConfigureFromEnv in cleanup restores output and level.
func captureNotionLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	logger.SetLogLevel(logger.LevelDebug)
	logger.SetOutput(&buf)
	t.Cleanup(func() { logger.ConfigureFromEnv() })
	return &buf
}

// TestBlockCapWarnsOnTruncation drives the real paging loop against a fake
// children endpoint holding 1001 blocks. The cap must still stop at 1000
// (behavior unchanged), the 1001st block must never be requested, and the
// truncation must be reported instead of happening silently.
func TestBlockCapWarnsOnTruncation(t *testing.T) {
	ts, requests := fakeChildrenPages(t, "big-page", 1001, 100)
	buf := captureNotionLogs(t)

	client := mustTestClient(t, "test-token", ts.URL)
	blocks, err := client.GetBlockChildrenAll(context.Background(), "big-page")
	if err != nil {
		t.Fatalf("GetBlockChildrenAll() error: %v", err)
	}
	if len(blocks) != maxBlocksPerPage {
		t.Fatalf("blocks = %d, want %d (the cap itself must not change)", len(blocks), maxBlocksPerPage)
	}
	if got := atomic.LoadInt32(requests); got != 10 {
		t.Errorf("children requests = %d, want 10 (no page may be requested past the cap)", got)
	}

	out := buf.String()
	if !strings.Contains(out, "WARN") {
		t.Fatalf("expected a Warn log entry, got:\n%s", out)
	}
	for _, want := range []string{"[Notion]", "big-page", "exceeded 1000 blocks", "truncating"} {
		if !strings.Contains(out, want) {
			t.Errorf("truncation warning missing %q, got:\n%s", want, out)
		}
	}
}

// TestBlockCapExactlyAtLimitDoesNotWarn guards the off-by-one at the cap: a
// document whose last page ends exactly at maxBlocksPerPage has dropped
// nothing, so no truncation warning may be emitted.
func TestBlockCapExactlyAtLimitDoesNotWarn(t *testing.T) {
	ts, _ := fakeChildrenPages(t, "exact-page", maxBlocksPerPage, 100)
	buf := captureNotionLogs(t)

	client := mustTestClient(t, "test-token", ts.URL)
	blocks, err := client.GetBlockChildrenAll(context.Background(), "exact-page")
	if err != nil {
		t.Fatalf("GetBlockChildrenAll() error: %v", err)
	}
	if len(blocks) != maxBlocksPerPage {
		t.Fatalf("blocks = %d, want %d", len(blocks), maxBlocksPerPage)
	}
	if out := buf.String(); strings.Contains(out, "truncating") {
		t.Errorf("exactly %d blocks is not truncation, got:\n%s", maxBlocksPerPage, out)
	}
}

func TestBlocksTruncated(t *testing.T) {
	cases := []struct {
		name         string
		currentCount int
		hasMore      bool
		nextCursor   string
		want         bool
	}{
		{"cap reached with a next page", 1000, true, "cursor-1000", true},
		{"cap overshot by a partial page", 1050, true, "cursor-1050", true},
		{"cap reached on the final page", 1000, false, "", false},
		{"cap reached and has_more cleared", 1000, false, "cursor-1000", false},
		{"cap reached but cursor empty", 1000, true, "", false},
		{"below the cap", 999, true, "cursor-999", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := blocksTruncated(tc.currentCount, tc.hasMore, tc.nextCursor); got != tc.want {
				t.Errorf("blocksTruncated(%d, %v, %q) = %v, want %v",
					tc.currentCount, tc.hasMore, tc.nextCursor, got, tc.want)
			}
		})
	}
}
