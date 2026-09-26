package opds

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// TestMain whitelists loopback for SSRF so the httptest servers (127.0.0.1) are
// reachable. Production keeps the default strict SSRF policy.
func TestMain(m *testing.M) {
	_ = os.Setenv("SSRF_WHITELIST", "127.0.0.1,::1")
	utils.ResetSSRFWhitelistForTest()
	code := m.Run()
	os.Exit(code)
}

// bookBytes is a stand-in for a downloaded acquisition file.
const bookBytes = "PK\x03\x04fake-epub-payload"

// fixture serves an OPDS catalog tree plus book downloads, recording the
// requests it receives so tests can assert on auth headers and download counts.
type fixture struct {
	server *httptest.Server

	// seenAuthHeaders records every Authorization header the book endpoints
	// received. The credential-scoping test asserts it stays empty for the CDN.
	seenAuthHeaders []string
	seenCustomHdrs  []string

	bookDownloads atomic.Int32
	feedFetches   atomic.Int32

	failCatalog atomic.Bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{}
	mux := http.NewServeMux()

	recordAuth := func(r *http.Request) {
		if v := r.Header.Get("Authorization"); v != "" {
			f.seenAuthHeaders = append(f.seenAuthHeaders, v)
		}
		if v := r.Header.Get("X-Test-Auth"); v != "" {
			f.seenCustomHdrs = append(f.seenCustomHdrs, v)
		}
	}

	// Root: a navigation feed pointing at one acquisition sub-feed.
	mux.HandleFunc("/opds", func(w http.ResponseWriter, _ *http.Request) {
		f.feedFetches.Add(1)
		if f.failCatalog.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/atom+xml;profile=opds-catalog")
		_, _ = fmt.Fprint(w, navFeed)
	})

	mux.HandleFunc("/opds/fiction", func(w http.ResponseWriter, _ *http.Request) {
		f.feedFetches.Add(1)
		w.Header().Set("Content-Type", "application/atom+xml;profile=opds-catalog")
		_, _ = fmt.Fprint(w, acqFeed)
	})

	mux.HandleFunc("/opds/nonfiction", func(w http.ResponseWriter, _ *http.Request) {
		f.feedFetches.Add(1)
		w.Header().Set("Content-Type", "application/atom+xml;profile=opds-catalog")
		_, _ = fmt.Fprint(w, acqFeed)
	})

	// Book downloads. Auth headers are recorded so the scoping test can prove
	// the catalog credentials do not travel to book hosts.
	book := func(w http.ResponseWriter, r *http.Request) {
		recordAuth(r)
		f.bookDownloads.Add(1)
		w.Header().Set("Content-Type", "application/epub+zip")
		_, _ = fmt.Fprint(w, bookBytes)
	}
	mux.HandleFunc("/opds/download/", book)
	mux.HandleFunc("/download/", book)
	mux.HandleFunc("/buy/", book)
	mux.HandleFunc("/sample/", book)

	// Paged feed.
	mux.HandleFunc("/opds/paged", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml;profile=opds-catalog")
		if r.URL.Query().Get("page") == "2" {
			_, _ = fmt.Fprint(w, pagedFeedPage2)
			return
		}
		_, _ = fmt.Fprint(w, pagedFeedPage1)
	})

	// A feed whose next link points back at itself.
	mux.HandleFunc("/opds/loop", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml;profile=opds-catalog")
		_, _ = fmt.Fprint(w, selfLoopingFeed)
	})

	// A second catalog that always fails, for partial-failure tests.
	mux.HandleFunc("/broken", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fixture) url(path string) string { return f.server.URL + path }

