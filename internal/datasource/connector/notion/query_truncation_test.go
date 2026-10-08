package notion

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// A Notion data source query stops at 10,000 results and reports has_more=false
// together with request_status.type="incomplete". A capped query is no longer the
// end of the read: the client continues in created_time windows (see
// query_windows_test.go). These tests pin down what a window that cannot advance
// does instead — stop, stay visible, and never drive deletions.
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

func TestQueryDatabaseAllStopsWhenWindowCannotAdvance(t *testing.T) {
	logs := captureNotionLogs(t)
	// More rows than one query returns share a single created_time, so the second
	// window starts where the first one did and would return the same rows again.
	server := newWindowQueryServer(t, windowQueryFake{
		rows: []windowTestRow{
			{id: "r1", createdTime: "2026-01-01T10:00:00Z"},
			{id: "r2", createdTime: "2026-01-01T10:00:00Z"},
		},
		queryLimit: 1,
	})
	client := mustTestClient(t, "test-token", server.URL)

	records, err := client.QueryDatabaseAll(context.Background(), "ds-1")
	require.ErrorIs(t, err, errQueryResultTruncated, "an unwalkable window must still surface the truncation")
	require.ErrorIs(t, err, errWindowNotAdvancing)
	require.Contains(t, err.Error(), truncationReason)
	require.Equal(t, []string{"r1"}, recordIDs(records), "rows already read stay available to the caller")
	require.Equal(t, int32(2), server.queryCalls(),
		"the walk must stop as soon as the window stops advancing instead of repeating the same query")
	require.Contains(t, logs.String(), "does not advance", "the stop must be explained in a warning")
}

func TestQueryDatabaseAllStopsWhenRowsCarryNoCreatedTime(t *testing.T) {
	logs := captureNotionLogs(t)
	server := newWindowQueryServer(t, windowQueryFake{
		rows:       []windowTestRow{{id: "r1"}, {id: "r2"}},
		queryLimit: 1,
	})
	client := mustTestClient(t, "test-token", server.URL)

	records, err := client.QueryDatabaseAll(context.Background(), "ds-1")
	require.ErrorIs(t, err, errQueryResultTruncated)
	require.ErrorIs(t, err, errWindowNotAdvancing)
	require.Equal(t, []string{"r1"}, recordIDs(records))
	require.Equal(t, int32(1), server.queryCalls(),
		"a window without any created_time cannot start another one")
	require.Contains(t, logs.String(), "no row carrying created_time")
}

func TestQueryDatabaseAllStopsOnEmptyIncompleteWindow(t *testing.T) {
	logs := captureNotionLogs(t)
	server := newWindowQueryServer(t, windowQueryFake{alwaysIncomplete: true})
	client := mustTestClient(t, "test-token", server.URL)

	records, err := client.QueryDatabaseAll(context.Background(), "ds-1")
	require.ErrorIs(t, err, errQueryResultTruncated)
	require.ErrorIs(t, err, errWindowNotAdvancing)
	require.Empty(t, recordIDs(records))
	require.Equal(t, int32(1), server.queryCalls(), "an empty incomplete window must not be re-queried")
	require.Contains(t, logs.String(), "no row carrying created_time")
}

func TestFetchIncrementalTruncatedQuerySkipsDeletions(t *testing.T) {
	server := newWindowQueryServer(t, windowQueryFake{
		searchResults: []interface{}{truncationDataSourceRow()},
		rows: []windowTestRow{
			{id: "r1", createdTime: "2026-01-01T10:00:00Z"},
			{id: "r2", createdTime: "2026-01-01T10:00:00Z"},
		},
		queryLimit: 1,
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
	server := newWindowQueryServer(t, windowQueryFake{
		searchResults: []interface{}{truncationDataSourceRow()},
		rows:          createdTimeRows("r1", "r2"),
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

func TestFetchIncrementalWindowedQueryKeepsDeletionDetection(t *testing.T) {
	// The data source holds more rows than one query returns, but every window
	// advances, so the round ends complete: r4 is genuinely gone and must still be
	// reported as deleted. This guards against "never delete anything as soon as
	// windowing is involved".
	server := newWindowQueryServer(t, windowQueryFake{
		searchResults: []interface{}{truncationDataSourceRow()},
		rows:          createdTimeRows("r1", "r2", "r3"),
		queryLimit:    2,
		pageSize:      2,
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
	require.Equal(t, map[string]bool{"r4": true}, deleted,
		"rows read across windows prove the rows that are absent at source are deleted")
}

func TestFetchPagePropagatesTruncationFromChildDatabase(t *testing.T) {
	server := newWindowQueryServer(t, windowQueryFake{
		rows: []windowTestRow{
			{id: "r1", createdTime: "2026-01-01T10:00:00Z"},
			{id: "r2", createdTime: "2026-01-01T10:00:00Z"},
		},
		queryLimit: 1,
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
