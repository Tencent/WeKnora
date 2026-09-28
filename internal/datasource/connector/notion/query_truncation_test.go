package notion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// A Notion data source query stops at 10,000 results and reports has_more=false
// together with request_status.type="incomplete". The tests below pin down that a
// capped result set stays distinguishable from a complete one and that rows it no
// longer reports are not mistaken for source-side deletions.
// See https://developers.notion.com/guides/data-apis/query-large-data-sources

const truncationReason = "query_result_limit_reached"

func truncationIncomplete() map[string]interface{} {
	return map[string]interface{}{"type": "incomplete", "incomplete_reason": truncationReason}
}

func truncationRecord(id string) map[string]interface{} {
	return map[string]interface{}{
		"id":               id,
		"object":           "page",
		"url":              "https://notion.so/" + id,
		"last_edited_time": "2026-01-02T10:00:00.000Z",
		"in_trash":         false,
		"parent":           map[string]interface{}{"type": "data_source_id", "data_source_id": "ds-1"},
		"properties": map[string]interface{}{
			"Name": map[string]interface{}{
				"type":  "title",
				"title": []interface{}{map[string]interface{}{"plain_text": "Row " + id}},
			},
		},
	}
}

func truncationDataSourceRow() map[string]interface{} {
	return map[string]interface{}{
		"id":               "ds-1",
		"object":           "data_source",
		"url":              "https://notion.so/ds-1",
		"last_edited_time": "2026-01-02T10:00:00.000Z",
		"in_trash":         false,
		"parent":           map[string]interface{}{"type": "workspace", "workspace": true},
		"title":            []interface{}{map[string]interface{}{"plain_text": "Large Database"}},
	}
}

type truncationQueryPage struct {
	rows          []interface{}
	hasMore       bool
	nextCursor    string
	requestStatus map[string]interface{}
}

type truncationFake struct {
	searchResults []interface{}
	queryPages    []truncationQueryPage
	blocks        map[string][]interface{}
}

// newTruncationServer emulates a workspace whose data source query answers with
// the given pages. It returns the server and the number of query requests served.
func newTruncationServer(t *testing.T, fake truncationFake) (*httptest.Server, *int32) {
	t.Helper()
	var queryCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/search":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"object": "list", "results": fake.searchResults, "has_more": false, "next_cursor": nil,
			})
		case r.URL.Path == "/v1/data_sources/ds-1" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "ds-1", "object": "data_source",
				"title": []interface{}{map[string]interface{}{"plain_text": "Large Database"}},
			})
		case r.URL.Path == "/v1/data_sources/ds-1/query":
			call := int(atomic.AddInt32(&queryCalls, 1)) - 1
			if call >= len(fake.queryPages) {
				http.Error(w, "unexpected extra query request", http.StatusInternalServerError)
				return
			}
			page := fake.queryPages[call]
			resp := map[string]interface{}{
				"object": "list", "results": page.rows, "has_more": page.hasMore, "next_cursor": nil,
			}
			if page.nextCursor != "" {
				resp["next_cursor"] = page.nextCursor
			}
			if page.requestStatus != nil {
				resp["request_status"] = page.requestStatus
			}
			_ = json.NewEncoder(w).Encode(resp)
		case strings.HasPrefix(r.URL.Path, "/v1/blocks/"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/blocks/"), "/children")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"object": "list", "results": fake.blocks[id], "has_more": false, "next_cursor": nil,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, &queryCalls
}

func truncationPrevCursor() *types.SyncCursor {
	jan1 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	return buildCursor(map[string]time.Time{
		"ds-1": jan1,
		"r1":   jan1,
		"r2":   jan1,
		"r3":   jan1,
		"r4":   jan1,
	})
}

func TestPaginatedResponseParsesRequestStatus(t *testing.T) {
	t.Run("incomplete", func(t *testing.T) {
		var resp paginatedResponse
		require.NoError(t, json.Unmarshal([]byte(`{"object":"list","results":[],"has_more":false,`+
			`"next_cursor":null,"request_status":{"type":"incomplete",`+
			`"incomplete_reason":"`+truncationReason+`"}}`), &resp))
		require.True(t, resp.isIncomplete())
		require.Equal(t, truncationReason, resp.RequestStatus.IncompleteReason)
	})

	t.Run("absent", func(t *testing.T) {
		var resp paginatedResponse
		require.NoError(t, json.Unmarshal([]byte(`{"object":"list","results":[],"has_more":false}`), &resp))
		require.False(t, resp.isIncomplete())
		require.Nil(t, resp.RequestStatus)
	})

	t.Run("explicit complete", func(t *testing.T) {
		var resp paginatedResponse
		require.NoError(t, json.Unmarshal([]byte(`{"results":[],"has_more":false,`+
			`"request_status":{"type":"complete"}}`), &resp))
		require.False(t, resp.isIncomplete())
	})
}

