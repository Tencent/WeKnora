package rss

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type articleRetryFeed struct {
	server       *httptest.Server
	status       atomic.Int32
	articleReads atomic.Int32
	healthyReads atomic.Int32
	summary      string
	includeLink  bool
}

func newArticleRetryFeed(t *testing.T) *articleRetryFeed {
	t.Helper()
	f := &articleRetryFeed{summary: "Temporary summary", includeLink: true}
	f.status.Store(http.StatusServiceUnavailable)
	f.server = httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(f.server.Close)
	return f
}

func (f *articleRetryFeed) serveHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/feed.xml":
		link := ""
		if f.includeLink {
			link = "<link>http://" + r.Host + "/article</link>"
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = fmt.Fprintf(w, `<rss version="2.0"><channel><title>Retry feed</title>
<item><guid>retry</guid><title>Retry article</title>%s<description><![CDATA[%s]]></description></item>
<item><guid>healthy</guid><title>Healthy article</title><link>http://%s/healthy</link></item>
</channel></rss>`, link, f.summary, r.Host)
	case "/article":
		f.articleReads.Add(1)
		if status := int(f.status.Load()); status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		writeRetryArticle(w)
	case "/healthy":
		f.healthyReads.Add(1)
		writeRetryArticle(w)
	default:
		http.NotFound(w, r)
	}
}

func writeRetryArticle(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, "<html><head><title>Article</title></head><body><article>%s</article></body></html>",
		longArticleBody)
}

func (f *articleRetryFeed) sync(t *testing.T, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor) {
	t.Helper()
	url := f.server.URL + "/feed.xml"
	cfg := makeConfig(url, "")
	cfg.ResourceIDs = []string{url}
	items, next, err := NewConnector().FetchIncremental(context.Background(), cfg, cursor)
	require.NoError(t, err)
	require.NotNil(t, next)
	return items, next
}

func TestIncrementalRetriesArticleAfterFallback(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			f := newArticleRetryFeed(t)
			f.status.Store(int32(status))
			items, cursor := f.sync(t, nil)
			require.Len(t, items, 2)
			require.Equal(t, f.summary, string(items[0].Content))
			items, cursor = f.sync(t, cursor)
			require.Empty(t, items, "unchanged fallback must not be ingested again")
			require.EqualValues(t, 2, f.articleReads.Load(), "failed article must be retried on the next sync")
			require.EqualValues(t, 1, f.healthyReads.Load(), "successful sibling should stay cached")
			f.status.Store(http.StatusOK)
			items, cursor = f.sync(t, cursor)
			require.Len(t, items, 1)
			require.Contains(t, string(items[0].Content), "reasonably long article")
			items, _ = f.sync(t, cursor)
			require.Empty(t, items)
			require.EqualValues(t, 3, f.articleReads.Load(), "recovered article should now stay cached")
			require.EqualValues(t, 1, f.healthyReads.Load())
		})
	}
}

func TestIncrementalRetriesArticleWithEmptyFallback(t *testing.T) {
	f := newArticleRetryFeed(t)
	f.summary = ""
	_, cursor := f.sync(t, nil)
	f.status.Store(http.StatusOK)
	items, _ := f.sync(t, cursor)
	require.Len(t, items, 1)
	require.NotEmpty(t, items[0].Content)
	require.EqualValues(t, 2, f.articleReads.Load())
}

func TestIncrementalCachesRecoveryWithIdenticalContent(t *testing.T) {
	f := newArticleRetryFeed(t)
	f.summary = longArticleBody
	_, cursor := f.sync(t, nil)
	f.status.Store(http.StatusOK)
	items, cursor := f.sync(t, cursor)
	require.Empty(t, items, "identical recovered content does not need ingestion")
	require.EqualValues(t, 2, f.articleReads.Load(), "the article must have been fetched successfully")
	items, _ = f.sync(t, cursor)
	require.Empty(t, items)
	require.EqualValues(t, 2, f.articleReads.Load(), "success should cache the feed signal despite equal content")
}

func TestIncrementalCachesEntryWithoutArticleLink(t *testing.T) {
	f := newArticleRetryFeed(t)
	f.includeLink = false
	items, cursor := f.sync(t, nil)
	require.Len(t, items, 2)
	items, _ = f.sync(t, cursor)
	require.Empty(t, items)
	require.Zero(t, f.articleReads.Load())
	require.EqualValues(t, 1, f.healthyReads.Load())
}
