// Package opds implements the OPDS (Open Publication Distribution System)
// data source connector for WeKnora.
//
// OPDS is an Atom-based catalog format used by e-book libraries (Calibre-Web,
// Kavita, Komga, COPS, Project Gutenberg, Standard Ebooks, …). A catalog is a
// tree of feeds:
//
//   - a *navigation feed* lists sections; its entries link to other feeds;
//   - an *acquisition feed* lists books; each entry carries an "acquisition
//     link" pointing at a downloadable file (EPUB, PDF, …).
//
// This connector enumerates a configured catalog URL plus one level of its
// sections, and syncs each acquisition entry by downloading its file and
// handing the bytes to the knowledge import pipeline.
//
// Parsing is done with encoding/xml rather than the gofeed library used by the
// RSS connector: gofeed's Atom translator flattens entry links, keeping only
// rel "" / "alternate" / "self", which discards the OPDS acquisition rels and
// the link's MIME type. Those two attributes are exactly what identifies a
// book and its format, so we parse the Atom document directly.
package opds

import (
	"encoding/xml"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// OPDS relation values (OPDS 1.2).
const (
	relAcquisition         = "http://opds-spec.org/acquisition"
	relAcquisitionOpen     = "http://opds-spec.org/acquisition/open-access"
	relAcquisitionBorrow   = "http://opds-spec.org/acquisition/borrow"
	relAcquisitionBuy      = "http://opds-spec.org/acquisition/buy"
	relAcquisitionSample   = "http://opds-spec.org/acquisition/sample"
	relAcquisitionPreview  = "http://opds-spec.org/acquisition/preview"
	relAcquisitionSubscr   = "http://opds-spec.org/acquisition/subscribe"
	relSubsection          = "subsection"
	relNext                = "next"
	relAlternate           = "alternate"
	relSelf                = "self"
	opdsCatalogTypePrefix  = "application/atom+xml;profile=opds-catalog"
	opdsNavigationKind     = "kind=navigation"
	opdsAcquisitionKindStr = "kind=acquisition"
)

// feedKind distinguishes the two OPDS feed flavours.
type feedKind int

const (
	// kindUnknown means the document parsed as a feed but we could not tell
	// whether it lists books or sections. Treated as navigation so its
	// children are still explored.
	kindUnknown feedKind = iota
	kindNavigation
	kindAcquisition
)

func (k feedKind) String() string {
	switch k {
	case kindNavigation:
		return "navigation"
	case kindAcquisition:
		return "acquisition"
	default:
		return "unknown"
	}
}

// opdsLink is an Atom <link> element. All four attributes we care about are
// captured, which is the reason this package does not use gofeed.
type opdsLink struct {
	Rel   string `xml:"rel,attr"`
	Href  string `xml:"href,attr"`
	Type  string `xml:"type,attr"`
	Title string `xml:"title,attr"`
}

// opdsAuthor is an Atom <author> element.
type opdsAuthor struct {
	Name string `xml:"name"`
}

// opdsCategory is an Atom <category> element.
type opdsCategory struct {
	Term  string `xml:"term,attr"`
	Label string `xml:"label,attr"`
}

// opdsEntry is an Atom <entry>. In a navigation feed it describes a section;
// in an acquisition feed it describes a book.
type opdsEntry struct {
	ID        string         `xml:"id"`
	Title     string         `xml:"title"`
	Updated   string         `xml:"updated"`
	Published string         `xml:"published"`
	Summary   string         `xml:"summary"`
	Content   string         `xml:"content"`
	Authors   []opdsAuthor   `xml:"author"`
	Links     []opdsLink     `xml:"link"`
	Category  []opdsCategory `xml:"category"`
}

// opdsFeed is the root Atom <feed> document.
type opdsFeed struct {
	XMLName xml.Name    `xml:"feed"`
	ID      string      `xml:"id"`
	Title   string      `xml:"title"`
	Updated string      `xml:"updated"`
	Links   []opdsLink  `xml:"link"`
	Entries []opdsEntry `xml:"entry"`
}

// parseFeed decodes an OPDS/Atom document. A non-Atom body (for example an
// OPDS 2.0 JSON catalog) is rejected with a clear error rather than silently
// yielding an empty feed, so the sync fails loudly instead of importing
// nothing.
func parseFeed(data []byte) (*opdsFeed, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty feed body")
	}
	var feed opdsFeed
	if err := xml.Unmarshal(data, &feed); err != nil {
		return nil, fmt.Errorf("parse OPDS/Atom feed: %w", err)
	}
	if feed.XMLName.Local != "feed" {
		return nil, fmt.Errorf("not an OPDS/Atom feed (root element %q)", feed.XMLName.Local)
	}
	return &feed, nil
}

