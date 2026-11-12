package rss

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFetchIncrementalDoesNotCheckpointCanceledArticle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	articleCanceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feed.xml":
			_, _ = fmt.Fprintf(w, `<rss version="2.0"><channel><title>Example</title>
<item><guid>1</guid><title>Example article</title><link>http://%s/article</link>
<description>Summary</description></item>
</channel></rss>`, r.Host)
		case "/article":
			cancel()
			<-r.Context().Done()
			close(articleCanceled)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	items, cursor, err := NewConnector().FetchIncremental(ctx, makeConfig(server.URL+"/feed.xml", ""), nil)
	select {
	case <-articleCanceled:
	case <-time.After(time.Second):
		t.Fatal("article request did not reach the server and observe cancellation")
	}
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, items)
	require.Nil(t, cursor)
}
