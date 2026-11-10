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

// childrenPageFunc decides, for the page covering [offset, end), whether the
// response claims another page and which cursor it carries.
type childrenPageFunc func(offset, end int) (hasMore bool, nextCursor interface{})

// fakeChildrenPages serves GET /v1/blocks/{blockID}/children in pages of
// pageSize blocks, emulating Notion's pagination contract. The client does not
// pass page_size, so the real API defaults to 100 — the same default used here.
// It returns the number of children requests it saw via the second result.
func fakeChildrenPages(t *testing.T, blockID string, total, pageSize int) (*httptest.Server, *int32) {
	t.Helper()
	return fakeChildrenPagesShape(t, blockID, total, pageSize, func(_, end int) (bool, interface{}) {
		if end < total {
			return true, fmt.Sprintf("cursor-%d", end)
		}
		return false, nil
	})
}

// fakeChildrenPagesShape is fakeChildrenPages with per-page control over
// has_more/next_cursor, so a test can reproduce responses the documented
// contract does not promise (e.g. has_more=true with an empty cursor).
func fakeChildrenPagesShape(t *testing.T, blockID string, total, pageSize int,
	page childrenPageFunc,
) (*httptest.Server, *int32) {
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

		hasMore, nextCursor := page(offset, end)
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

// captureBlockCapLogs redirects the project logger into a buffer for the duration
// of the test. ConfigureFromEnv in cleanup restores output and level.
func captureBlockCapLogs(t *testing.T) *bytes.Buffer {
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
	buf := captureBlockCapLogs(t)

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
	if strings.Contains(out, "without a next_cursor") {
		t.Errorf("well-formed pagination must not trigger the cursor warning, got:\n%s", out)
	}
}

// TestBlockCapWarnsWhenHasMoreHasNoCursor covers the response the documented
// contract does not promise: the page that reaches the cap claims has_more=true
// but carries no next_cursor, so pagination cannot continue. Both facts must be
// reported — the cap hit and the unusable cursor. This is the exact cell that
// used to be silent: the old predicate required a non-empty cursor, so the cap
// warning was skipped and the dropped tail left no trace at all.
func TestBlockCapWarnsWhenHasMoreHasNoCursor(t *testing.T) {
	const total = maxBlocksPerPage + 1
	ts, requests := fakeChildrenPagesShape(t, "odd-page", total, 100, func(_, end int) (bool, interface{}) {
		if end == maxBlocksPerPage {
			return true, nil // has_more=true with no usable cursor
		}
		if end < total {
			return true, fmt.Sprintf("cursor-%d", end)
		}
		return false, nil
	})
	buf := captureBlockCapLogs(t)

	client := mustTestClient(t, "test-token", ts.URL)
	blocks, err := client.GetBlockChildrenAll(context.Background(), "odd-page")
	if err != nil {
		t.Fatalf("GetBlockChildrenAll() error: %v", err)
	}
	if len(blocks) != maxBlocksPerPage {
		t.Fatalf("blocks = %d, want %d (the cap itself must not change)", len(blocks), maxBlocksPerPage)
	}
	if got := atomic.LoadInt32(requests); got != 10 {
		t.Errorf("children requests = %d, want 10 (an empty cursor must stop pagination)", got)
	}

	out := buf.String()
	if !strings.Contains(out, "WARN") {
		t.Fatalf("expected a Warn log entry, got:\n%s", out)
	}
	for _, want := range []string{
		"[Notion]", "odd-page", "exceeded 1000 blocks",
		"has_more=true", "without a next_cursor", "remaining blocks are not synced",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("warning missing %q, got:\n%s", want, out)
		}
	}
}

// TestHasMoreWithoutCursorWarnsBelowTheCap pins the other half of the anomaly:
// has_more=true with an empty cursor stops pagination no matter how far below
// the cap the client is, so a dropped tail must be reported there as well — the
// cap warning is not what makes this case observable.
func TestHasMoreWithoutCursorWarnsBelowTheCap(t *testing.T) {
	const total = 500
	ts, requests := fakeChildrenPagesShape(t, "odd-small-page", total, 100, func(_, end int) (bool, interface{}) {
		if end == 300 {
			return true, nil // has_more=true with no usable cursor
		}
		if end < total {
			return true, fmt.Sprintf("cursor-%d", end)
		}
		return false, nil
	})
	buf := captureBlockCapLogs(t)

	client := mustTestClient(t, "test-token", ts.URL)
	blocks, err := client.GetBlockChildrenAll(context.Background(), "odd-small-page")
	if err != nil {
		t.Fatalf("GetBlockChildrenAll() error: %v", err)
	}
	if len(blocks) != 300 {
		t.Fatalf("blocks = %d, want 300 (pagination stops at the cursorless page)", len(blocks))
	}
	if got := atomic.LoadInt32(requests); got != 3 {
		t.Errorf("children requests = %d, want 3 (no page may be requested without a cursor)", got)
	}

	out := buf.String()
	if !strings.Contains(out, "WARN") {
		t.Fatalf("expected a Warn log entry, got:\n%s", out)
	}
	for _, want := range []string{
		"[Notion]", "odd-small-page", "has_more=true", "without a next_cursor",
		"after 300 blocks", "remaining blocks are not synced",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("cursor warning missing %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "exceeded 1000 blocks") {
		t.Errorf("below the cap there is no truncation to report, got:\n%s", out)
	}
}

// TestBlockCapExactlyAtLimitDoesNotWarn guards the off-by-one at the cap: a
// document whose last page ends exactly at maxBlocksPerPage has dropped
// nothing, so no truncation warning may be emitted.
func TestBlockCapExactlyAtLimitDoesNotWarn(t *testing.T) {
	ts, _ := fakeChildrenPages(t, "exact-page", maxBlocksPerPage, 100)
	buf := captureBlockCapLogs(t)

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
	// has_more is the only signal consulted: "cap hit while has_more=true but
	// next_cursor empty" is no longer classified as "not truncated" here — the
	// paging loop warns about it unconditionally (see
	// TestBlockCapWarnsWhenHasMoreHasNoCursor and
	// TestHasMoreWithoutCursorWarnsBelowTheCap).
	cases := []struct {
		name         string
		currentCount int
		hasMore      bool
		want         bool
	}{
		{"cap reached with a next page", 1000, true, true},
		{"cap overshot by a partial page", 1050, true, true},
		{"cap reached on the final page", 1000, false, false},
		{"cap overshot on the final page", 1050, false, false},
		{"below the cap", 999, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := blocksTruncated(tc.currentCount, tc.hasMore); got != tc.want {
				t.Errorf("blocksTruncated(%d, %v) = %v, want %v",
					tc.currentCount, tc.hasMore, got, tc.want)
			}
		})
	}
}
