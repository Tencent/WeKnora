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
		switch f.mode {
		case "database":
			_, _ = w.Write([]byte(`{"results":[` +
				retryDataSource("page") + `,` + retryRecord("child", "page") +
				`],"has_more":false}`))
		case "record":
			_, _ = w.Write([]byte(`{"results":[` + retryRecord("page", "ds") + `],"has_more":false}`))
		default:
			_, _ = w.Write([]byte(`{"results":[` + retryPage("page", "workspace") + `],"has_more":false}`))
		}
	case "/v1/pages/page":
		if f.mode == "database" {
			http.NotFound(w, r)
			return
		}
		if f.mode == "record" {
			_, _ = w.Write([]byte(retryRecord("page", "ds")))
			return
		}
		_, _ = w.Write([]byte(retryPage("page", "workspace")))
	case "/v1/data_sources/page":
		_, _ = w.Write([]byte(retryDataSource("page")))
	case "/v1/data_sources/ds":
		_, _ = w.Write([]byte(retryDataSource("ds")))
	case "/v1/data_sources/page/query":
		_, _ = w.Write([]byte(`{"results":[` + retryRecord("child", "page") + `],"has_more":false}`))
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

func retryDataSource(id string) string {
	return `{"id":"` + id + `","object":"data_source","last_edited_time":"` + retryPageTime +
		`","parent":{"type":"workspace"},"title":[{"plain_text":"Team Wiki"}]}`
}

func retryRecord(id, dataSourceID string) string {
	return `{"id":"` + id + `","object":"page","last_edited_time":"` + retryPageTime +
		`","parent":{"type":"data_source_id","data_source_id":"` + dataSourceID + `"},` +
		`"properties":{"Name":{"type":"title","title":[{"plain_text":"Row"}]}}}`
}

func (f *blockRetryFixture) pageBlocks(w http.ResponseWriter, r *http.Request) {
	switch f.mode {
	case "child":
		_, _ = w.Write([]byte(`{"results":[{"id":"child","type":"child_page",` +
			`"child_page":{"title":"Child"}}],"has_more":false}`))
	case "nested":
		_, _ = w.Write([]byte("{\"results\":[{\"id\":\"child\",\"type\":\"toggle\",\"has_children\":true," +
			"\"toggle\":{\"rich_text\":[{\"plain_text\":\"Parent\"}]}}],\"has_more\":false}"))
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
	modes := []string{"page", "server_error", "later_page", "child", "nested", "database", "record"}
	for _, mode := range modes {
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
	for _, mode := range []string{"child", "database", "record"} {
		t.Run(mode, func(t *testing.T) {
			_, config := newBlockRetryFixture(t, mode)
			config.ResourceIDs = []string{"healthy", "page"}
			items, err := NewConnector().FetchAll(context.Background(), config, config.ResourceIDs)
			require.Error(t, err)
			require.Nil(t, items, "discard earlier successful items when a later resource fails")
		})
	}
}

func TestNotionUnsharedSyncedBlockPreservesHealthyContent(t *testing.T) {
	allowNotionTestServer(t)
	blocksWithUnsharedSync := func(text string) string {
		return `{"results":[` +
			`{"id":"unshared-sync","type":"synced_block","has_children":true,` +
			`"synced_block":{"synced_from":{"type":"block_id","block_id":"private-orig"}}},` +
			`{"id":"body","type":"paragraph","paragraph":{"rich_text":[` +
			`{"type":"text","text":{"content":"` + text + `"},"plain_text":"` + text + `"}]}}` +
			`],"has_more":false}`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/search":
			_, _ = w.Write([]byte(`{"results":[` +
				retryPage("page", "workspace") + `,` +
				retryPage("healthy", "workspace") + `,` +
				retryDataSource("db-1") + `,` +
				retryRecord("rec-db", "db-1") + `,` +
				retryRecord("rec-direct", "ds") +
				`],"has_more":false}`))
		case "/v1/pages/page":
			_, _ = w.Write([]byte(retryPage("page", "workspace")))
		case "/v1/pages/healthy":
			_, _ = w.Write([]byte(retryPage("healthy", "workspace")))
		case "/v1/pages/rec-direct":
			_, _ = w.Write([]byte(retryRecord("rec-direct", "ds")))
		case "/v1/data_sources/db-1":
			_, _ = w.Write([]byte(retryDataSource("db-1")))
		case "/v1/data_sources/ds":
			_, _ = w.Write([]byte(retryDataSource("ds")))
		case "/v1/data_sources/db-1/query":
			_, _ = w.Write([]byte(`{"results":[` + retryRecord("rec-db", "db-1") + `],"has_more":false}`))
		case "/v1/blocks/page/children":
			_, _ = w.Write([]byte(blocksWithUnsharedSync("Accessible page content")))
		case "/v1/blocks/healthy/children":
			_, _ = w.Write([]byte(blocksWithUnsharedSync("Healthy sibling content")))
		case "/v1/blocks/rec-db/children":
			_, _ = w.Write([]byte(blocksWithUnsharedSync("Accessible database record body")))
		case "/v1/blocks/rec-direct/children":
			_, _ = w.Write([]byte(blocksWithUnsharedSync("Direct record body")))
		case "/v1/blocks/unshared-sync/children":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":404,"code":"object_not_found","message":"Could not find block."}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	resourceIDs := []string{"page", "healthy", "db-1", "rec-direct"}
	config := makeNotionConfig(&Config{APIKey: "test-token"}, server.URL, resourceIDs)
	connector := NewConnector()

	assertAllAccessibleBodies := func(t *testing.T, items []types.FetchedItem) {
		t.Helper()
		byID := make(map[string]string, len(items))
		for _, item := range items {
			byID[item.ExternalID] = string(item.Content)
		}
		require.Contains(t, byID["page"], "Accessible page content")
		require.Contains(t, byID["healthy"], "Healthy sibling content")
		require.Contains(t, byID["db-1"], "Accessible database record body")
		require.Contains(t, byID["rec-direct"], "Direct record body")
	}

	t.Run("full_sync", func(t *testing.T) {
		items, err := connector.FetchAll(context.Background(), config, resourceIDs)
		require.NoError(t, err)
		assertAllAccessibleBodies(t, items)
	})

	t.Run("incremental_sync", func(t *testing.T) {
		oldTime := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
		previous := buildCursor(map[string]time.Time{
			"page": oldTime, "healthy": oldTime, "db-1": oldTime, "rec-direct": oldTime,
		})
		items, next, err := connector.FetchIncremental(context.Background(), config, previous)
		require.NoError(t, err)
		require.NotNil(t, next)
		assertAllAccessibleBodies(t, items)

		again, _, err := connector.FetchIncremental(context.Background(), config, next)
		require.NoError(t, err)
		require.Empty(t, again)
	})
}

func TestNotionEmptyPageStillSucceeds(t *testing.T) {
	_, config := newBlockRetryFixture(t, "empty")
	previous := buildCursor(map[string]time.Time{"page": {}})
	items, next, err := NewConnector().FetchIncremental(context.Background(), config, previous)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "page", items[0].ExternalID)
	require.False(t, items[0].IsDeleted)
	require.Equal(t, "# "+defaultUntitledName+"\n", string(items[0].Content))
	require.NotNil(t, next)
}
