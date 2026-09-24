package yuque

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

const (
	retryOldTime = "2026-04-20T10:00:00Z"
	retryNewTime = "2026-04-20T11:00:00Z"
)

type retryFixture struct {
	cfg       *types.DataSourceConfig
	recovered atomic.Bool
	removed   atomic.Bool
	calls     atomic.Int32
}

func newRetryFixture(t *testing.T) *retryFixture {
	t.Helper()
	fake := newFakeYuque()
	t.Cleanup(fake.Close)
	f := &retryFixture{cfg: makeDSConfig(fake, []string{"7"})}
	fake.mux.HandleFunc("/api/v2/repos/7/docs", func(w http.ResponseWriter, _ *http.Request) {
		docs := []v2Doc{{ID: 102, Type: "Doc", Status: "1", ContentUpdatedAt: retryNewTime}}
		if !f.removed.Load() {
			docs = append(docs, v2Doc{ID: 101, Type: "Doc", Status: "1", ContentUpdatedAt: retryNewTime})
		}
		_ = json.NewEncoder(w).Encode(v2DocListResponse{Data: docs})
	})
	fake.handleJSON("/api/v2/repos/docs/102", http.StatusOK,
		v2DocDetailResponse{Data: v2DocDetail{ID: 102, Body: "healthy", Format: "markdown"}})
	fake.mux.HandleFunc("/api/v2/repos/docs/101", func(w http.ResponseWriter, _ *http.Request) {
		f.calls.Add(1)
		if !f.recovered.Load() {
			http.Error(w, "temporary outage", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(v2DocDetailResponse{
			Data: v2DocDetail{ID: 101, Body: "recovered", Format: "markdown"},
		})
	})
	return f
}

func retryPreviousCursor() *types.SyncCursor {
	return &types.SyncCursor{ConnectorCursor: map[string]interface{}{
		"book_doc_times": map[string]map[string]string{"7": {"101": retryOldTime}},
	}}
}

func (f *retryFixture) sync(t *testing.T, previous *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor) {
	t.Helper()
	items, next, err := NewConnector().FetchIncremental(context.Background(), f.cfg, previous)
	require.NoError(t, err)
	require.NotNil(t, next)
	return items, next
}

func retryBookTimes(t *testing.T, cursor *types.SyncCursor) map[string]string {
	t.Helper()
	data, err := json.Marshal(cursor.ConnectorCursor)
	require.NoError(t, err)
	var decoded yuqueCursor
	require.NoError(t, json.Unmarshal(data, &decoded))
	return decoded.BookDocTimes["7"]
}

func TestFetchIncrementalRetriesFailedDetail(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "new document"
		if existing {
			name = "existing document"
		}
		t.Run(name, func(t *testing.T) { testRetryRecovery(t, existing) })
	}
}

func testRetryRecovery(t *testing.T, existing bool) {
	f := newRetryFixture(t)
	var previous *types.SyncCursor
	if existing {
		previous = retryPreviousCursor()
	}
	items, failed := f.sync(t, previous)
	require.Len(t, items, 2, "one success and one failure must keep the batch partial")
	require.Equal(t, "healthy", string(items[0].Content))
	require.NotEmpty(t, items[1].Metadata["error"])
	require.False(t, items[1].IsDeleted)
	times := retryBookTimes(t, failed)
	require.Equal(t, retryNewTime, times["102"])
	if existing {
		require.Equal(t, retryOldTime, times["101"])
	} else {
		require.NotContains(t, times, "101")
	}
	f.recovered.Store(true)
	before := f.calls.Load()
	items, recovered := f.sync(t, failed)
	require.Len(t, items, 1, "only the failed document should be retried")
	require.Equal(t, "101", items[0].ExternalID)
	require.Equal(t, "recovered", string(items[0].Content))
	require.Equal(t, before+1, f.calls.Load())
	require.Equal(t, retryNewTime, retryBookTimes(t, recovered)["101"])
	items, unchanged := f.sync(t, recovered)
	require.Empty(t, items, "successful recovery must not repeat on the next sync")
	require.Equal(t, retryBookTimes(t, recovered), retryBookTimes(t, unchanged))
}

func TestFetchIncrementalDetectsDeletionAfterDetailFailure(t *testing.T) {
	f := newRetryFixture(t)
	_, failed := f.sync(t, retryPreviousCursor())
	f.removed.Store(true)
	items, next := f.sync(t, failed)
	require.Len(t, items, 1)
	require.Equal(t, "101", items[0].ExternalID)
	require.True(t, items[0].IsDeleted, "a previously synced document must remain tracked for deletion")
	require.NotContains(t, retryBookTimes(t, next), "101")
	require.Equal(t, retryNewTime, retryBookTimes(t, next)["102"])
}
