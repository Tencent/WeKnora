package notion

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A data-source query that reports another page but supplies no continuation
// cursor must fail pagination: the rows read so far are a prefix of the
// result, and an incremental sync that treated them as the complete set
// would report previously known rows as deleted (#5604).
func TestQueryDataSourceWindowRejectsHasMoreWithoutCursor(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writeListPage(w, `[{`+
			`"id":"row-1",`+
			`"created_time":"2026-11-01T00:00:00.000Z"}`+
			`]`, true, "")
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, _, _, err := paginationTestClient(t, server).queryDataSourceWindow(
		ctx, "/v1/data_sources/ds-1/query", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "has_more=true without a next_cursor")
	require.Equal(t, int64(1), requests.Load(), "the loop must stop after the anomalous page; err=%v", err)
}

// The same response reached through queryDataSourceAll must surface the
// pagination error rather than returning a truncated walk that callers
// read as a complete result (#5604).
func TestQueryDataSourceAllPropagatesMissingCursorError(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writeListPage(w, "[]", true, "")
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := paginationTestClient(t, server).queryDataSourceAll(ctx, "/v1/data_sources/ds-1/query")
	require.Error(t, err)
	require.Contains(t, err.Error(), "has_more=true without a next_cursor")
}
