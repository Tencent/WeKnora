package rss

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/renderer/html"
	nethtml "golang.org/x/net/html"
)

func renderedFeedText(t *testing.T, markdown []byte) string {
	t.Helper()
	var rendered bytes.Buffer
	require.NoError(t, goldmark.New(goldmark.WithRendererOptions(html.WithUnsafe())).Convert(markdown, &rendered))
	doc, err := nethtml.Parse(&rendered)
	require.NoError(t, err)
	var text strings.Builder
	var walk func(*nethtml.Node)
	walk = func(n *nethtml.Node) {
		if n.Type == nethtml.TextNode {
			text.WriteString(n.Data)
		}
		if n.Type == nethtml.ElementNode && n.Data == "br" {
			text.WriteByte('\n')
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return strings.TrimSpace(text.String())
}

func TestJSONFeedContentFormats(t *testing.T) {
	const multiline = "First <tag>line</tag>.\nSecond &lt;line&gt;."
	const richHTML = "<p>Use <strong>bold</strong> &amp; 中文.</p>"
	const plain = "Use <widget>value</widget>, **literal stars**, &lt;literal&gt; & 中文."
	for _, tc := range []struct {
		name    string
		item    map[string]string
		want    string
		article string
	}{
		{"plain text", map[string]string{"content_text": plain}, plain, ""},
		{"multiline text", map[string]string{"content_text": multiline}, multiline, ""},
		{"article failure fallback", map[string]string{"content_text": plain}, plain, "failed"},
		{"full article preferred", map[string]string{"content_text": plain}, "", "success"},
		{"summary fallback", map[string]string{"content_text": "", "summary": plain}, plain, ""},
		{"HTML preferred", map[string]string{"content_html": richHTML, "content_text": plain}, "Use bold & 中文.", ""},
		{"HTML only", map[string]string{"content_html": richHTML}, "Use bold & 中文.", ""},
		{"empty", map[string]string{"content_text": ""}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.item["id"] = "entry-1"
			tc.item["title"] = "Example"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/article" {
					if tc.article == "failed" {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					writeRetryArticle(w)
					return
				}
				w.Header().Set("Content-Type", "application/feed+json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"version": "https://jsonfeed.org/version/1.1",
					"title":   "Examples", "items": []map[string]string{tc.item},
				})
			}))
			defer server.Close()
			if tc.article != "" {
				tc.item["url"] = server.URL + "/article"
			}
			items, err := NewConnector().FetchAll(context.Background(), makeConfig(server.URL, ""), nil)
			require.NoError(t, err)
			require.Len(t, items, 1)
			require.Equal(t, "text/markdown", items[0].ContentType)
			if tc.article == "success" {
				require.Contains(t, renderedFeedText(t, items[0].Content),
					"first paragraph of a reasonably long article")
				require.NotContains(t, string(items[0].Content), "widget")
			} else {
				require.Equal(t, tc.want, renderedFeedText(t, items[0].Content), "Markdown: %s", items[0].Content)
			}
			if strings.HasPrefix(tc.name, "HTML") {
				require.Contains(t, string(items[0].Content), "**bold**")
			}
		})
	}
}

func TestJSONFeedPlainTextIncremental(t *testing.T) {
	var body atomic.Value
	body.Store("Use <widget>first</widget> &lt;literal&gt;.")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/feed+json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"version": "https://jsonfeed.org/version/1.1", "title": "Examples",
			"items": []map[string]string{{"id": "stable-id", "title": "Example", "content_text": body.Load().(string)}},
		})
	}))
	defer server.Close()
	var cursor *types.SyncCursor
	var externalID string
	for _, text := range []string{body.Load().(string), "Use <other>second</other> &amp; literally."} {
		body.Store(text)
		items, next, err := NewConnector().FetchIncremental(context.Background(), makeConfig(server.URL, ""), cursor)
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.Equal(t, text, renderedFeedText(t, items[0].Content))
		if externalID != "" {
			require.Equal(t, externalID, items[0].ExternalID)
		}
		externalID = items[0].ExternalID
		encoded, err := json.Marshal(next)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(encoded, &cursor))
		items, cursor, err = NewConnector().FetchIncremental(context.Background(), makeConfig(server.URL, ""), cursor)
		require.NoError(t, err)
		require.Empty(t, items, "unchanged plain text should not be imported again")
	}
}
