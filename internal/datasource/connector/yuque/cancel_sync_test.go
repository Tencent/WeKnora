package yuque

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestFetchIncrementalCancelDuringLastDetailDiscardsBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f := newFakeYuque()
	defer f.Close()
	f.handleJSON("/api/v2/repos/10/docs", http.StatusOK, v2DocListResponse{
		Data: []v2Doc{{ID: 7, Title: "Changed note", ContentUpdatedAt: "2026-11-13T08:00:00Z"}},
	})
	f.mux.HandleFunc("/api/v2/repos/docs/7", func(w http.ResponseWriter, _ *http.Request) {
		cancel()
		w.WriteHeader(http.StatusOK)
	})

	items, cursor, err := NewConnector().FetchIncremental(ctx, makeDSConfig(f, []string{"10"}), nil)
	if ctx.Err() == nil {
		t.Fatal("test server did not cancel the detail request")
	}
	if !errors.Is(err, context.Canceled) || len(items) != 0 || cursor != nil {
		t.Fatalf("canceled sync returned %d items, cursor=%v, err=%v", len(items), cursor, err)
	}
}
