package notion

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func emptyPageConfig(t *testing.T, blocks, title string) *types.DataSourceConfig {
	t.Helper()
	allowNotionTestServer(t)
	titleJSON, err := json.Marshal(title)
	require.NoError(t, err)
	page := `{"id":"page","object":"page","url":"https://notion.so/page",` +
		`"last_edited_time":"2026-09-26T10:00:00Z","parent":{"type":"workspace"},` +
		`"properties":{"title":{"type":"title","title":[{"plain_text":` + string(titleJSON) + `}]}}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/pages/page":
			_, _ = fmt.Fprint(w, page)
		case "/v1/search":
			_, _ = fmt.Fprint(w, `{"results":[`+page+`],"has_more":false}`)
		case "/v1/blocks/page/children":
			if blocks == "later_failure" && r.URL.Query().Get("start_cursor") == "" {
				_, _ = fmt.Fprint(w, `{"results":[],"has_more":true,"next_cursor":"later"}`)
				return
			}
			if blocks == "unauthorized" || blocks == "later_failure" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = fmt.Fprint(w, `{"results":`+blocks+`,"has_more":false}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return makeNotionConfig(&Config{APIKey: "test-token"}, server.URL, []string{"page"})
}

func TestClearedPageEmitsReplacement(t *testing.T) {
	config := emptyPageConfig(t, `[]`, "Retained title")
	previous := buildCursor(map[string]time.Time{"page": time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)})
	connector := NewConnector()
	items, next, err := connector.FetchIncremental(context.Background(), config, previous)
	require.NoError(t, err)
	require.Len(t, items, 1, "an empty source page must replace previously indexed content")
	require.Equal(t, "page", items[0].ExternalID)
	require.Equal(t, "# Retained title\n", string(items[0].Content))
	require.Equal(t, "Retained title.md", items[0].FileName)
	require.False(t, items[0].IsDeleted, "the source page still exists")
	require.Equal(t, "page", items[0].Metadata["object_type"])
	items, _, err = connector.FetchIncremental(context.Background(), config, next)
	require.NoError(t, err)
	require.Empty(t, items, "unchanged empty pages must not be ingested repeatedly")
}

func TestFullSyncIncludesEmptyPageTitle(t *testing.T) {
	config := emptyPageConfig(t, `[]`, "Retained title")
	items, err := NewConnector().FetchAll(context.Background(), config, config.ResourceIDs)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "# Retained title\n", string(items[0].Content))
}

func TestEmptyPageFallbackDoesNotMaskUnreadableContent(t *testing.T) {
	for _, blocks := range []string{
		`[{"id":"unknown","type":"unsupported","unsupported":{}}]`, "unauthorized", "later_failure",
	} {
		t.Run(blocks, func(t *testing.T) {
			config := emptyPageConfig(t, blocks, "Retained title")
			items, _ := NewConnector().FetchAll(context.Background(), config, config.ResourceIDs)
			require.Empty(t, items, "unreadable content must not be replaced with a title-only document")
		})
	}
}

func TestEmptyPageUsesUntitledFallback(t *testing.T) {
	for _, title := range []string{"", "   "} {
		config := emptyPageConfig(t, `[]`, title)
		items, err := NewConnector().FetchAll(context.Background(), config, config.ResourceIDs)
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.Equal(t, "# "+defaultUntitledName+"\n", string(items[0].Content))
	}
}

func TestAttachmentPageKeepsRenderedContent(t *testing.T) {
	blocks := []map[string]interface{}{{"id": "file", "type": "file", "file": map[string]interface{}{
		"type": "external", "name": "reference.pdf", "external": map[string]string{"url": ""},
	}}}
	data, err := json.Marshal(blocks)
	require.NoError(t, err)
	config := emptyPageConfig(t, string(data), "Retained title")
	items, err := NewConnector().FetchAll(context.Background(), config, config.ResourceIDs)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Contains(t, string(items[0].Content), "[reference.pdf]")
	require.NotContains(t, string(items[0].Content), "# Retained title")
}
