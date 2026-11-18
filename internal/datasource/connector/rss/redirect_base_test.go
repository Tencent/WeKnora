package rss

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mmcdole/gofeed"
	"github.com/stretchr/testify/require"
)

func TestRedirectedArticleResolvesResourcesAgainstFinalURL(t *testing.T) {
	var root string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/short":
			http.Redirect(w, r, "/archive/2026/story.html", http.StatusFound)
		case "/archive/2026/story.html":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><head><title>Article</title></head><body><article><h1>Article</h1>`+
				longArticleBody+`<p>Read the <a href="appendix.html">supporting evidence</a> for this article.</p>`+
				`<img src="images/chart.png" alt="Evidence chart"></article></body></html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	root = server.URL
	for _, path := range []string{"/short", "/archive/2026/story.html"} {
		t.Run(path, func(t *testing.T) {
			item := &gofeed.Item{Title: "Article", Link: root + path}
			resolved := NewConnector().resolveItem(context.Background(), newClient(nil),
				&gofeed.Feed{Title: "Feed"}, item, root+"/feed", "article", "Summary")
			require.False(t, resolved.articleFailed)
			body := string(resolved.item.Content)
			require.Contains(t, body, root+"/archive/2026/appendix.html")
			require.Contains(t, body, root+"/archive/2026/images/chart.png")
			require.False(t, strings.Contains(body, root+"/images/chart.png"))
			require.Equal(t, root+path, resolved.item.URL, "feed identity and source URL stay unchanged")
		})
	}
}

func TestFeedSyncKeepsRedirectedResourceURLs(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<html><body><article>`+longArticleBody+
			`<p>See <a href="reference.html">reference evidence</a>.</p>`+
			`<img src="chart.png" alt="Chart"></article></body></html>`)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/short" {
			http.Redirect(w, r, target.URL+"/content/article", http.StatusMovedPermanently)
			return
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = fmt.Fprintf(w, `<rss version="2.0"><channel><title>Feed</title><link>http://%s</link>`+
			`<description>Feed</description><item><title>Article</title><guid>article</guid>`+
			`<link>http://%s/short</link><description>Summary</description></item></channel></rss>`, r.Host, r.Host)
	}))
	defer source.Close()
	cfg := makeConfig(source.URL+"/feed", "")
	connector := NewConnector()
	all, err := connector.FetchAll(context.Background(), cfg, nil)
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.Contains(t, string(all[0].Content), target.URL+"/content/chart.png")
	require.Contains(t, string(all[0].Content), target.URL+"/content/reference.html")
	items, cursor, err := connector.FetchIncremental(context.Background(), cfg, nil)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, all[0].Content, items[0].Content)
	again, _, err := connector.FetchIncremental(context.Background(), cfg, cursor)
	require.NoError(t, err)
	require.Empty(t, again)
}