func TestQueryDatabaseAllReportsTruncatedResult(t *testing.T) {
	server, queryCalls := newTruncationServer(t, truncationFake{
		queryPages: []truncationQueryPage{{
			rows:          []interface{}{truncationRecord("r1"), truncationRecord("r2")},
			requestStatus: truncationIncomplete(),
		}},
	})
	client := mustTestClient(t, "test-token", server.URL)

	records, err := client.QueryDatabaseAll(context.Background(), "ds-1")
	require.ErrorIs(t, err, errQueryResultTruncated, "a capped query must not look like a complete read")
	require.Contains(t, err.Error(), truncationReason)
	require.Len(t, records, 2, "rows the capped page did return stay available to the caller")
	require.Equal(t, int32(1), atomic.LoadInt32(queryCalls), "a capped query must not be retried")
}

func TestQueryDatabaseAllChecksRequestStatusOnEveryPage(t *testing.T) {
	server, queryCalls := newTruncationServer(t, truncationFake{
		queryPages: []truncationQueryPage{
			{rows: []interface{}{truncationRecord("r1")}, hasMore: true, nextCursor: "page-2"},
			{rows: []interface{}{truncationRecord("r2")}, requestStatus: truncationIncomplete()},
		},
	})
	client := mustTestClient(t, "test-token", server.URL)

	records, err := client.QueryDatabaseAll(context.Background(), "ds-1")
	require.ErrorIs(t, err, errQueryResultTruncated, "the marker can appear before the last page")
	require.Len(t, records, 2)
	require.Equal(t, int32(2), atomic.LoadInt32(queryCalls), "pagination must stop at the incomplete page")
}

func TestQueryDatabaseAllCompleteResultUnchanged(t *testing.T) {
	cases := map[string]*truncationQueryPage{
		"no request_status": {
			rows: []interface{}{truncationRecord("r1"), truncationRecord("r2")},
		},
		"explicit complete": {
			rows:          []interface{}{truncationRecord("r1"), truncationRecord("r2")},
			requestStatus: map[string]interface{}{"type": "complete"},
		},
	}
	for name, page := range cases {
		t.Run(name, func(t *testing.T) {
			server, queryCalls := newTruncationServer(t, truncationFake{
				queryPages: []truncationQueryPage{*page},
			})
			client := mustTestClient(t, "test-token", server.URL)

			records, err := client.QueryDatabaseAll(context.Background(), "ds-1")
			require.NoError(t, err, "a complete result set must not raise a truncation error")
			require.Len(t, records, 2)
			require.Equal(t, int32(1), atomic.LoadInt32(queryCalls))
		})
	}
}

func TestFetchIncrementalTruncatedQuerySkipsDeletions(t *testing.T) {
	server, _ := newTruncationServer(t, truncationFake{
		searchResults: []interface{}{truncationDataSourceRow()},
		queryPages: []truncationQueryPage{{
			rows:          []interface{}{truncationRecord("r1"), truncationRecord("r2")},
			requestStatus: truncationIncomplete(),
		}},
	})
	config := makeNotionConfig(&Config{APIKey: "test-token"}, server.URL, []string{"ds-1"})

	items, next, err := NewConnector().FetchIncremental(context.Background(), config, truncationPrevCursor())
	require.NoError(t, err, "a truncated round is a partial success, not a failed sync")
	require.NotNil(t, next, "a truncated round still returns a cursor")
	require.Len(t, items, 1)
	require.Equal(t, "ds-1", items[0].ExternalID)
	require.False(t, items[0].IsDeleted)
	for _, item := range items {
		require.False(t, item.IsDeleted,
			"row %s was missing only because the query was capped and must not be deleted", item.ExternalID)
	}
}

func TestFetchIncrementalCompleteQueryStillDetectsDeletions(t *testing.T) {
	server, _ := newTruncationServer(t, truncationFake{
		searchResults: []interface{}{truncationDataSourceRow()},
		queryPages: []truncationQueryPage{{
			rows: []interface{}{truncationRecord("r1"), truncationRecord("r2")},
		}},
	})
	config := makeNotionConfig(&Config{APIKey: "test-token"}, server.URL, []string{"ds-1"})

	items, next, err := NewConnector().FetchIncremental(context.Background(), config, truncationPrevCursor())
	require.NoError(t, err)
	require.NotNil(t, next)

	deleted := map[string]bool{}
	for _, item := range items {
		if item.IsDeleted {
			deleted[item.ExternalID] = true
		}
	}
	require.Equal(t, map[string]bool{"r3": true, "r4": true}, deleted,
		"a complete result set must keep reporting genuinely absent rows as deleted")
}

func TestFetchPagePropagatesTruncationFromChildDatabase(t *testing.T) {
	server, _ := newTruncationServer(t, truncationFake{
		queryPages: []truncationQueryPage{{
			rows:          []interface{}{truncationRecord("r1")},
			requestStatus: truncationIncomplete(),
		}},
		blocks: map[string][]interface{}{
			"page-1": {map[string]interface{}{
				"id": "ds-1", "type": "child_database", "has_children": true,
				"child_database": map[string]interface{}{"title": "Embedded Database"},
			}},
		},
	})
	client := mustTestClient(t, "test-token", server.URL)
	page := &notionPage{
		ID:     "page-1",
		Object: "page",
		Parent: notionParent{Type: parentTypeWorkspace},
	}

	_, truncated := NewConnector().fetchPage(context.Background(), client, page, map[string]bool{})
	require.True(t, truncated, "a capped child database query must reach the incremental round")
}
