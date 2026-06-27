package rss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/mmcdole/gofeed"
)

// TestMain whitelists loopback for SSRF so the httptest servers (127.0.0.1)
// are reachable. Production keeps the default strict SSRF policy.
func TestMain(m *testing.M) {
	_ = os.Setenv("SSRF_WHITELIST", "127.0.0.1,::1")
	utils.ResetSSRFWhitelistForTest()
	code := m.Run()
	os.Exit(code)
}

const longArticleBody = `<p>This is the first paragraph of a reasonably long article that the ` +
	`readability extractor should detect as the main content of the page. It contains ` +
	`enough words to clear the minimum content threshold used by the algorithm.</p>` +
	`<p>The second paragraph continues the discussion with more sentences so that the ` +
	`scoring heuristics confidently select this block over navigation and footer noise.</p>` +
	`<p>A third paragraph adds further substance, ensuring the article is unmistakably ` +
	`the dominant readable region of the document under test.</p>`

// fakeFeed spins up an httptest server serving an RSS feed and article pages.
type fakeFeed struct {
	server             *httptest.Server
	feedTitle          string
	itemContent        string // optional <description>/content for items
	articleAuthHeaders []string
	articleFetches     atomic.Int32
	failFeed           atomic.Bool
}

func newFakeFeed(t *testing.T) *fakeFeed {
	t.Helper()
	f := &fakeFeed{feedTitle: "Test Feed"}
	mux := http.NewServeMux()

	mux.HandleFunc("/article/", func(w http.ResponseWriter, r *http.Request) {
		f.articleFetches.Add(1)
		for k, vals := range r.Header {
			if strings.EqualFold(k, "X-Test-Auth") && len(vals) > 0 && vals[0] != "" {
				f.articleAuthHeaders = append(f.articleAuthHeaders, vals[0])
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!DOCTYPE html><html><head><title>%s</title></head>`+
			`<body><nav>menu</nav><article><h1>Heading</h1>%s</article><footer>foot</footer></body></html>`,
			"Full "+r.URL.Path, longArticleBody)
	})

	mux.HandleFunc("/feed.xml", func(w http.ResponseWriter, r *http.Request) {
		if f.failFeed.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if auth := r.Header.Get("X-Test-Auth"); f.itemContent == "needs-auth" && auth != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		base := "http://" + r.Host
		desc := "summary fallback"
		fmt.Fprintf(w, `<?xml version="1.0"?>
<rss version="2.0"><channel>
<title>%s</title>
<link>%s</link>
<description>A test feed</description>
<item>
  <title>Article One</title>
  <link>%s/article/a1</link>
  <guid>guid-1</guid>
  <pubDate>Mon, 02 Jan 2006 15:04:05 GMT</pubDate>
  <description>%s</description>
</item>
<item>
  <title>Article Two</title>
  <link>%s/article/a2</link>
  <guid>guid-2</guid>
  <pubDate>Tue, 03 Jan 2006 15:04:05 GMT</pubDate>
  <description>%s</description>
</item>
</channel></rss>`, f.feedTitle, base, base, desc, base, desc)
	})

	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeFeed) feedURL() string { return f.server.URL + "/feed.xml" }

func makeConfig(feedURLs string, headers string) *types.DataSourceConfig {
	cfg := &types.DataSourceConfig{
		Type: types.ConnectorTypeRSS,
		Settings: map[string]interface{}{
			"feed_urls": feedURLs,
		},
		Credentials: map[string]interface{}{},
	}
	if headers != "" {
		cfg.Credentials["auth_headers"] = headers
	}
	return cfg
}

func TestConnector_Type(t *testing.T) {
	if NewConnector().Type() != types.ConnectorTypeRSS {
		t.Fatalf("Type() = %q, want %q", NewConnector().Type(), types.ConnectorTypeRSS)
	}
}

func TestParseConfig_RequiresFeedURLs(t *testing.T) {
	if _, err := parseConfig(makeConfig("   ", "")); err == nil {
		t.Fatal("expected error when feed_urls is blank")
	}
}

func TestParseConfig_FeedURLsFromSettings(t *testing.T) {
	cfg, err := parseConfig(makeConfig("https://example.com/feed.xml", ""))
	if err != nil {
		t.Fatalf("parseConfig error: %v", err)
	}
	got := cfg.feedURLList()
	if len(got) != 1 || got[0] != "https://example.com/feed.xml" {
		t.Fatalf("feedURLList = %v", got)
	}
}

func TestParseConfig_LegacyFeedURLsInCredentials(t *testing.T) {
	legacy := &types.DataSourceConfig{
		Type: types.ConnectorTypeRSS,
		Credentials: map[string]interface{}{
			"feed_urls": "https://legacy.example/feed.xml",
		},
	}
	cfg, err := parseConfig(legacy)
	if err != nil {
		t.Fatalf("parseConfig error: %v", err)
	}
	if got := cfg.feedURLList(); len(got) != 1 || got[0] != "https://legacy.example/feed.xml" {
		t.Fatalf("feedURLList = %v", got)
	}
}

func TestConfig_FeedURLList_SplitsAndDedupes(t *testing.T) {
	cfg := &Config{FeedURLs: "https://a.com/f, https://b.com/f\nhttps://a.com/f\n"}
	got := cfg.feedURLList()
	want := []string{"https://a.com/f", "https://b.com/f"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("feedURLList = %v, want %v", got, want)
	}
}

func TestConfig_ParseHeaders(t *testing.T) {
	cfg := &Config{AuthHeaders: "Authorization: Bearer x\nX-Foo:  bar \nbroken-line\n: noname"}
	got := cfg.parseHeaders()
	if got["Authorization"] != "Bearer x" {
		t.Errorf("Authorization = %q", got["Authorization"])
	}
	if got["X-Foo"] != "bar" {
		t.Errorf("X-Foo = %q", got["X-Foo"])
	}
	if len(got) != 2 {
		t.Errorf("expected 2 headers, got %d: %v", len(got), got)
	}
}

func TestFeedSignalFingerprint(t *testing.T) {
	item := &gofeed.Item{GUID: "g1", Link: "https://example.com/a", Title: "t"}
	sig1 := feedSignalFingerprint(item, "body")
	sig2 := feedSignalFingerprint(item, "body")
	if sig1 == "" || sig1 != sig2 {
		t.Fatalf("feed signal unstable: %q vs %q", sig1, sig2)
	}
	sig3 := feedSignalFingerprint(item, "changed")
	if sig1 == sig3 {
		t.Fatal("expected different signal when feed content changes")
	}
}

func TestItemExternalID_ScopesByFeed(t *testing.T) {
	got := itemExternalID("https://a.com/feed", "guid-1")
	want := "https://a.com/feed:guid-1"
	if got != want {
		t.Fatalf("itemExternalID = %q, want %q", got, want)
	}
}

func TestConnector_Validate_Success(t *testing.T) {
	f := newFakeFeed(t)
	if err := NewConnector().Validate(context.Background(), makeConfig(f.feedURL(), "")); err != nil {
		t.Fatalf("Validate error: %v", err)
	}
}

func TestConnector_Validate_PrivateFeedWithHeader(t *testing.T) {
	f := newFakeFeed(t)
	f.itemContent = "needs-auth"

	// Without the header → 401.
	if err := NewConnector().Validate(context.Background(), makeConfig(f.feedURL(), "")); err == nil {
		t.Fatal("expected error without auth header")
	}
	// With the header → success.
	if err := NewConnector().Validate(
		context.Background(), makeConfig(f.feedURL(), "X-Test-Auth: secret"),
	); err != nil {
		t.Fatalf("Validate with header error: %v", err)
	}
}

func TestConnector_FetchAll_DoesNotSendAuthHeadersToArticles(t *testing.T) {
	f := newFakeFeed(t)
	f.itemContent = "needs-auth"
	_, err := NewConnector().FetchAll(
		context.Background(), makeConfig(f.feedURL(), "X-Test-Auth: secret"), nil,
	)
	if err != nil {
		t.Fatalf("FetchAll error: %v", err)
	}
	if len(f.articleAuthHeaders) != 0 {
		t.Fatalf("article requests must not carry feed auth headers, got %v", f.articleAuthHeaders)
	}
}

func TestConnector_ListResources(t *testing.T) {
	f := newFakeFeed(t)
	res, err := NewConnector().ListResources(context.Background(), makeConfig(f.feedURL(), ""), "")
	if err != nil {
		t.Fatalf("ListResources error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(res))
	}
	if res[0].ExternalID != f.feedURL() {
		t.Errorf("ExternalID = %q, want %q", res[0].ExternalID, f.feedURL())
	}
	if res[0].Name != "Test Feed" {
		t.Errorf("Name = %q, want %q", res[0].Name, "Test Feed")
	}

	// Non-empty parentID → empty (feeds are flat).
	children, err := NewConnector().ListResources(context.Background(), makeConfig(f.feedURL(), ""), "feed-x")
	if err != nil {
		t.Fatalf("ListResources(parent) error: %v", err)
	}
	if len(children) != 0 {
		t.Fatalf("expected no children, got %d", len(children))
	}
}

func TestConnector_FetchAll_FullTextMarkdown(t *testing.T) {
	f := newFakeFeed(t)
	items, err := NewConnector().FetchAll(context.Background(), makeConfig(f.feedURL(), ""), nil)
	if err != nil {
		t.Fatalf("FetchAll error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	it := items[0]
	wantExternalID := itemExternalID(f.feedURL(), "guid-1")
	if it.ExternalID != wantExternalID {
		t.Errorf("ExternalID = %q, want %q", it.ExternalID, wantExternalID)
	}
	if it.ContentType != "text/markdown" {
		t.Errorf("ContentType = %q, want text/markdown", it.ContentType)
	}
	if it.Metadata["channel"] != types.ChannelRSS {
		t.Errorf("channel = %q, want %q", it.Metadata["channel"], types.ChannelRSS)
	}
	// Full text from the article page should be present (not the short summary).
	if !strings.Contains(string(it.Content), "first paragraph") {
		t.Errorf("expected full article text in content, got: %q", string(it.Content))
	}
	if !strings.HasSuffix(it.FileName, ".md") {
		t.Errorf("FileName = %q, want .md suffix", it.FileName)
	}
}

func TestConnector_FetchIncremental_SkipsWithoutArticleFetch(t *testing.T) {
	f := newFakeFeed(t)
	cfg := makeConfig(f.feedURL(), "")
	cfg.ResourceIDs = []string{f.feedURL()}

	items, cursor, err := NewConnector().FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("first FetchIncremental error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items on first sync, got %d", len(items))
	}
	if got := f.articleFetches.Load(); got != 2 {
		t.Fatalf("first sync article fetches = %d, want 2", got)
	}

	f.articleFetches.Store(0)
	items2, _, err := NewConnector().FetchIncremental(context.Background(), cfg, cursor)
	if err != nil {
		t.Fatalf("second FetchIncremental error: %v", err)
	}
	if len(items2) != 0 {
		t.Fatalf("expected 0 items on unchanged second sync, got %d", len(items2))
	}
	if got := f.articleFetches.Load(); got != 0 {
		t.Fatalf("unchanged incremental sync must not refetch articles, got %d fetches", got)
	}
}

func TestConnector_FetchIncremental_SkipsUnchanged(t *testing.T) {
	f := newFakeFeed(t)
	cfg := makeConfig(f.feedURL(), "")
	cfg.ResourceIDs = []string{f.feedURL()}

	// First sync: everything is new.
	items, cursor, err := NewConnector().FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("first FetchIncremental error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items on first sync, got %d", len(items))
	}
	if cursor == nil {
		t.Fatal("expected non-nil cursor")
	}

	// Second sync with the returned cursor: nothing changed → no items.
	items2, _, err := NewConnector().FetchIncremental(context.Background(), cfg, cursor)
	if err != nil {
		t.Fatalf("second FetchIncremental error: %v", err)
	}
	if len(items2) != 0 {
		t.Fatalf("expected 0 items on unchanged second sync, got %d", len(items2))
	}
}

func TestConnector_Walk_PartialFeedFailure(t *testing.T) {
	f := newFakeFeed(t)
	cfg := makeConfig(f.feedURL()+", https://invalid.invalid/feed.xml", "")
	items, err := NewConnector().FetchAll(context.Background(), cfg, nil)
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("expected PartialFetchError, got %v", err)
	}
	if len(partial.Details) != 1 {
		t.Fatalf("expected 1 feed error, got %v", partial.Details)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items from healthy feed, got %d", len(items))
	}
}

func TestConnector_Walk_PreservesCursorOnFeedFailure(t *testing.T) {
	f := newFakeFeed(t)
	cfg := makeConfig(f.feedURL(), "")
	cfg.ResourceIDs = []string{f.feedURL()}

	_, cursor, err := NewConnector().FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("first sync error: %v", err)
	}

	f.failFeed.Store(true)
	_, newCursor, err := NewConnector().FetchIncremental(context.Background(), cfg, cursor)
	if err == nil {
		t.Fatal("expected error when sole feed is unavailable")
	}
	if newCursor == nil || newCursor.ConnectorCursor == nil {
		t.Fatal("expected cursor to be preserved on feed failure")
	}

	var restored rssCursor
	b, err := json.Marshal(newCursor.ConnectorCursor)
	if err != nil {
		t.Fatalf("marshal cursor: %v", err)
	}
	if err := json.Unmarshal(b, &restored); err != nil {
		t.Fatalf("unmarshal cursor: %v", err)
	}
	items := restored.FeedItems[f.feedURL()]
	if len(items) != 2 {
		t.Fatalf("expected preserved fingerprints for 2 items, got %d", len(items))
	}
	if items["guid-1"] == "" || items["guid-2"] == "" {
		t.Fatalf("expected non-empty preserved fingerprints, got %+v", items)
	}
}

func TestConnector_ResolveResourceAncestors_Empty(t *testing.T) {
	got, err := NewConnector().ResolveResourceAncestors(
		context.Background(), makeConfig("https://a.com/f", ""), []string{"x"},
	)
	if err != nil {
		t.Fatalf("ResolveResourceAncestors error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty ancestors, got %v", got)
	}
}
