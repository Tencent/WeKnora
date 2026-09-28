package rss

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

type identityFeed struct {
	server  *httptest.Server
	xml     atomic.Value
	fetches atomic.Int32
}

func newIdentityFeed(t *testing.T) *identityFeed {
	t.Helper()
	f := &identityFeed{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/feed.xml" {
			w.Header().Set("Content-Type", "application/rss+xml")
			_, _ = fmt.Fprint(w, f.xml.Load().(string))
			return
		}
		f.fetches.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, "<html><head><title>Stable article</title></head>"+
			"<body><article>%s</article></body></html>", longArticleBody)
	}))
	t.Cleanup(f.server.Close)
	f.publish("Original title", "/original", "Mon, 02 Jan 2006 15:04:05 GMT")
	return f
}

func (f *identityFeed) publish(title, path, date string) {
	f.xml.Store(fmt.Sprintf(`<rss version="2.0"><channel><title>Feed</title>
<link>%s</link><description>Test</description>
<item><guid isPermaLink="false">stable-guid</guid><title>%s</title><link>%s%s</link>
<pubDate>%s</pubDate><description>Stable summary</description></item></channel></rss>`,
		f.server.URL, title, f.server.URL, path, date))
}

func (f *identityFeed) sync(t *testing.T, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor) {
	t.Helper()
	config := makeConfig(f.server.URL+"/feed.xml", "")
	items, next, err := NewConnector().FetchIncremental(context.Background(), config, cursor)
	if err != nil {
		t.Fatalf("FetchIncremental: %v", err)
	}
	return items, next
}

func TestIncrementalItemIdentityChanges(t *testing.T) {
	for _, tt := range []struct{ name, title, path string }{
		{"title only", "Corrected title", "/original"},
		{"link only", "Original title", "/moved"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newIdentityFeed(t)
			original, cursor := f.sync(t, nil)
			if len(original) != 1 {
				t.Fatalf("expected 1 initial item, got %d", len(original))
			}
			f.publish(tt.title, tt.path, "Mon, 02 Jan 2006 15:04:05 GMT")
			updated, next := f.sync(t, cursor)
			if len(updated) != 1 {
				t.Fatalf("expected 1 metadata update with unchanged body, got %d", len(updated))
			}
			if updated[0].ExternalID != original[0].ExternalID ||
				string(updated[0].Content) != string(original[0].Content) {
				t.Fatal("the stable GUID and body must be preserved")
			}
			if updated[0].Title != tt.title || updated[0].FileName != tt.title+".md" ||
				updated[0].URL != f.server.URL+tt.path {
				t.Fatalf("incorrect updated identity: %+v", updated[0])
			}
			if updated[0].Metadata["link"] != updated[0].URL {
				t.Fatal("persisted source link must match the updated URL")
			}
			assertIdentityUnchanged(t, f, next)
		})
	}
}

func assertIdentityUnchanged(t *testing.T, f *identityFeed, cursor *types.SyncCursor) {
	t.Helper()
	before := f.fetches.Load()
	items, _ := f.sync(t, cursor)
	if len(items) != 0 || f.fetches.Load() != before {
		t.Fatal("unchanged sync must not emit items or fetch the article")
	}
}

func TestIncrementalItemTimestampOnly(t *testing.T) {
	f := newIdentityFeed(t)
	_, cursor := f.sync(t, nil)
	f.publish("Original title", "/original", "Tue, 03 Jan 2006 15:04:05 GMT")
	items, next := f.sync(t, cursor)
	if len(items) != 0 {
		t.Fatalf("expected no update for timestamp-only changes, got %d", len(items))
	}
	if f.fetches.Load() != 2 {
		t.Fatal("changed feed signal must still resolve the article")
	}
	assertIdentityUnchanged(t, f, next)
}

func TestIncrementalItemLegacyCursor(t *testing.T) {
	f := newIdentityFeed(t)
	items, cursor := f.sync(t, nil)
	if len(items) != 1 {
		t.Fatalf("expected 1 initial item, got %d", len(items))
	}
	// Old cursors cannot prove that the stored title/link matches this feed,
	// even when the feed signal was acknowledged by an earlier sync.
	sum := sha256.Sum256(items[0].Content)
	cursor.ConnectorCursor["feed_items"] = map[string]map[string]string{
		f.server.URL + "/feed.xml": {"stable-guid": fmt.Sprintf("h:%x", sum[:8])},
	}
	updated, next := f.sync(t, cursor)
	if len(updated) != 1 {
		t.Fatalf("expected one-time refresh of legacy body-only cursor, got %d", len(updated))
	}
	assertIdentityUnchanged(t, f, next)
}

func TestIncrementalItemContentWithoutLink(t *testing.T) {
	f := newIdentityFeed(t)
	f.xml.Store(`<rss version="2.0"><channel><title>Feed</title><description>Test</description>
<link>https://example.com</link>
<item><guid>stable-guid</guid><title>Feed-only</title><description>Original body</description></item></channel></rss>`)
	original, cursor := f.sync(t, nil)
	f.xml.Store(strings.ReplaceAll(f.xml.Load().(string), "Original body", "Changed body"))
	updated, next := f.sync(t, cursor)
	if len(original) != 1 || len(updated) != 1 || string(updated[0].Content) != "Changed body" {
		t.Fatal("body-only changes must still update feed-only entries")
	}
	assertIdentityUnchanged(t, f, next)
}
