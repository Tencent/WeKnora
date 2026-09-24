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

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

const retryPageTime = "2026-09-24T10:00:00Z"

type blockRetryFixture struct {
	failed atomic.Bool
	calls  atomic.Int32
	mode   string
}

func (f *blockRetryFixture) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/v1/search":
		_, _ = w.Write([]byte(`{"results":[` + retryPage("page", "workspace") + `],"has_more":false}`))
	case "/v1/pages/page":
		_, _ = w.Write([]byte(retryPage("page", "workspace")))
	case "/v1/pages/healthy":
		_, _ = w.Write([]byte(retryPage("healthy", "workspace")))
	case "/v1/pages/child":
		_, _ = w.Write([]byte(retryPage("child", "page_id")))
	case "/v1/blocks/page/children":
		f.pageBlocks(w, r)
	case "/v1/blocks/child/children":
		f.content(w)
	case "/v1/blocks/healthy/children":
		_, _ = w.Write([]byte(`{"results":[{"id":"text","type":"paragraph","paragraph":{"rich_text":[` +
			`{"type":"text","text":{"content":"healthy content"},"plain_text":"healthy content"}]}}]}`))
	default:
		http.NotFound(w, r)
	}
}

func retryPage(id, parentType string) string {
	return `{"id":"` + id + `","object":"page","last_edited_time":"` + retryPageTime +
		`","parent":{"type":"` + parentType + `","page_id":"page"}}`
}

func (f *blockRetryFixture) pageBlocks(w http.ResponseWriter, r *http.Request) {
	switch f.mode {
	case "child":
		_, _ = w.Write([]byte(`{"results":[{"id":"child","type":"child_page",` +
			`"child_page":{"title":"Child"}}],"has_more":false}`))
	case "later_page":
		if r.URL.Query().Get("start_cursor") == "later" {
			f.content(w)
			return
		}
		_, _ = w.Write([]byte(`{"results":[],"has_more":true,"next_cursor":"later"}`))
	case "empty":
		_, _ = w.Write([]byte(`{"results":[],"has_more":false}`))
	default:
		f.content(w)
	}
}

func (f *blockRetryFixture) content(w http.ResponseWriter) {
	f.calls.Add(1)
	if f.failed.Load() {
		status := http.StatusUnauthorized
		if f.mode == "server_error" {
			status = http.StatusServiceUnavailable
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"code":"unauthorized","message":"test block request rejected"}`))
		return
	}
	_, _ = w.Write([]byte(`{"results":[{"id":"text","type":"paragraph","paragraph":{"rich_text":[` +
		`{"type":"text","text":{"content":"recovered content"},"plain_text":"recovered content"}` +
		`]}}],"has_more":false}`))
}

func newBlockRetryFixture(t *testing.T, mode string) (*blockRetryFixture, *types.DataSourceConfig) {
	t.Helper()
	allowNotionTestServer(t)
	fixture := &blockRetryFixture{mode: mode}
	fixture.failed.Store(true)
	server := httptest.NewServer(http.HandlerFunc(fixture.serve))
	t.Cleanup(server.Close)
	return fixture, makeNotionConfig(&Config{APIKey: "test-token"}, server.URL, []string{"page"})
}

func TestNotionBlockFailurePreservesRetry(t *testing.T) {
	for _, mode := range []string{"page", "server_error", "later_page", "child"} {
		t.Run(mode, func(t *testing.T) {
			fixture, config := newBlockRetryFixture(t, mode)
			oldTime := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
			previous := buildCursor(map[string]time.Time{"page": oldTime, "removed": oldTime})
			before, err := json.Marshal(previous)
			require.NoError(t, err)
			connector := NewConnector()
			items, next, err := connector.FetchIncremental(context.Background(), config, previous)
			require.ErrorContains(t, err, "get blocks")
			require.Nil(t, items, "failed traversal must not emit content or deletion events")
			require.Nil(t, next, "failed traversal must not acknowledge the new edit time")
			after, marshalErr := json.Marshal(previous)
			require.NoError(t, marshalErr)
			require.Equal(t, before, after)
			require.Positive(t, fixture.calls.Load())

			fixture.failed.Store(false)
			items, next, err = connector.FetchIncremental(context.Background(), config, previous)
			require.NoError(t, err)
			require.NotNil(t, next)
			var foundContent, foundDeletion bool
			for _, item := range items {
				foundContent = foundContent || strings.Contains(string(item.Content), "recovered content")
				foundDeletion = foundDeletion || (item.ExternalID == "removed" && item.IsDeleted)
			}
			require.True(t, foundContent, "recovered page must be fetched without another source edit")
			require.True(t, foundDeletion, "successful traversal must still detect actual deletions")
			calls := fixture.calls.Load()
			items, _, err = connector.FetchIncremental(context.Background(), config, next)
			require.NoError(t, err)
			require.Empty(t, items)
			require.Equal(t, calls, fixture.calls.Load(), "unchanged page must not be fetched again")
		})
	}
}

func TestNotionFirstSyncRejectsBlockFailure(t *testing.T) {
	fixture, config := newBlockRetryFixture(t, "page")
	connector := NewConnector()
	items, next, err := connector.FetchIncremental(context.Background(), config, nil)
	require.Error(t, err)
	require.Nil(t, items)
	require.Nil(t, next, "first sync must not substitute time.Now for a failed page")
	fixture.failed.Store(false)
	items, next, err = connector.FetchIncremental(context.Background(), config, nil)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.NotNil(t, next)
}

func TestNotionFullSyncRejectsBlockFailure(t *testing.T) {
	_, config := newBlockRetryFixture(t, "child")
	config.ResourceIDs = []string{"healthy", "page"}
	items, err := NewConnector().FetchAll(context.Background(), config, config.ResourceIDs)
	require.Error(t, err)
	require.Nil(t, items, "discard earlier successful items when a later page fails")
}

func TestNotionEmptyPageStillSucceeds(t *testing.T) {
	_, config := newBlockRetryFixture(t, "empty")
	previous := buildCursor(map[string]time.Time{"page": {}})
	items, next, err := NewConnector().FetchIncremental(context.Background(), config, previous)
	require.NoError(t, err)
	require.Empty(t, items)
	require.NotNil(t, next)
}
