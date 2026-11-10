package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"github.com/Tencent/WeKnora/internal/logger"
)

// paginationHopCap mirrors maxPaginationHops in client.go. It is spelled as a
// literal so this file also compiles — and then fails on behaviour — against a
// tree that has no guard at all.
const paginationHopCap = 10000

// fakeLoopBudget bounds an unguarded run a little above the guard's own cap:
// the fake answers 400 once the budget is gone, so a regression fails with the
// wrong error instead of paging until the test times out.
const fakeLoopBudget = paginationHopCap + 5

// paginationTestClient returns a client pointed at the fake server. The
// production 3 req/s limiter would turn a 10000-hop regression into 55 minutes
// of waiting, so it is replaced with an unlimited one: this test is about the
// loop's exit conditions, not its pacing.
func paginationTestClient(t *testing.T, server *httptest.Server) *notionClient {
	t.Helper()
	client := mustTestClient(t, "test-token", server.URL)
	client.limiter = rate.NewLimiter(rate.Inf, 1)
	return client
}

func writeListPage(w http.ResponseWriter, results string, hasMore bool, nextCursor string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"object":"list","results":%s,"has_more":%t,"next_cursor":%q}`,
		results, hasMore, nextCursor)
}

// Notion can answer has_more=true with a next_cursor it already returned. The
// loop used to follow it forever; it must now stop as soon as a cursor repeats.
func TestPaginatePagesRejectsRepeatedCursor(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) > fakeLoopBudget {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writeListPage(w, `[]`, true, "same-cursor")
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := paginationTestClient(t, server).paginatePages(ctx, http.MethodPost, "/v1/search")
	require.Error(t, err)
	require.Equal(t, int64(2), requests.Load(),
		"the guard must reject the repeated cursor on the second page; err=%v", err)
	require.Contains(t, err.Error(), `repeated next_cursor "same-cursor"`)
}

// A vendor that invents a new cursor for every page never repeats one, so the
// repeat guard cannot stop it: the hop cap must, and it must log why.
func TestPaginatePagesStopsAtPaginationCap(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := requests.Add(1)
		if n > fakeLoopBudget {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writeListPage(w, `[]`, true, fmt.Sprintf("cursor-%d", n))
	}))
	defer server.Close()

	var logs bytes.Buffer
	logger.SetOutput(&logs)
	defer logger.SetOutput(os.Stdout)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, err := paginationTestClient(t, server).paginatePages(ctx, http.MethodPost, "/v1/search")
	require.Error(t, err)
	require.Contains(t, err.Error(), "pagination exceeded 10000 pages")
	require.Equal(t, int64(paginationHopCap), requests.Load())
	require.Contains(t, logs.String(), "pagination exceeded 10000 pages",
		"hitting the hop cap must be visible in the log")
}

// The guard must not truncate a listing that genuinely spans several pages.
func TestPaginatePagesMergesRealPages(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		_ = json.Unmarshal(body, &req)
		if cursor, _ := req["start_cursor"].(string); cursor == "" {
			writeListPage(w, `[{"id":"page-1","object":"page"}]`, true, "cursor-2")
			return
		}
		writeListPage(w, `[{"id":"page-2","object":"page"}]`, false, "")
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pages, err := paginationTestClient(t, server).paginatePages(ctx, http.MethodPost, "/v1/search")
	require.NoError(t, err)
	require.Len(t, pages, 2)
	require.Equal(t, "page-1", pages[0].ID)
	require.Equal(t, "page-2", pages[1].ID)
	require.Equal(t, int64(2), requests.Load())
}

// GetBlockChildrenFlat is the same cursor loop on the block endpoint and used
// to spin the same way during resource discovery.
func TestGetBlockChildrenFlatRejectsRepeatedCursor(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) > fakeLoopBudget {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writeListPage(w, `[]`, true, "same-cursor")
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := paginationTestClient(t, server).GetBlockChildrenFlat(ctx, "block-1")
	require.Error(t, err)
	require.Equal(t, int64(2), requests.Load(),
		"the guard must reject the repeated cursor on the second page; err=%v", err)
	require.Contains(t, err.Error(), `repeated next_cursor "same-cursor"`)
}

// GetBlockChildrenAll is the recursive sibling of GetBlockChildrenFlat and
// carried the same unbounded cursor loop. Its own maxBlocksPerPage fallback
// counts blocks already appended, so a vendor answering empty results with
// has_more=true keeps that counter at zero forever and the loop never exits.
func TestGetBlockChildrenAllRejectsRepeatedCursor(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) > fakeLoopBudget {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writeListPage(w, `[]`, true, "same-cursor")
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := paginationTestClient(t, server).GetBlockChildrenAll(ctx, "block-1")
	require.Error(t, err)
	require.Equal(t, int64(2), requests.Load(),
		"the guard must reject the repeated cursor on the second page; err=%v", err)
	require.Contains(t, err.Error(), `repeated next_cursor "same-cursor"`)
}

// A vendor that mints a fresh cursor for every empty page never repeats one, so
// only the hop cap can end the recursive walk.
func TestGetBlockChildrenAllStopsAtPaginationCap(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := requests.Add(1)
		if n > fakeLoopBudget {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writeListPage(w, `[]`, true, fmt.Sprintf("cursor-%d", n))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, err := paginationTestClient(t, server).GetBlockChildrenAll(ctx, "block-1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "pagination exceeded 10000 pages")
	require.Equal(t, int64(paginationHopCap), requests.Load())
}

// The recursive walk must still merge a genuinely multi-page listing.
func TestGetBlockChildrenAllMergesRealPages(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Query().Get("start_cursor") == "" {
			writeListPage(w, `[{"id":"block-1","type":"paragraph",`+
				`"paragraph":{"rich_text":[{"type":"text","text":{"content":"one"},"plain_text":"one"}]}}]`,
				true, "cursor-2")
			return
		}
		writeListPage(w, `[{"id":"block-2","type":"paragraph",`+
			`"paragraph":{"rich_text":[{"type":"text","text":{"content":"two"},"plain_text":"two"}]}}]`,
			false, "")
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	blocks, err := paginationTestClient(t, server).GetBlockChildrenAll(ctx, "block-1")
	require.NoError(t, err)
	require.Len(t, blocks, 2)
	require.Equal(t, "block-1", blocks[0].ID)
	require.Equal(t, "block-2", blocks[1].ID)
	require.Equal(t, int64(2), requests.Load())
}

// Data source queries page through queryDataSourceWindow since #3845, so the
// repeated-cursor guard has to cover that loop too.
func TestQueryDataSourceAllRejectsRepeatedCursor(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) > fakeLoopBudget {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writeListPage(w, `[]`, true, "same-cursor")
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := paginationTestClient(t, server).queryDataSourceAll(ctx, "/v1/data_sources/ds-1/query")
	require.Error(t, err)
	require.Equal(t, int64(2), requests.Load(),
		"the guard must reject the repeated cursor on the second page; err=%v", err)
	require.Contains(t, err.Error(), `repeated next_cursor "same-cursor"`)
}
