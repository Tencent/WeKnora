package opds

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// navFeed is a minimal navigation feed: its entries point at sub-feeds.
const navFeed = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom"
      xmlns:opds="http://opds-spec.org/2010/catalog">
  <id>urn:catalog:root</id>
  <title>Root Catalog</title>
  <updated>2024-01-01T00:00:00Z</updated>
  <link rel="self" href="/opds" type="application/atom+xml;profile=opds-catalog;kind=navigation"/>
  <entry>
    <title>Fiction</title>
    <id>urn:section:fiction</id>
    <updated>2024-01-01T00:00:00Z</updated>
    <link rel="subsection" href="/opds/fiction" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>
  </entry>
  <entry>
    <title>Non-fiction</title>
    <id>urn:section:nonfiction</id>
    <updated>2024-01-01T00:00:00Z</updated>
    <link rel="subsection" href="/opds/nonfiction" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>
  </entry>
</feed>`

// acqFeed is an acquisition feed with a mix of link shapes: a preferred
// open-access EPUB, a lower-priority borrow, a paywalled buy, a partial sample,
// an unsupported mobi, and a book whose only link is relative.
const acqFeed = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <id>urn:catalog:fiction</id>
  <title>Fiction</title>
  <updated>2024-02-01T00:00:00Z</updated>
  <link rel="self" href="/opds/fiction" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>
  <entry>
    <title>Open Book</title>
    <id>urn:book:open</id>
    <updated>2024-02-01T00:00:00Z</updated>
    <author><name>A. Writer</name></author>
    <summary>A freely available book.</summary>
    <link rel="http://opds-spec.org/acquisition/buy" href="/buy/open" type="application/epub+zip"/>
    <link rel="http://opds-spec.org/acquisition/sample" href="/sample/open" type="application/epub+zip"/>
    <link rel="http://opds-spec.org/acquisition/open-access" href="download/open.epub" type="application/epub+zip"/>
  </entry>
  <entry>
    <title>Borrowable</title>
    <id>urn:book:borrow</id>
    <updated>2024-02-01T00:00:00Z</updated>
    <link rel="http://opds-spec.org/acquisition/borrow" href="/download/borrow.epub" type="application/epub+zip"/>
  </entry>
  <entry>
    <title>Paywalled</title>
    <id>urn:book:buy</id>
    <updated>2024-02-01T00:00:00Z</updated>
    <link rel="http://opds-spec.org/acquisition/buy" href="/download/buy.epub" type="application/epub+zip"/>
  </entry>
  <entry>
    <title>Kindle Only</title>
    <id>urn:book:mobi</id>
    <updated>2024-02-01T00:00:00Z</updated>
    <link rel="http://opds-spec.org/acquisition/open-access"
          href="/download/k.mobi" type="application/x-mobipocket-ebook"/>
  </entry>
</feed>`

// pagedFeed links to a second page via rel="next".
const pagedFeedPage1 = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <id>urn:catalog:paged</id>
  <title>Paged</title>
  <link rel="self" href="/opds/paged" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>
  <link rel="next" href="/opds/paged?page=2" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>
  <entry>
    <title>Page One Book</title>
    <id>urn:book:p1</id>
    <link rel="http://opds-spec.org/acquisition/open-access" href="/download/p1.epub" type="application/epub+zip"/>
  </entry>
</feed>`

const pagedFeedPage2 = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <id>urn:catalog:paged</id>
  <title>Paged</title>
  <link rel="self" href="/opds/paged?page=2" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>
  <entry>
    <title>Page Two Book</title>
    <id>urn:book:p2</id>
    <link rel="http://opds-spec.org/acquisition/open-access" href="/download/p2.epub" type="application/epub+zip"/>
  </entry>
</feed>`

// selfLoopingFeed's next link points back at itself.
const selfLoopingFeed = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <id>urn:catalog:loop</id>
  <title>Loop</title>
  <link rel="self" href="/opds/loop" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>
  <link rel="next" href="/opds/loop" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>
  <entry>
    <title>Only Book</title>
    <id>urn:book:loop</id>
    <link rel="http://opds-spec.org/acquisition/open-access" href="/download/loop.epub" type="application/epub+zip"/>
  </entry>
