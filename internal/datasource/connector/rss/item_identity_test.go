package rss

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

type identityFeed struct {
	server        *httptest.Server
	xml           atomic.Value
	fetches       atomic.Int32
	articleStatus atomic.Int32
	articleBody   atomic.Value
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
		if status := int(f.articleStatus.Load()); status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, "<html><head><title>Stable article</title></head>"+
			"<body><article>%s</article></body></html>", f.articleBody.Load().(string))
	}))
	t.Cleanup(f.server.Close)
	f.articleStatus.Store(http.StatusOK)
	f.articleBody.Store(longArticleBody)
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

// legacyIdentityCursor syncs once and rewrites the cursor the way a release
// before item fingerprints stored it: a body-only "h:" hash.
func legacyIdentityCursor(t *testing.T, f *identityFeed) (*types.SyncCursor, string) {
	t.Helper()
	items, cursor := f.sync(t, nil)
	if len(items) != 1 {
		t.Fatalf("expected 1 initial item, got %d", len(items))
	}
	sum := sha256.Sum256(items[0].Content)
	legacy := fmt.Sprintf("h:%x", sum[:8])
	cursor.ConnectorCursor["feed_items"] = map[string]map[string]string{
		f.server.URL + "/feed.xml": {"stable-guid": legacy},
	}
	return cursor, legacy
}

func storedItemFingerprint(t *testing.T, f *identityFeed, cursor *types.SyncCursor) string {
	t.Helper()
	data, err := json.Marshal(cursor.ConnectorCursor)
	if err != nil {
		t.Fatalf("marshal cursor: %v", err)
	}
	var decoded rssCursor
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal cursor: %v", err)
	}
	return decoded.FeedItems[f.server.URL+"/feed.xml"]["stable-guid"]
}

// Upgrading must not delete and rebuild every stored item: an unchanged body
// only has its cursor upgraded to an item fingerprint.
func TestIncrementalItemLegacyCursorUnchangedBodyUpgradesInPlace(t *testing.T) {
	f := newIdentityFeed(t)
	cursor, _ := legacyIdentityCursor(t, f)
	updated, next := f.sync(t, cursor)
	if len(updated) != 0 {
		t.Fatalf("unchanged legacy item must not be re-ingested, got %d", len(updated))
	}
	if fp := storedItemFingerprint(t, f, next); !strings.HasPrefix(fp, itemFingerprintPrefix) {
		t.Fatalf("legacy cursor must be upgraded to an item fingerprint, got %q", fp)
	}
	assertIdentityUnchanged(t, f, next)
}

// A transient article failure during the upgrade must not replace the stored
// full text with the feed summary. The legacy cursor is kept so the next sync
// retries, and the upgrade completes once the article is reachable again.
func TestIncrementalItemLegacyCursorArticleFailureKeepsCursor(t *testing.T) {
	f := newIdentityFeed(t)
	cursor, legacy := legacyIdentityCursor(t, f)
	f.articleStatus.Store(http.StatusInternalServerError)
	updated, next := f.sync(t, cursor)
	if len(updated) != 0 {
		t.Fatalf("article failure must not emit the feed summary, got %d", len(updated))
	}
	if fp := storedItemFingerprint(t, f, next); fp != legacy {
		t.Fatalf("legacy cursor must be kept for retry, got %q want %q", fp, legacy)
	}

	f.articleStatus.Store(http.StatusOK)
	before := f.fetches.Load()
	updated, next = f.sync(t, next)
	if len(updated) != 0 || f.fetches.Load() == before {
		t.Fatalf("recovered article must be re-read and upgrade in place, emitted %d", len(updated))
	}
	if fp := storedItemFingerprint(t, f, next); !strings.HasPrefix(fp, itemFingerprintPrefix) {
		t.Fatalf("legacy cursor must be upgraded after recovery, got %q", fp)
	}
}

// A legacy item whose body really changed is still re-ingested.
func TestIncrementalItemLegacyCursorChangedBodyEmits(t *testing.T) {
	f := newIdentityFeed(t)
	cursor, _ := legacyIdentityCursor(t, f)
	f.articleBody.Store(longArticleBody + "<p>An appended paragraph that changes the stored body.</p>")
	updated, next := f.sync(t, cursor)
	if len(updated) != 1 || !strings.Contains(string(updated[0].Content), "appended paragraph") {
		t.Fatalf("changed legacy item must be re-ingested with the new body, got %d items", len(updated))
	}
	if fp := storedItemFingerprint(t, f, next); !strings.HasPrefix(fp, itemFingerprintPrefix) {
		t.Fatalf("re-ingested item must store an item fingerprint, got %q", fp)
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
