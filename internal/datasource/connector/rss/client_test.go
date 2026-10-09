package rss

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/mmcdole/gofeed"
)

func TestFetchResponseSizeLimit(t *testing.T) {
	const limit = 16
	for _, chunked := range []bool{false, true} {
		for _, size := range []int{limit - 1, limit, limit + 1} {
			name := strconv.Itoa(size) + "/chunked=" + strconv.FormatBool(chunked)
			t.Run(name, func(t *testing.T) {
				body := strings.Repeat("x", size)
				server := serveResponseBody(t, body, chunked)
				got, err := newClient(nil).fetch(context.Background(), server.URL, limit, false)
				if size > limit {
					if err == nil {
						t.Fatalf("expected oversized response error, got %d bytes and no error", len(got))
					}
					if got != nil {
						t.Fatal("oversized response must not return a truncated body")
					}
					return
				}
				if err != nil {
					t.Fatalf("fetch within limit: %v", err)
				}
				if string(got) != body {
					t.Fatalf("expected %q, got %q", body, got)
				}
			})
		}
	}
}

func TestResolveItemOversizedArticleUsesFeedContent(t *testing.T) {
	const feedContent = "Complete feed fallback content"
	body := "<html><body><article>" + longArticleBody +
		strings.Repeat(" ", maxArticleSize) + "<p>Final paragraph</p></article></body></html>"
	server := serveResponseBody(t, body, true)
	item := &gofeed.Item{Title: "Article", Link: server.URL}
	resolved := NewConnector().resolveItem(context.Background(), newClient(nil),
		&gofeed.Feed{Title: "Feed"}, item, server.URL+"/feed", "item-1", feedContent)
	if string(resolved.item.Content) != feedContent {
		t.Fatalf("expected feed fallback %q, got %q", feedContent, resolved.item.Content)
	}
}

func serveResponseBody(t *testing.T, body string, chunked bool) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if chunked {
			w.(http.Flusher).Flush()
		} else {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}
