package notion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func databaseMembershipCursor(recordIDs ...string) *types.SyncCursor {
	editedAt := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	editTimes := map[string]time.Time{"ds-1": editedAt}
	for _, recordID := range recordIDs {
		editTimes[recordID] = editedAt
	}
	return buildCursorWithDatabaseRecords(editTimes, map[string][]string{
		"ds-1": append([]string(nil), recordIDs...),
	})
}

func TestFetchIncrementalRebuildsUnchangedDatabaseWhenMembershipChanges(t *testing.T) {
	server := newWindowQueryServer(t, windowQueryFake{
		searchResults: []interface{}{truncationDataSourceRow()},
		rows:          createdTimeRows("r1"),
	})
	config := makeNotionConfig(&Config{APIKey: "test-token"}, server.URL, []string{"ds-1"})

	items, next, err := NewConnector().FetchIncremental(
		context.Background(), config, databaseMembershipCursor("r1", "r2"),
	)
	require.NoError(t, err)
	require.NotNil(t, next)

	var databaseItem *types.FetchedItem
	for i := range items {
		if items[i].ExternalID == "ds-1" && !items[i].IsDeleted {
			databaseItem = &items[i]
		}
	}
	require.NotNil(t, databaseItem, "membership removal must rebuild the aggregate document")
	require.Contains(t, string(databaseItem.Content), "Row r1")
	require.NotContains(t, string(databaseItem.Content), "Row r2")

	var cursor notionCursor
	cursorBytes, marshalErr := json.Marshal(next.ConnectorCursor)
	require.NoError(t, marshalErr)
	require.NoError(t, json.Unmarshal(cursorBytes, &cursor))
	require.Equal(t, []string{"r1"}, cursor.DatabaseRecordIDs["ds-1"])
}

func TestFetchIncrementalDeletesAggregateForCompleteEmptyDatabase(t *testing.T) {
	server := newWindowQueryServer(t, windowQueryFake{
		searchResults: []interface{}{truncationDataSourceRow()},
	})
	config := makeNotionConfig(&Config{APIKey: "test-token"}, server.URL, []string{"ds-1"})

	items, next, err := NewConnector().FetchIncremental(
		context.Background(), config, databaseMembershipCursor("r1"),
	)
	require.NoError(t, err)
	require.NotNil(t, next)

	foundDatabaseDeletion := false
	for _, item := range items {
		if item.ExternalID == "ds-1" && item.IsDeleted {
			foundDatabaseDeletion = true
		}
	}
	require.True(t, foundDatabaseDeletion, "a successful empty query must remove the stale aggregate")
}

func TestFetchIncrementalDatabaseQueryFailureDoesNotAdvanceCursor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/search":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"object": "list", "results": []interface{}{truncationDataSourceRow()},
				"has_more": false, "next_cursor": nil,
			})
		case r.URL.Path == "/v1/data_sources/ds-1" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(windowDataSourceInfo())
		case r.URL.Path == "/v1/data_sources/ds-1/query":
			http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	config := makeNotionConfig(&Config{APIKey: "test-token"}, server.URL, []string{"ds-1"})

	items, next, err := NewConnector().FetchIncremental(
		context.Background(), config, databaseMembershipCursor("r1"),
	)
	require.Error(t, err)
	require.Nil(t, items)
	require.Nil(t, next, "a failed membership query must not acknowledge a new cursor")
}
