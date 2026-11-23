package notion

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func nestedTextBlock(id, kind, text string, children ...notionBlock) notionBlock {
	raw, _ := json.Marshal(map[string]any{"rich_text": []map[string]any{
		{"type": "text", "text": map[string]string{"content": text}},
	}, "checked": true})
	return notionBlock{ID: id, Type: kind, RawContent: raw, Children: children, HasChildren: len(children) > 0}
}

func TestNestedParagraphAndTodoContent(t *testing.T) {
	for _, kind := range []string{"paragraph", "to_do"} {
		t.Run(kind, func(t *testing.T) {
			leaf := nestedTextBlock("leaf", "paragraph", "Nested evidence")
			task := nestedTextBlock("task", "to_do", "Nested task", leaf)
			parent := nestedTextBlock("parent", kind, "Parent", task)
			body, _ := BlocksToMarkdown([]notionBlock{parent, nestedTextBlock("after", "paragraph", "After")})
			require.Equal(t, 1, strings.Count(body, "Nested evidence"))
			require.Equal(t, 1, strings.Count(body, "Nested task"))
			require.Less(t, strings.Index(body, "Parent"), strings.Index(body, "Nested task"))
			require.Less(t, strings.Index(body, "Nested evidence"), strings.Index(body, "After"))
			require.Contains(t, body, "- [x] Nested task")
		})
	}
}

func TestNotionSyncPreservesNestedContentAndAttachments(t *testing.T) {
	for _, kind := range []string{"paragraph", "to_do"} {
		for _, mode := range []string{"full", "incremental"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				allowNotionTestServer(t)
				var baseURL string
				var attachmentCalls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/v1/pages/page":
						_, _ = fmt.Fprint(w, retryPage("page", "workspace"))
					case "/v1/search":
						_, _ = fmt.Fprint(w, `{"results":[`+retryPage("page", "workspace")+`],"has_more":false}`)
					case "/v1/blocks/page/children":
						_, _ = fmt.Fprintf(w, `{"results":[{"id":"parent","type":%q,"has_children":true,`+
							`%q:{"rich_text":[{"type":"text","text":{"content":"Parent"}}],"checked":true}}]}`,
							kind, kind)
					case "/v1/blocks/parent/children":
						_, _ = fmt.Fprintf(w, `{"results":[`+
							`{"id":"text","type":"paragraph","paragraph":{"rich_text":[`+
							`{"type":"text","text":{"content":"Nested evidence"}}]}},`+
							`{"id":"file","type":"file","file":{"type":"external","name":"evidence.pdf",`+
							`"external":{"url":%q}}}]}`, baseURL+"/evidence.pdf")
					case "/evidence.pdf":
						attachmentCalls.Add(1)
						_, _ = fmt.Fprint(w, "%PDF-1.7 synthetic evidence")
					default:
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				baseURL = server.URL
				cfg := makeNotionConfig(&Config{APIKey: "test-token"}, baseURL, []string{"page"})
				connector := NewConnector()
				if mode == "full" {
					items, err := connector.FetchAll(context.Background(), cfg, cfg.ResourceIDs)
					require.NoError(t, err)
					require.Len(t, items, 2)
					require.Contains(t, string(items[0].Content), "Nested evidence")
					require.Equal(t, "evidence.pdf", items[1].FileName)
				} else {
					items, cursor, err := connector.FetchIncremental(context.Background(), cfg, nil)
					require.NoError(t, err)
					require.Len(t, items, 2)
					require.Contains(t, string(items[0].Content), "Nested evidence")
					require.Equal(t, "evidence.pdf", items[1].FileName)
					again, _, err := connector.FetchIncremental(context.Background(), cfg, cursor)
					require.NoError(t, err)
					require.Empty(t, again)
				}
				require.Equal(t, int32(1), attachmentCalls.Load())
			})
		}
	}
}

func TestTabParagraphIncludesBodyAndImage(t *testing.T) {
	image := notionBlock{ID: "image", Type: "image", RawContent: json.RawMessage(
		`{"type":"external","external":{"url":"https://example.com/chart.png"}}`)}
	label := nestedTextBlock("label", "paragraph", "Overview",
		nestedTextBlock("body", "paragraph", "Tab evidence"), image)
	tab := notionBlock{ID: "tab", Type: "tab", Children: []notionBlock{label}}
	body, attachments := BlocksToMarkdown([]notionBlock{tab})
	require.Contains(t, body, "Overview\n\nTab evidence")
	require.Contains(t, body, "https://example.com/chart.png")
	require.Len(t, attachments, 1)
	require.Equal(t, "image", attachments[0].Type)
}