</feed>`

func mustParse(t *testing.T, xmlDoc string) *opdsFeed {
	t.Helper()
	feed, err := parseFeed([]byte(xmlDoc))
	if err != nil {
		t.Fatalf("parseFeed: %v", err)
	}
	return feed
}

func TestParseFeedRejectsNonAtom(t *testing.T) {
	// OPDS 2.0 is JSON, and an HTML error page is the other realistic body.
	for name, body := range map[string]string{
		"json":  `{"metadata": {"title": "OPDS 2.0"}}`,
		"html":  `<html><body>Not found</body></html>`,
		"empty": ``,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseFeed([]byte(body)); err == nil {
				t.Fatalf("expected an error for a non-Atom body")
			}
		})
	}
}

func TestFeedKind(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want feedKind
	}{
		{"navigation via self link", navFeed, kindNavigation},
		{"acquisition via self link", acqFeed, kindAcquisition},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := feedKindOf(mustParse(t, tc.doc)); got != tc.want {
				t.Fatalf("feedKindOf = %v, want %v", got, tc.want)
			}
		})
	}
}

// A catalog that omits the kind= parameter must still be classified by looking
// at its entries.
func TestFeedKindFallsBackToEntries(t *testing.T) {
	noKind := `<feed xmlns="http://www.w3.org/2005/Atom">
	  <id>urn:x</id><title>No Kind</title>
	  <entry><title>Book</title><id>urn:b</id>
	    <link rel="http://opds-spec.org/acquisition/open-access" href="/b.epub" type="application/epub+zip"/>
	  </entry></feed>`
	if got := feedKindOf(mustParse(t, noKind)); got != kindAcquisition {
		t.Fatalf("feedKindOf = %v, want acquisition", got)
	}

	noKindNav := `<feed xmlns="http://www.w3.org/2005/Atom">
	  <id>urn:x</id><title>No Kind</title>
	  <entry><title>Section</title><id>urn:s</id>
	    <link rel="subsection" href="/s"/>
	  </entry></feed>`
	if got := feedKindOf(mustParse(t, noKindNav)); got != kindNavigation {
		t.Fatalf("feedKindOf = %v, want navigation", got)
	}
}

func TestAcquisitionLinkPreference(t *testing.T) {
	feed := mustParse(t, acqFeed)
	byID := map[string]*opdsEntry{}
	for i := range feed.Entries {
		byID[entryID(&feed.Entries[i])] = &feed.Entries[i]
	}

	t.Run("open-access beats buy and sample", func(t *testing.T) {
		l := acquisitionLink(byID["urn:book:open"])
		if l == nil {
			t.Fatal("expected an acquisition link")
		}
		if !strings.Contains(l.Href, "open.epub") {
			t.Fatalf("picked %q, want the open-access link", l.Href)
		}
	})

	t.Run("borrow is accepted as a fallback", func(t *testing.T) {
		l := acquisitionLink(byID["urn:book:borrow"])
		if l == nil || !strings.Contains(l.Href, "borrow.epub") {
			t.Fatalf("expected the borrow link, got %+v", l)
		}
	})

	t.Run("buy-only entry yields nothing", func(t *testing.T) {
		if l := acquisitionLink(byID["urn:book:buy"]); l != nil {
			t.Fatalf("buy is paywalled and must not be selected, got %q", l.Href)
		}
	})
}

func TestExtensionResolution(t *testing.T) {
	tests := []struct {
		mime, href, want string
	}{
		{"application/epub+zip", "/x", "epub"},
		{"application/pdf", "/x", "pdf"},
		{"application/epub+zip;charset=utf-8", "/x", "epub"},
		{"", "/download/book.epub", "epub"},
		{"application/octet-stream", "/download/BOOK.EPUB", "epub"},
		{"application/x-mobipocket-ebook", "/x", "mobi"},
	}
	for _, tc := range tests {
		got := extensionForLink(&opdsLink{Type: tc.mime, Href: tc.href})
		if got != tc.want {
			t.Errorf("extensionForLink(%q,%q) = %q, want %q", tc.mime, tc.href, got, tc.want)
		}
	}
}

// Everything the knowledge pipeline can parse must be accepted, so the
// connector never drops a format WeKnora could in fact ingest.
func TestSupportedAcquisitionExtensions(t *testing.T) {
	supported := []string{
		// E-books and documents
		"epub", "pdf", "txt", "html", "htm", "md", "markdown", "docx", "doc",
		"mhtml", "xlsx", "xls", "pptx", "ppt", "json", "csv", "xmind",
		// Images
		"png", "jpg", "jpeg", "gif",
		// Audiobooks
		"mp3", "wav", "m4a", "flac", "ogg",
	}
	for _, ext := range supported {
		if !isSupportedAcquisitionExtension(ext) {
			t.Errorf("%q should be supported", ext)
		}
	}

	// Formats the knowledge pipeline cannot parse are skipped, not failed.
	for _, ext := range []string{"mobi", "azw", "azw3", "fb2", "cbz", "cbr", "djvu", ""} {
		if isSupportedAcquisitionExtension(ext) {
			t.Errorf("%q should not be supported", ext)
		}
	}
}

// The connector must accept exactly the pipeline's canonical set. This is the
// regression guard for the drift #2447 fixed: a narrower local list would
// silently drop formats the pipeline can parse.
func TestSupportedExtensionsMatchPipeline(t *testing.T) {
	for ext := range types.SupportedImportFileExtensions {
		if !isSupportedAcquisitionExtension(ext) {
			t.Errorf("pipeline accepts %q but the OPDS connector rejects it", ext)
		}
	}
}

// An audiobook catalog entry must be ingestible: OPDS libraries commonly
// distribute audio, and the pipeline transcribes it via ASR.
func TestAcquisitionLinkResolvesAudiobook(t *testing.T) {
	entry := &opdsEntry{
		ID: "urn:audio:1", Title: "An Audiobook",
		Links: []opdsLink{
			{Rel: relAcquisitionOpen, Href: "/dl/book.mp3", Type: "audio/mpeg"},
		},
	}
	l := acquisitionLink(entry)
	if l == nil {
		t.Fatal("expected the audio acquisition link to be selected")
	}
	ext := extensionForLink(l)
	if ext != "mp3" {
		t.Fatalf("extension = %q, want mp3", ext)
	}
	if !isSupportedAcquisitionExtension(ext) {
		t.Fatal("an audiobook must be ingestible")
	}
}

// Office and audio MIME types must resolve to their extension even when the URL
// path carries no hint, so a catalog serving them is not silently skipped.
func TestMimeMapCoversPipelineFormats(t *testing.T) {
	cases := []struct{ mime, want string }{
		{"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "xlsx"},
		{"application/vnd.ms-excel", "xls"},
		{"application/vnd.openxmlformats-officedocument.presentationml.presentation", "pptx"},
		{"application/vnd.ms-powerpoint", "ppt"},
		{"audio/mpeg", "mp3"},
		{"audio/mp4", "m4a"},
		{"audio/flac", "flac"},
		{"audio/ogg", "ogg"},
		{"application/x-mimearchive", "mhtml"},
		{"application/x-xmind", "xmind"},
	}
	for _, tc := range cases {
		// A URL with no usable extension forces the MIME lookup to decide.
		got := extensionForLink(&opdsLink{Type: tc.mime, Href: "/download/42"})
		if got != tc.want {
			t.Errorf("extensionForLink(%q) = %q, want %q", tc.mime, got, tc.want)
		}
		if !isSupportedAcquisitionExtension(got) {
			t.Errorf("%q resolves to %q, which the connector rejects", tc.mime, got)
		}
	}
}

func TestResolveHref(t *testing.T) {
	base := mustParseURL(t, "https://catalog.example.com/opds/fiction")

	tests := []struct{ href, want string }{
		{"download/open.epub", "https://catalog.example.com/opds/download/open.epub"},
		{"/download/open.epub", "https://catalog.example.com/download/open.epub"},
		{"https://cdn.example.com/b.epub", "https://cdn.example.com/b.epub"},
		{"../other/b.epub", "https://catalog.example.com/other/b.epub"},
	}
	for _, tc := range tests {
		got, err := resolveHref(base, tc.href)
		if err != nil {
			t.Errorf("resolveHref(%q): %v", tc.href, err)
			continue
		}
		if got != tc.want {
			t.Errorf("resolveHref(%q) = %q, want %q", tc.href, got, tc.want)
		}
	}

	if _, err := resolveHref(base, ""); err == nil {
		t.Error("expected an error for an empty href")
	}
	if _, err := resolveHref(nil, "relative.epub"); err == nil {
		t.Error("expected an error for a relative href with no base")
	}
}

func TestNavigationLink(t *testing.T) {
	feed := mustParse(t, navFeed)
	for i := range feed.Entries {
		if navigationLink(&feed.Entries[i]) == nil {
			t.Fatalf("entry %q should be a navigation link", feed.Entries[i].Title)
		}
	}
	// A book entry is not a navigation link.
	acq := mustParse(t, acqFeed)
	if navigationLink(&acq.Entries[0]) != nil {
		t.Fatal("a book entry must not be treated as a section")
	}
}

func TestNextLink(t *testing.T) {
	if nextLink(mustParse(t, pagedFeedPage1)) == nil {
		t.Fatal("page 1 should expose a next link")
	}
	if nextLink(mustParse(t, pagedFeedPage2)) != nil {
		t.Fatal("the last page should have no next link")
	}
}

func TestEntryIDFallbacks(t *testing.T) {
	withID := &opdsEntry{ID: "urn:book:1", Title: "T"}
	if got := entryID(withID); got != "urn:book:1" {
		t.Errorf("entryID = %q, want the Atom id", got)
	}
	noID := &opdsEntry{Title: "T", Links: []opdsLink{
		{Rel: relAcquisitionOpen, Href: "/b.epub", Type: "application/epub+zip"},
	}}
	if got := entryID(noID); got != "/b.epub" {
		t.Errorf("entryID = %q, want the acquisition href", got)
	}
	titleOnly := &opdsEntry{Title: "Just A Title"}
	if got := entryID(titleOnly); got != "Just A Title" {
		t.Errorf("entryID = %q, want the title", got)
	}
}

func TestEntrySignalFingerprintDetectsChange(t *testing.T) {
	base := &opdsEntry{
		ID: "urn:book:1", Title: "Book", Updated: "2024-01-01T00:00:00Z",
		Links: []opdsLink{{Rel: relAcquisitionOpen, Href: "/b.epub", Type: "application/epub+zip"}},
	}
	same := *base
	if entrySignalFingerprint(base) != entrySignalFingerprint(&same) {
		t.Fatal("identical entries must fingerprint the same")
	}

	changedHref := *base
	changedHref.Links = []opdsLink{{Rel: relAcquisitionOpen, Href: "/b-v2.epub", Type: "application/epub+zip"}}
	if entrySignalFingerprint(base) == entrySignalFingerprint(&changedHref) {
		t.Fatal("a changed acquisition href must change the fingerprint")
	}

	changedTitle := *base
	changedTitle.Title = "Renamed"
	if entrySignalFingerprint(base) == entrySignalFingerprint(&changedTitle) {
		t.Fatal("a changed title must change the fingerprint")
	}
}

func TestSanitizeFileName(t *testing.T) {
	got := sanitizeFileName("A/B: A Novel\n")
	if strings.ContainsAny(got, `/\:`+"\n") {
		t.Fatalf("sanitizeFileName left hostile characters: %q", got)
	}
	if sanitizeFileName("") != "untitled" {
		t.Fatalf("empty title should become untitled, got %q", sanitizeFileName(""))
	}
}

func TestHostKey(t *testing.T) {
	tests := []struct{ raw, want string }{
		{"https://catalog.example.com/opds", "catalog.example.com:443"},
		{"http://catalog.example.com/opds", "catalog.example.com:80"},
		{"https://catalog.example.com:8443/opds", "catalog.example.com:8443"},
		{"https://CATALOG.Example.COM/opds", "catalog.example.com:443"},
	}
	for _, tc := range tests {
		u := mustParseURL(t, tc.raw)
		if got := hostKey(u); got != tc.want {
			t.Errorf("hostKey(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