// makeConfig builds a DataSourceConfig the way the UI would: catalog URLs in
// Settings (non-secret) and credentials in Credentials (encrypted at rest).
func makeConfig(catalogURLs, username, password, headers string) *types.DataSourceConfig {
	cfg := &types.DataSourceConfig{
		Type:        types.ConnectorTypeOPDS,
		Settings:    map[string]interface{}{"catalog_urls": catalogURLs},
		Credentials: map[string]interface{}{},
	}
	if username != "" {
		cfg.Credentials["username"] = username
	}
	if password != "" {
		cfg.Credentials["password"] = password
	}
	if headers != "" {
		cfg.Credentials["auth_headers"] = headers
	}
	return cfg
}

func TestConnectorType(t *testing.T) {
	if got := NewConnector().Type(); got != types.ConnectorTypeOPDS {
		t.Fatalf("Type() = %q, want %q", got, types.ConnectorTypeOPDS)
	}
}

func TestParseConfigRequiresCatalogURLs(t *testing.T) {
	if _, err := parseConfig(makeConfig("  ", "", "", "")); err == nil {
		t.Fatal("expected an error when catalog_urls is blank")
	}
	if _, err := parseConfig(nil); err == nil {
		t.Fatal("expected an error for a nil config")
	}
}

func TestParseConfigReadsSettingsAndCredentials(t *testing.T) {
	cfg, err := parseConfig(makeConfig(
		"https://a.example/opds\nhttps://b.example/opds", "u", "p", "X-Test-Auth: secret"))
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if got := cfg.catalogURLList(); len(got) != 2 {
		t.Fatalf("catalogURLList = %v, want 2 entries", got)
	}
	if !cfg.hasBasicAuth() {
		t.Fatal("expected basic auth to be configured")
	}
	if h := cfg.parseHeaders(); h["X-Test-Auth"] != "secret" {
		t.Fatalf("parseHeaders = %v, want X-Test-Auth: secret", h)
	}
}

// A lone username must not be sent as a half-formed Authorization header.
func TestParseConfigHalfBasicAuthIsNotUsed(t *testing.T) {
	cfg, err := parseConfig(makeConfig("https://a.example/opds", "user-only", "", ""))
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.hasBasicAuth() {
		t.Fatal("a username without a password must not count as Basic auth")
	}
}

func TestValidate(t *testing.T) {
	f := newFixture(t)
	conn := NewConnector()

	if err := conn.Validate(context.Background(), makeConfig(f.url("/opds"), "", "", "")); err != nil {
		t.Fatalf("Validate on a good catalog: %v", err)
	}

	cfg := makeConfig(f.url("/broken"), "", "", "")
	if err := conn.Validate(context.Background(), cfg); err == nil {
		t.Fatal("expected Validate to fail when the only catalog is unreachable")
	}

	// One good catalog among several is enough to validate.
	multi := makeConfig(f.url("/broken")+"\n"+f.url("/opds"), "", "", "")
	if err := conn.Validate(context.Background(), multi); err != nil {
		t.Fatalf("Validate should succeed when one catalog works: %v", err)
	}
}

func TestListResourcesRoot(t *testing.T) {
	f := newFixture(t)
	res, err := NewConnector().ListResources(context.Background(), makeConfig(f.url("/opds"), "", "", ""), "")
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected one root resource, got %d", len(res))
	}
	if res[0].Name != "Root Catalog" {
		t.Errorf("Name = %q, want the feed title", res[0].Name)
	}
	if !res[0].HasChildren {
		t.Error("a catalog root should be expandable")
	}
}

// A catalog that cannot be fetched still appears, with the failure in its
// description, so one broken entry does not fail the whole listing.
func TestListResourcesRootDegradesOnFetchFailure(t *testing.T) {
	f := newFixture(t)
	f.failCatalog.Store(true)

	res, err := NewConnector().ListResources(context.Background(), makeConfig(f.url("/opds"), "", "", ""), "")
	if err != nil {
		t.Fatalf("ListResources should not fail the whole listing: %v", err)
	}
	if len(res) != 1 || !strings.Contains(res[0].Description, "fetch failed") {
		t.Fatalf("expected a degraded resource, got %+v", res)
	}
}