// feedKindOf classifies a feed. The authoritative signal is the kind= parameter
// on the feed's own rel="self" (or rel="alternate") catalog link; when that is
// absent we fall back to inspecting entries, since real-world catalogs are not
// always strict about advertising the kind.
func feedKindOf(feed *opdsFeed) feedKind {
	if feed == nil {
		return kindUnknown
	}
	for _, l := range feed.Links {
		if l.Rel != relSelf && l.Rel != relAlternate {
			continue
		}
		if !strings.HasPrefix(l.Type, opdsCatalogTypePrefix) {
			continue
		}
		switch {
		case strings.Contains(l.Type, opdsNavigationKind):
			return kindNavigation
		case strings.Contains(l.Type, opdsAcquisitionKindStr):
			return kindAcquisition
		}
	}

	// Fall back to the entries: an acquisition link means books, a subsection
	// link means sections.
	var sawNavigation, sawAcquisition bool
	for i := range feed.Entries {
		if acquisitionLink(&feed.Entries[i]) != nil {
			sawAcquisition = true
		}
		if navigationLink(&feed.Entries[i]) != nil {
			sawNavigation = true
		}
	}
	switch {
	case sawAcquisition:
		return kindAcquisition
	case sawNavigation:
		return kindNavigation
	default:
		return kindUnknown
	}
}

// acquisitionRels lists the acquisition rels we are willing to download, in
// preference order. buy/subscribe are paywalled and sample/preview are partial,
// so they are deliberately excluded.
var acquisitionRels = []string{
	relAcquisitionOpen,
	relAcquisition,
	relAcquisitionBorrow,
}

// acquisitionLink returns the best downloadable link for an entry, or nil when
// the entry offers nothing we can fetch. Among links of the highest-priority
// rel, one whose MIME type maps to a supported import format is preferred over
// one we cannot parse.
func acquisitionLink(entry *opdsEntry) *opdsLink {
	if entry == nil {
		return nil
	}
	for _, rel := range acquisitionRels {
		var fallback *opdsLink
		for i := range entry.Links {
			l := &entry.Links[i]
			if !strings.EqualFold(strings.TrimSpace(l.Rel), rel) || l.Href == "" {
				continue
			}
			if ext := extensionForLink(l); ext != "" && isSupportedAcquisitionExtension(ext) {
				return l
			}
			if fallback == nil {
				fallback = l
			}
		}
		if fallback != nil {
			return fallback
		}
	}
	return nil
}

// navigationLink returns the link that leads to a child feed, or nil when the
// entry is not a section.
func navigationLink(entry *opdsEntry) *opdsLink {
	if entry == nil {
		return nil
	}
	// rel="subsection" is the OPDS-defined navigation rel.
	for i := range entry.Links {
		if strings.EqualFold(strings.TrimSpace(entry.Links[i].Rel), relSubsection) &&
			entry.Links[i].Href != "" {
			return &entry.Links[i]
		}
	}
	// Some catalogs omit the rel but declare the OPDS catalog media type.
	for i := range entry.Links {
		if strings.HasPrefix(entry.Links[i].Type, opdsCatalogTypePrefix) &&
			entry.Links[i].Href != "" {
			return &entry.Links[i]
		}
	}
	return nil
}

// nextLink returns the rel="next" pagination link, or nil.
func nextLink(feed *opdsFeed) *opdsLink {
	if feed == nil {
		return nil
	}
	for i := range feed.Links {
		if strings.EqualFold(strings.TrimSpace(feed.Links[i].Rel), relNext) &&
			feed.Links[i].Href != "" {
			return &feed.Links[i]
		}
	}
	return nil
}

// entryID returns a stable identifier for an entry, preferring the Atom <id>
// and falling back to the acquisition href and then the title.
func entryID(entry *opdsEntry) string {
	if entry == nil {
		return ""
	}
	if id := strings.TrimSpace(entry.ID); id != "" {
		return id
	}
	if l := acquisitionLink(entry); l != nil {
		return l.Href
	}
	if l := navigationLink(entry); l != nil {
		return l.Href
	}
	return strings.TrimSpace(entry.Title)
}

// entryAuthor returns the first author name, if any.
func entryAuthor(entry *opdsEntry) string {
	if entry == nil {
		return ""
	}
	for _, a := range entry.Authors {
		if name := strings.TrimSpace(a.Name); name != "" {
			return name
		}
	}
	return ""
}

// mimeToExtension maps the MIME types an OPDS catalog realistically serves to
// the file extension the knowledge import pipeline expects.
//
// OPDS is a generic distribution protocol, not an EPUB format: catalogs also
// distribute audiobooks, comics, office documents and plain text. The map
// therefore covers every MIME type whose extension the pipeline can parse
// (see types.SupportedImportFileExtensions), so a catalog entry is never
// skipped merely because we failed to recognise its type.
var mimeToExtension = map[string]string{
	// E-books
	"application/epub+zip":           "epub",
	"application/x-mobipocket-ebook": "mobi",
	"application/vnd.amazon.ebook":   "azw",
	"application/x-mobipocket":       "mobi",
	// Documents
	"application/pdf": "pdf",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": "docx",
	"application/msword": "doc",
	// Spreadsheets / slides
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": "xlsx",
	"application/vnd.ms-excel": "xls",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": "pptx",
	"application/vnd.ms-powerpoint":                                             "ppt",
	// Markup / plain text
	"text/html":                 "html",
	"application/xhtml+xml":     "html",
	"application/x-mimearchive": "mhtml",
	"multipart/related":         "mhtml",
	"text/plain":                "txt",
	"text/markdown":             "md",
	"application/xml":           "txt",
	"text/xml":                  "txt",
	// Data
	"application/json": "json",
	"text/csv":         "csv",
	// Mind maps
	"application/x-xmind": "xmind",
	// Images
	"image/jpeg": "jpg",
	"image/png":  "png",
	"image/gif":  "gif",
	// Audio (audiobook catalogs)
	"audio/mpeg":   "mp3",
	"audio/mp4":    "m4a",
	"audio/x-m4a":  "m4a",
	"audio/wav":    "wav",
	"audio/x-wav":  "wav",
	"audio/flac":   "flac",
	"audio/x-flac": "flac",
	"audio/ogg":    "ogg",
}

// extensionForLink resolves a link's file extension from its MIME type,
// falling back to the URL path.
func extensionForLink(l *opdsLink) string {
	if l == nil {
		return ""
	}
	mime := strings.ToLower(strings.TrimSpace(strings.Split(l.Type, ";")[0]))
	if ext, ok := mimeToExtension[mime]; ok {
		return ext
	}
	return extensionFromURL(l.Href)
}

// extensionFromURL extracts a lowercased extension from a URL path, ignoring
// query strings and fragments.
func extensionFromURL(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(u.Path), "."))
	if len(ext) > 8 || strings.ContainsAny(ext, "/\\") {
		return ""
	}
	return ext
}

// isSupportedAcquisitionExtension reports whether the knowledge import pipeline
// can parse this format. It delegates to the canonical list in internal/types
// rather than keeping a connector-local copy: OPDS serves many formats, and a
// narrower local list would silently drop formats WeKnora can in fact ingest
// (the drift #2447 fixed for upload vs URL import).
//
// Checking here, before the download, means an entry we cannot parse is skipped
// cheaply instead of being fetched (up to GetMaxFileSize) and then rejected by
// the pipeline. Formats genuinely outside the set — cbz, cbr, djvu, azw3, fb2 —
// are skipped rather than failing the sync.
func isSupportedAcquisitionExtension(ext string) bool {
	return types.IsSupportedImportExtension(ext)
}

// resolveHref resolves a possibly-relative href against the feed's base URL.
func resolveHref(base *url.URL, href string) (string, error) {
	href = strings.TrimSpace(href)
	if href == "" {
		return "", fmt.Errorf("empty href")
	}
	ref, err := url.Parse(href)
	if err != nil {
		return "", fmt.Errorf("parse href %q: %w", href, err)
	}
	if base == nil {
		if ref.IsAbs() {
			return ref.String(), nil
		}
		return "", fmt.Errorf("relative href %q with no base URL", href)
	}
	return base.ResolveReference(ref).String(), nil
}