func TestListResourcesChildren(t *testing.T) {
	f := newFixture(t)
	root := f.url("/opds")

	// Children of the root are sections.
	children, err := NewConnector().ListResources(context.Background(), makeConfig(root, "", "", ""), root)
	if err != nil {
		t.Fatalf("ListResources children: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(children))
	}
	for _, c := range children {
		if c.Type != "navigation" {
			t.Errorf("child %q type = %q, want navigation", c.Name, c.Type)
		}
		if c.ParentID != root {
			t.Errorf("child %q ParentID = %q, want %q", c.Name, c.ParentID, root)
		}
	}

	// Children of an acquisition feed are books.
	fictionURL := f.url("/opds/fiction")
	books, err := NewConnector().ListResources(context.Background(), makeConfig(root, "", "", ""), fictionURL)
	if err != nil {
		t.Fatalf("ListResources books: %v", err)
	}
	var bookCount int
	for _, b := range books {
		if b.Type == "book" {
			bookCount++
			if b.HasChildren {
				t.Errorf("book %q must be a leaf", b.Name)
			}
		}
	}
	// "Paywalled" has only a buy link, so it is not offered; "Kindle Only" is
	// an unsupported format but is still listed (the format filter is applied
	// at fetch time, not in the picker).
	if bookCount < 3 {
		t.Fatalf("expected at least 3 books, got %d", bookCount)
	}
}

// The security-critical test: Basic credentials and custom headers must reach
// the configured catalog host but never a third-party host serving the files.
func TestCredentialsAreScopedToCatalogHosts(t *testing.T) {
	var (
		cdnMu     sync.Mutex
		cdnAuth   []string
		cdnCustom []string
	)
	var cdnHits atomic.Int32
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cdnHits.Add(1)
		cdnMu.Lock()
		if v := r.Header.Get("Authorization"); v != "" {
			cdnAuth = append(cdnAuth, v)
		}
		if v := r.Header.Get("X-Test-Auth"); v != "" {
			cdnCustom = append(cdnCustom, v)
		}
		cdnMu.Unlock()
		w.Header().Set("Content-Type", "application/epub+zip")
		_, _ = fmt.Fprint(w, bookBytes)
	}))
	t.Cleanup(cdn.Close)

	// Record what the catalog host itself receives.
	var (
		catMu     sync.Mutex
		catAuth   []string
		catCustom []string
	)
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		catMu.Lock()
		if v := r.Header.Get("Authorization"); v != "" {
			catAuth = append(catAuth, v)
		}
		if v := r.Header.Get("X-Test-Auth"); v != "" {
			catCustom = append(catCustom, v)
		}
		catMu.Unlock()
		w.Header().Set("Content-Type", "application/atom+xml;profile=opds-catalog")
		_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <id>urn:catalog</id><title>Private Catalog</title>
  <link rel="self" href="%s" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>
  <entry>
    <title>Secret Book</title><id>urn:book:secret</id>
    <link rel="http://opds-spec.org/acquisition/open-access" href="%s/book.epub" type="application/epub+zip"/>
  </entry>
</feed>`, r.Host, cdn.URL)
	}))
	t.Cleanup(catalog.Close)

	cfg := makeConfig(catalog.URL, "alice", "s3cret", "X-Test-Auth: tok")
	conn := NewConnector()

	items, err := conn.FetchAll(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if cdnHits.Load() != 1 {
		t.Fatalf("expected the CDN to be hit once, got %d", cdnHits.Load())
	}

	// Positive control: the configured catalog host DID receive the credentials.
	// Without this, a regression that stripped credentials everywhere would
	// make the leak assertions below pass vacuously.
	catMu.Lock()
	gotCatAuth, gotCatCustom := len(catAuth), len(catCustom)
	catMu.Unlock()
	if gotCatAuth == 0 {
		t.Fatal("the configured catalog host should have received Basic credentials")
	}
	if gotCatCustom == 0 {
		t.Fatal("the configured catalog host should have received the custom header")
	}

	// The CDN must NOT have received them.
	cdnMu.Lock()
	defer cdnMu.Unlock()
	if len(cdnAuth) != 0 {
		t.Fatalf("credentials leaked to a third-party host: %v", cdnAuth)
	}
	if len(cdnCustom) != 0 {
		t.Fatalf("custom headers leaked to a third-party host: %v", cdnCustom)
	}
}

// Conversely, when the catalog host IS the book host, the credentials are sent.
func TestCredentialsSentToCatalogHost(t *testing.T) {
	f := newFixture(t)
	cfg := makeConfig(f.url("/opds"), "alice", "s3cret", "X-Test-Auth: tok")

	if _, err := NewConnector().FetchAll(context.Background(), cfg, nil); err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(f.seenAuthHeaders) == 0 {
		t.Fatal("expected the catalog host to receive Basic credentials")
	}
	if !strings.HasPrefix(f.seenAuthHeaders[0], "Basic ") {
		t.Fatalf("expected a Basic Authorization header, got %q", f.seenAuthHeaders[0])
	}
	if len(f.seenCustomHdrs) == 0 || f.seenCustomHdrs[0] != "tok" {
		t.Fatalf("expected the custom header on the catalog host, got %v", f.seenCustomHdrs)
	}
}

// Public catalogs need no credentials at all.
func TestFetchAllWithoutCredentials(t *testing.T) {
	f := newFixture(t)
	items, err := NewConnector().FetchAll(context.Background(), makeConfig(f.url("/opds"), "", "", ""), nil)
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected books from the public catalog")
	}
	if len(f.seenAuthHeaders) != 0 {
		t.Fatalf("no credentials were configured but Authorization was sent: %v", f.seenAuthHeaders)
	}
}

// Selecting the root catalog follows one level of sub-feeds, and the resulting
// items carry the file bytes, the right extension and the OPDS channel.
func TestFetchAllTraversesOneLevelAndBuildsItems(t *testing.T) {
	f := newFixture(t)
	items, err := NewConnector().FetchAll(context.Background(), makeConfig(f.url("/opds"), "", "", ""), nil)
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}

	byTitle := map[string]types.FetchedItem{}
	for _, it := range items {
		byTitle[it.Title] = it
	}

	open, ok := byTitle["Open Book"]
	if !ok {
		t.Fatalf("expected 'Open Book' to be ingested, got %v", keys(byTitle))
	}
	if string(open.Content) != bookBytes {
		t.Errorf("Content = %q, want the downloaded bytes", open.Content)
	}
	if !strings.HasSuffix(open.FileName, ".epub") {
		t.Errorf("FileName = %q, want an .epub extension", open.FileName)
	}
	if open.Metadata["channel"] != types.ChannelOPDS {
		t.Errorf("channel = %q, want %q", open.Metadata["channel"], types.ChannelOPDS)
	}
	if open.Metadata["author"] != "A. Writer" {
		t.Errorf("author = %q, want A. Writer", open.Metadata["author"])
	}
	if open.ExternalID == "" {
		t.Error("ExternalID must be set for datasource identity")
	}

	// The paywalled entry must not be fetched or ingested.
	if _, ok := byTitle["Paywalled"]; ok {
		t.Error("a buy-only entry must not be ingested")
	}
	// The unsupported mobi entry is skipped rather than failing the sync.
	if _, ok := byTitle["Kindle Only"]; ok {
		t.Error("an unsupported acquisition format must be skipped")
	}
}

func TestFetchAllFollowsPagination(t *testing.T) {
	f := newFixture(t)
	items, err := NewConnector().FetchAll(context.Background(), makeConfig(f.url("/opds/paged"), "", "", ""), nil)
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	titles := map[string]bool{}
	for _, it := range items {
		titles[it.Title] = true
	}
	if !titles["Page One Book"] || !titles["Page Two Book"] {
		t.Fatalf("pagination was not followed, got %v", keys(titles))
	}
}

// A next link pointing back at its own page must not loop forever.
func TestFetchAllTerminatesOnSelfReferentialNext(t *testing.T) {
	f := newFixture(t)
	items, err := NewConnector().FetchAll(context.Background(), makeConfig(f.url("/opds/loop"), "", "", ""), nil)
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected the single book exactly once, got %d items", len(items))
	}
}

// The second sync of an unchanged catalog must not re-download any book.
func TestIncrementalSkipsUnchangedBooks(t *testing.T) {
	f := newFixture(t)
	cfg := makeConfig(f.url("/opds"), "", "", "")
	conn := NewConnector()
	ctx := context.Background()

	// First sync: everything is new.
	items, cursor, err := conn.FetchIncremental(ctx, cfg, nil)
	if err != nil {
		t.Fatalf("first FetchIncremental: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected items on the first sync")
	}
	firstDownloads := f.bookDownloads.Load()
	if firstDownloads == 0 {
		t.Fatal("expected downloads on the first sync")
	}

	// Second sync against an unchanged catalog: no downloads, no items.
	items2, cursor2, err := conn.FetchIncremental(ctx, cfg, cursor)
	if err != nil {
		t.Fatalf("second FetchIncremental: %v", err)
	}
	if len(items2) != 0 {
		t.Fatalf("expected no items on an unchanged second sync, got %d", len(items2))
	}
	if got := f.bookDownloads.Load(); got != firstDownloads {
		t.Fatalf("expected no new downloads, went from %d to %d", firstDownloads, got)
	}
	if cursor2 == nil {
		t.Fatal("the second sync must still return a cursor")
	}
}

// One failing catalog among several is a partial failure, not a hard error.
func TestPartialFailure(t *testing.T) {
	f := newFixture(t)
	cfg := makeConfig(f.url("/broken")+"\n"+f.url("/opds"), "", "", "")

	items, err := NewConnector().FetchAll(context.Background(), cfg, nil)
	if err == nil {
		t.Fatal("expected a partial failure error")
	}
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("expected *PartialFetchError, got %T: %v", err, err)
	}
	if len(items) == 0 {
		t.Fatal("the working catalog should still have produced items")
	}
}

// A root navigation catalog where one section works and another is broken is a
// partial success. Regression test: the "all catalogs failed" verdict must be
// judged against the top-level selections, not every feed visited at depth, or
// a root whose sections partly succeeded would be misreported as dead.
func TestRootWithOneBrokenSectionIsPartial(t *testing.T) {
	root := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/opds":
			w.Header().Set("Content-Type", "application/atom+xml;profile=opds-catalog")
			_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <id>urn:root</id><title>Root</title>
  <link rel="self" href="%s/opds" type="application/atom+xml;profile=opds-catalog;kind=navigation"/>
  <entry><title>Good</title><id>urn:good</id>
    <link rel="subsection" href="/opds/good" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/></entry>
  <entry><title>Bad</title><id>urn:bad</id>
    <link rel="subsection" href="/opds/bad" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/></entry>
</feed>`, r.Host)
		case "/opds/good":
			w.Header().Set("Content-Type", "application/atom+xml;profile=opds-catalog")
			_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <id>urn:good</id><title>Good</title>
  <link rel="self" href="%s/opds/good" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>
  <entry><title>Book</title><id>urn:b1</id>
    <link rel="http://opds-spec.org/acquisition/open-access" href="/dl/b.epub" type="application/epub+zip"/></entry>
</feed>`, r.Host)
		case "/dl/b.epub":
			_, _ = fmt.Fprint(w, bookBytes)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(root.Close)

	items, err := NewConnector().FetchAll(context.Background(), makeConfig(root.URL+"/opds", "", "", ""), nil)
	if len(items) != 1 {
		t.Fatalf("expected the working section's book, got %d items (err=%v)", len(items), err)
	}
	if err == nil {
		t.Fatal("expected a partial failure error")
	}
	var partial *datasource.PartialFetchError
	if !errors.As(err, &partial) {
		t.Fatalf("expected *PartialFetchError, got %T: %v", err, err)
	}
}

// Every catalog failing is a hard error.
func TestAllCatalogsFail(t *testing.T) {
	f := newFixture(t)
	cfg := makeConfig(f.url("/broken")+"\n"+f.url("/broken"), "", "", "")

	_, err := NewConnector().FetchAll(context.Background(), cfg, nil)
	if err == nil {
		t.Fatal("expected an error when every catalog fails")
	}
	var partial *datasource.PartialFetchError
	if errors.As(err, &partial) {
		t.Fatal("all-failed must be a hard error, not a partial one")
	}
}

// FetchStream emits items through the handler and returns a cursor.
func TestFetchStreamEmitsAndCheckpoints(t *testing.T) {
	f := newFixture(t)
	h := &recordingHandler{}
	conn := NewConnector()

	cursor, err := conn.FetchStream(context.Background(), makeConfig(f.url("/opds"), "", "", ""), nil, h)
	if err != nil {
		t.Fatalf("FetchStream: %v", err)
	}
	if len(h.items) == 0 {
		t.Fatal("expected streamed items")
	}
	if h.checkpoints == 0 {
		t.Fatal("expected at least one checkpoint")
	}
	if cursor == nil {
		t.Fatal("expected a cursor")
	}
}

// A handler error aborts the stream.
func TestFetchStreamPropagatesEmitError(t *testing.T) {
	f := newFixture(t)
	wantErr := errors.New("ingest failed")
	h := &recordingHandler{err: wantErr}

	_, err := NewConnector().FetchStream(context.Background(), makeConfig(f.url("/opds"), "", "", ""), nil, h)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the handler error to propagate, got %v", err)
	}
}

type recordingHandler struct {
	items       []types.FetchedItem
	checkpoints int
	err         error
}

func (h *recordingHandler) Emit(_ context.Context, item types.FetchedItem) error {
	if h.err != nil {
		return h.err
	}
	h.items = append(h.items, item)
	return nil
}

func (h *recordingHandler) Checkpoint(_ context.Context, _ *types.SyncCursor) error {
	h.checkpoints++
	return nil
}

// The incremental cursor must survive the encode/decode round-trip through the
// generic SyncCursor, otherwise every sync would re-download the whole catalog.
func TestCursorRoundTrip(t *testing.T) {
	orig := &opdsCursor{
		LastSyncTime: time.Now().UTC().Truncate(time.Second),
		Signals:      map[string]map[string]string{"http://c/opds": {"urn:b1": "s:aaa"}},
		Hashes:       map[string]map[string]string{"http://c/opds": {"urn:b1": "h:bbb"}},
	}
	enc := encodeCursor(orig)
	if enc == nil {
		t.Fatal("encodeCursor returned nil")
	}
	got := decodeCursor(context.Background(), enc)
	if got == nil {
		t.Fatal("decodeCursor returned nil")
	}
	if got.Signals["http://c/opds"]["urn:b1"] != "s:aaa" {
		t.Fatalf("signals lost in round-trip: %+v", got.Signals)
	}
	if got.Hashes["http://c/opds"]["urn:b1"] != "h:bbb" {
		t.Fatalf("hashes lost in round-trip: %+v", got.Hashes)
	}

	// A nil or empty cursor must decode to nil, not panic.
	if decodeCursor(context.Background(), nil) != nil {
		t.Error("a nil cursor should decode to nil")
	}
	if encodeCursor(nil) != nil {
		t.Error("a nil cursor should encode to nil")
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
