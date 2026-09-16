package confluence

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

const testBaseURL = "https://confluence.test/wiki"

func newImageTestClient(rt roundTripper) *client {
	return &client{
		cfg:  config{baseURL: testBaseURL, username: "reader", secret: "secret"},
		http: &http.Client{Transport: rt},
	}
}

func imageResp(status int, contentType string, body []byte) *http.Response {
	header := make(http.Header)
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

func redirectResp(location string) *http.Response {
	header := make(http.Header)
	header.Set("Location", location)
	return &http.Response{
		StatusCode: http.StatusFound,
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(nil)),
	}
}

// reqLog records outbound requests. Image downloads run concurrently, so every
// access is mutex-guarded.
type reqLog struct {
	mu   sync.Mutex
	reqs []reqRecord
}

type reqRecord struct {
	host    string
	path    string
	hasAuth bool
}

func newReqLog() *reqLog { return &reqLog{} }

func (l *reqLog) add(req *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reqs = append(l.reqs, reqRecord{
		host:    req.URL.Host,
		path:    req.URL.Path,
		hasAuth: req.Header.Get("Authorization") != "",
	})
}

func (l *reqLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.reqs)
}

func (l *reqLog) all() []reqRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]reqRecord(nil), l.reqs...)
}

func (l *reqLog) hasHost(host string) bool {
	for _, r := range l.all() {
		if r.host == host {
			return true
		}
	}
	return false
}

func (l *reqLog) hasPath(path string) bool {
	for _, r := range l.all() {
		if r.path == path {
			return true
		}
	}
	return false
}

func (l *reqLog) anyWithoutAuth() bool {
	for _, r := range l.all() {
		if !r.hasAuth {
			return true
		}
	}
	return false
}

var testPNG = []byte("private-png-bytes")

func testDataURI(body []byte) string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(body)
}

func TestAssetResolverKeepsPublicURL(t *testing.T) {
	log := newReqLog()
	rt := roundTripper(func(req *http.Request) (*http.Response, error) {
		log.add(req)
		return imageResp(http.StatusOK, "image/png", testPNG), nil
	})
	in := `<p>hi</p><img src="https://cdn.example.com/a.png">`
	out := newAssetResolver(newImageTestClient(rt)).Resolve(context.Background(), in)
	if out != in {
		t.Fatalf("public URL was modified:\n%s", out)
	}
	if log.count() != 0 {
		t.Fatalf("public image was downloaded: %v", log.all())
	}
}

func TestAssetResolverKeepsBase64(t *testing.T) {
	log := newReqLog()
	rt := roundTripper(func(req *http.Request) (*http.Response, error) {
		log.add(req)
		return imageResp(http.StatusOK, "image/png", testPNG), nil
	})
	in := `<img src="data:image/png;base64,QUJDRA==">`
	out := newAssetResolver(newImageTestClient(rt)).Resolve(context.Background(), in)
	if out != in {
		t.Fatalf("base64 image was modified:\n%s", out)
	}
	if log.count() != 0 {
		t.Fatalf("base64 image triggered a download: %v", log.all())
	}
}

func TestAssetResolverKeepsNonHTTPScheme(t *testing.T) {
	log := newReqLog()
	rt := roundTripper(func(req *http.Request) (*http.Response, error) {
		log.add(req)
		return imageResp(http.StatusOK, "image/png", testPNG), nil
	})
	in := `<img src="file:///etc/passwd"><img src="javascript:void(0)">`
	out := newAssetResolver(newImageTestClient(rt)).Resolve(context.Background(), in)
	if out != in {
		t.Fatalf("non-http scheme was modified:\n%s", out)
	}
	if log.count() != 0 {
		t.Fatalf("non-http scheme triggered a download: %v", log.all())
	}
}

func TestAssetResolverInlinesRelativeConfluenceImage(t *testing.T) {
	log := newReqLog()
	rt := roundTripper(func(req *http.Request) (*http.Response, error) {
		log.add(req)
		return imageResp(http.StatusOK, "image/png", testPNG), nil
	})
	in := `<img src="/wiki/download/attachments/123/a.png">`
	out := newAssetResolver(newImageTestClient(rt)).Resolve(context.Background(), in)

	want := testDataURI(testPNG)
	if !strings.Contains(out, `src="`+want+`"`) {
		t.Fatalf("relative image not inlined as data URI:\n%s", out)
	}
	if strings.Contains(out, "/wiki/download/attachments") {
		t.Fatalf("original private URL survived:\n%s", out)
	}
	reqs := log.all()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 download, got %d: %v", len(reqs), reqs)
	}
	if reqs[0].host != "confluence.test" || !reqs[0].hasAuth {
		t.Fatalf("download missing credentials or wrong host: %+v", reqs[0])
	}
}

func TestAssetResolverInlinesSameOriginPrivateImage(t *testing.T) {
	log := newReqLog()
	rt := roundTripper(func(req *http.Request) (*http.Response, error) {
		log.add(req)
		return imageResp(http.StatusOK, "image/png", testPNG), nil
	})
	in := `<img src="https://confluence.test/wiki/download/attachments/123/a.png" alt="x"/>`
	out := newAssetResolver(newImageTestClient(rt)).Resolve(context.Background(), in)

	if !strings.Contains(out, `src="`+testDataURI(testPNG)+`"`) {
		t.Fatalf("same-origin image not inlined:\n%s", out)
	}
	// The rest of the tag must be preserved.
	if !strings.Contains(out, `alt="x"`) {
		t.Fatalf("sibling attribute lost:\n%s", out)
	}
	if log.count() != 1 {
		t.Fatalf("expected 1 download, got %d", log.count())
	}
}

func TestAssetResolverDoesNotSendCredentialsCrossOrigin(t *testing.T) {
	log := newReqLog()
	rt := roundTripper(func(req *http.Request) (*http.Response, error) {
		log.add(req)
		return imageResp(http.StatusOK, "image/png", testPNG), nil
	})
	in := `<img src="https://cdn.example.com/a.png">` +
		`<img src="/wiki/download/attachments/1/a.png">`
	out := newAssetResolver(newImageTestClient(rt)).Resolve(context.Background(), in)

	if log.hasHost("cdn.example.com") {
		t.Fatalf("external host received a request: %v", log.all())
	}
	if !strings.Contains(out, "https://cdn.example.com/a.png") {
		t.Fatalf("external URL was modified:\n%s", out)
	}
	if !strings.Contains(out, testDataURI(testPNG)) {
		t.Fatalf("same-origin image was not inlined:\n%s", out)
	}
	if log.count() != 1 || log.anyWithoutAuth() {
		t.Fatalf("expected exactly one authenticated same-origin download: %v", log.all())
	}
}

func TestAssetResolverRejectsCrossOriginRedirect(t *testing.T) {
	log := newReqLog()
	rt := roundTripper(func(req *http.Request) (*http.Response, error) {
		log.add(req)
		if req.URL.Host == "confluence.test" {
			return redirectResp("https://evil.example.com/a.png"), nil
		}
		return imageResp(http.StatusOK, "image/png", testPNG), nil
	})
	in := `<img src="/wiki/download/attachments/1/a.png">`
	out := newAssetResolver(newImageTestClient(rt)).Resolve(context.Background(), in)

	if out != in {
		t.Fatalf("cross-origin redirect was followed and inlined:\n%s", out)
	}
	if log.hasHost("evil.example.com") {
		t.Fatalf("credentials could have leaked to evil host: %v", log.all())
	}
	if log.count() != 1 {
		t.Fatalf("expected only the original same-origin request, got %d: %v", log.count(), log.all())
	}
}

func TestAssetResolverRejectsSameOriginRedirectOutsideContextPath(t *testing.T) {
	log := newReqLog()
	rt := roundTripper(func(req *http.Request) (*http.Response, error) {
		log.add(req)
		// The base URL is https://confluence.test/wiki. A redirect to another app on
		// the same host stays same-origin but escapes the /wiki context path, and Go
		// keeps sending Basic Auth on a same-host hop. It must be refused before the
		// credentialed request is ever sent, not merely discarded afterward.
		if strings.HasPrefix(req.URL.Path, "/wiki/") {
			return redirectResp("https://confluence.test/other-app/leak.png"), nil
		}
		return imageResp(http.StatusOK, "image/png", testPNG), nil
	})
	in := `<img src="/wiki/download/attachments/1/a.png">`
	out := newAssetResolver(newImageTestClient(rt)).Resolve(context.Background(), in)

	if out != in {
		t.Fatalf("same-host redirect outside the context path was followed and inlined:\n%s", out)
	}
	if log.hasPath("/other-app/leak.png") {
		t.Fatalf("credentials followed a redirect outside the context path: %v", log.all())
	}
	if log.count() != 1 {
		t.Fatalf("expected only the original same-origin request, got %d: %v", log.count(), log.all())
	}
}

func TestAssetResolverRejectsOversizedImage(t *testing.T) {
	log := newReqLog()
	big := bytes.Repeat([]byte("a"), int(maxImageBytes)+1)
	rt := roundTripper(func(req *http.Request) (*http.Response, error) {
		log.add(req)
		return imageResp(http.StatusOK, "image/png", big), nil
	})
	in := `<img src="/wiki/download/attachments/1/big.png">`
	out := newAssetResolver(newImageTestClient(rt)).Resolve(context.Background(), in)

	if out != in {
		t.Fatalf("oversized image was inlined:\n%s", out)
	}
	if strings.Contains(out, "data:image") {
		t.Fatalf("oversized image produced a data URI")
	}
}

func TestAssetResolverRejectsNonImageResponse(t *testing.T) {
	rt := roundTripper(func(req *http.Request) (*http.Response, error) {
		return imageResp(http.StatusOK, "text/html; charset=utf-8", []byte("<html>login required</html>")), nil
	})
	in := `<img src="/wiki/download/attachments/1/a.png">`
	out := newAssetResolver(newImageTestClient(rt)).Resolve(context.Background(), in)

	if out != in {
		t.Fatalf("login HTML was inlined as an image:\n%s", out)
	}
}

func TestAssetResolverKeepsOriginalURLOnDownloadFailure(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			rt := roundTripper(func(req *http.Request) (*http.Response, error) {
				return imageResp(status, "image/png", testPNG), nil
			})
			in := `<img src="/wiki/download/attachments/1/a.png">`
			out := newAssetResolver(newImageTestClient(rt)).Resolve(context.Background(), in)
			if out != in {
				t.Fatalf("status %d modified the document:\n%s", status, out)
			}
		})
	}
}

func TestAssetResolverDeduplicatesSamePageImages(t *testing.T) {
	log := newReqLog()
	rt := roundTripper(func(req *http.Request) (*http.Response, error) {
		log.add(req)
		return imageResp(http.StatusOK, "image/png", testPNG), nil
	})
	src := `<img src="/wiki/download/attachments/1/a.png">`
	in := strings.Repeat(src, 5)
	out := newAssetResolver(newImageTestClient(rt)).Resolve(context.Background(), in)

	if got := strings.Count(out, "data:image/png;base64,"); got != 5 {
		t.Fatalf("expected 5 inlined copies, got %d:\n%s", got, out)
	}
	if log.count() != 1 {
		t.Fatalf("duplicate src downloaded %d times, want 1", log.count())
	}
}

func TestAssetResolverRespectsPerPageImageCap(t *testing.T) {
	log := newReqLog()
	rt := roundTripper(func(req *http.Request) (*http.Response, error) {
		log.add(req)
		return imageResp(http.StatusOK, "image/png", testPNG), nil
	})
	var sb strings.Builder
	total := maxPageImages + 5
	for i := 0; i < total; i++ {
		sb.WriteString(`<img src="/wiki/download/attachments/1/img`)
		sb.WriteString(strings.Repeat("x", i+1)) // unique path per image
		sb.WriteString(`.png">`)
	}
	out := newAssetResolver(newImageTestClient(rt)).Resolve(context.Background(), sb.String())

	if got := strings.Count(out, "data:image/png;base64,"); got != maxPageImages {
		t.Fatalf("inlined %d images, want cap %d", got, maxPageImages)
	}
	if log.count() != maxPageImages {
		t.Fatalf("downloaded %d images, want cap %d", log.count(), maxPageImages)
	}
	if got := strings.Count(out, "/wiki/download/attachments"); got != 5 {
		t.Fatalf("expected 5 images past the cap to keep original src, got %d", got)
	}
}

func TestAssetResolverCapsOccurrencesNotUniqueDownloads(t *testing.T) {
	log := newReqLog()
	rt := roundTripper(func(req *http.Request) (*http.Response, error) {
		log.add(req)
		return imageResp(http.StatusOK, "image/png", testPNG), nil
	})
	// Two unique URLs but 35 occurrences. Counting the cap by unique URL would
	// download cold.png too and inline 30 + 5 data URIs, overflowing the downstream
	// per-page limit that counts every occurrence. The budget must be occurrences.
	hot := `<img src="/wiki/download/attachments/1/hot.png">`
	cold := `<img src="/wiki/download/attachments/1/cold.png">`
	in := strings.Repeat(hot, maxPageImages) + strings.Repeat(cold, 5)
	out := newAssetResolver(newImageTestClient(rt)).Resolve(context.Background(), in)

	if got := strings.Count(out, "data:image/png;base64,"); got != maxPageImages {
		t.Fatalf("inlined %d data URIs, want cap %d", got, maxPageImages)
	}
	if got := strings.Count(out, "/wiki/download/attachments/1/cold.png"); got != 5 {
		t.Fatalf("expected the 5 images past the cap to keep original src, got %d", got)
	}
	// The repeated image downloads once; the image past the budget never downloads.
	if log.count() != 1 {
		t.Fatalf("downloaded %d images, want 1 (dedup + no over-budget fetch): %v", log.count(), log.all())
	}
	if log.hasPath("/wiki/download/attachments/1/cold.png") {
		t.Fatalf("image past the occurrence cap was downloaded: %v", log.all())
	}
}

func TestAssetResolverSharesCapWithExistingDataURIImages(t *testing.T) {
	log := newReqLog()
	rt := roundTripper(func(req *http.Request) (*http.Response, error) {
		log.add(req)
		return imageResp(http.StatusOK, "image/png", testPNG), nil
	})
	// Data URI images already in the page share the downstream 30-image budget with
	// the ones we generate. With 10 existing, only 20 of the 25 private images may
	// be inlined; the rest keep their original URL instead of overflowing the cap.
	var sb strings.Builder
	for i := 0; i < 10; i++ {
		sb.WriteString(`<img src="data:image/gif;base64,EXISTING">`)
	}
	for i := 0; i < 25; i++ {
		sb.WriteString(`<img src="/wiki/download/attachments/1/img`)
		sb.WriteString(strings.Repeat("x", i+1)) // unique path per image
		sb.WriteString(`.png">`)
	}
	out := newAssetResolver(newImageTestClient(rt)).Resolve(context.Background(), sb.String())

	// The 10 pre-existing data URIs are preserved untouched.
	if got := strings.Count(out, "data:image/gif;base64,EXISTING"); got != 10 {
		t.Fatalf("preserved %d existing data URIs, want 10", got)
	}
	// Exactly 20 private images inlined: 30 shared budget - 10 already present.
	if got := strings.Count(out, testDataURI(testPNG)); got != 20 {
		t.Fatalf("inlined %d private images, want 20", got)
	}
	// The 5 private images past the shared budget keep their original URL.
	if got := strings.Count(out, "/wiki/download/attachments"); got != 5 {
		t.Fatalf("expected 5 private images past the cap to keep original src, got %d", got)
	}
	// Only the 20 inlinable private images are downloaded.
	if log.count() != 20 {
		t.Fatalf("downloaded %d images, want 20: %v", log.count(), log.all())
	}
}

func TestAssetResolverIgnoresDataSrcAttribute(t *testing.T) {
	log := newReqLog()
	rt := roundTripper(func(req *http.Request) (*http.Response, error) {
		log.add(req)
		return imageResp(http.StatusOK, "image/png", testPNG), nil
	})
	// data-src must not be mistaken for src; there is no real src here.
	in := `<img data-src="/wiki/download/attachments/1/a.png">`
	out := newAssetResolver(newImageTestClient(rt)).Resolve(context.Background(), in)
	if out != in {
		t.Fatalf("data-src was treated as src:\n%s", out)
	}
	if log.count() != 0 {
		t.Fatalf("data-src triggered a download: %v", log.all())
	}
}

func TestAssetResolverToleratesGreaterThanInAttribute(t *testing.T) {
	log := newReqLog()
	rt := roundTripper(func(req *http.Request) (*http.Response, error) {
		log.add(req)
		return imageResp(http.StatusOK, "image/png", testPNG), nil
	})
	in := `<img alt="a > b" src="/wiki/download/attachments/1/a.png">`
	out := newAssetResolver(newImageTestClient(rt)).Resolve(context.Background(), in)
	if !strings.Contains(out, testDataURI(testPNG)) {
		t.Fatalf("image with '>' in alt was not inlined:\n%s", out)
	}
	if !strings.Contains(out, `alt="a > b"`) {
		t.Fatalf("alt attribute was corrupted:\n%s", out)
	}
}

func TestAssetResolverUnescapesAmpersandInQuery(t *testing.T) {
	var gotPath string
	var gotQuery string
	rt := roundTripper(func(req *http.Request) (*http.Response, error) {
		gotPath = req.URL.Path
		gotQuery = req.URL.RawQuery
		return imageResp(http.StatusOK, "image/png", testPNG), nil
	})
	in := `<img src="/wiki/download/attachments/1/a.png?api=v2&amp;v=3">`
	out := newAssetResolver(newImageTestClient(rt)).Resolve(context.Background(), in)
	if !strings.Contains(out, "data:image/png;base64,") {
		t.Fatalf("image with &amp; query was not inlined:\n%s", out)
	}
	if gotPath != "/wiki/download/attachments/1/a.png" || gotQuery != "api=v2&v=3" {
		t.Fatalf("query not unescaped: path=%q query=%q", gotPath, gotQuery)
	}
}

func TestDownloadPrivateImageLimitsBodySize(t *testing.T) {
	big := bytes.Repeat([]byte("a"), int(maxImageBytes)+1)
	rt := roundTripper(func(req *http.Request) (*http.Response, error) {
		return imageResp(http.StatusOK, "image/png", big), nil
	})
	c := newImageTestClient(rt)
	_, _, err := c.downloadImage(context.Background(), "https://confluence.test/wiki/download/attachments/1/big.png")
	if !errors.Is(err, errImageTooLarge) {
		t.Fatalf("downloadImage() error = %v, want errImageTooLarge", err)
	}
}

func TestDownloadPrivateImageValidatesContentType(t *testing.T) {
	rt := roundTripper(func(req *http.Request) (*http.Response, error) {
		return imageResp(http.StatusOK, "application/json", []byte(`{"error":"unauthorized"}`)), nil
	})
	c := newImageTestClient(rt)
	_, _, err := c.downloadImage(context.Background(), "https://confluence.test/wiki/download/attachments/1/a.png")
	if !errors.Is(err, errImageNonImageContent) {
		t.Fatalf("downloadImage() error = %v, want errImageNonImageContent", err)
	}
}

func TestDownloadPrivateImageSniffsWhenContentTypeMissing(t *testing.T) {
	png := append([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, bytes.Repeat([]byte{0}, 64)...)
	rt := roundTripper(func(req *http.Request) (*http.Response, error) {
		return imageResp(http.StatusOK, "", png), nil
	})
	c := newImageTestClient(rt)
	body, mime, err := c.downloadImage(context.Background(), "https://confluence.test/wiki/download/attachments/1/a.png")
	if err != nil {
		t.Fatalf("downloadImage() error = %v", err)
	}
	if mime != "image/png" {
		t.Fatalf("sniffed mime = %q, want image/png", mime)
	}
	if !bytes.Equal(body, png) {
		t.Fatalf("body mismatch: %d bytes", len(body))
	}
}

func TestDownloadPrivateImageRejectsCrossOriginRedirect(t *testing.T) {
	rt := roundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "confluence.test" {
			return redirectResp("https://evil.example.com/a.png"), nil
		}
		return imageResp(http.StatusOK, "image/png", testPNG), nil
	})
	c := newImageTestClient(rt)
	_, _, err := c.downloadImage(context.Background(), "https://confluence.test/wiki/download/attachments/1/a.png")
	if !errors.Is(err, errImageRedirectOffOrigin) {
		t.Fatalf("downloadImage() error = %v, want errImageRedirectOffOrigin", err)
	}
}

// TestFetchStreamInlinesPrivateImageAsDataURI is the connector integration test:
// a body.view containing a same-origin private image flows through FetchStream
// and the emitted FetchedItem.Content carries an inline data URI (which the
// downstream docparser pipeline later persists and rewrites to resource://).
func TestFetchStreamInlinesPrivateImageAsDataURI(t *testing.T) {
	api := &streamAPI{
		pages:    []streamPage{{id: "p1", title: "Page", version: 1}},
		bodyHTML: map[string]string{"p1": `<p>hi</p><img src="/wiki/download/attachments/p1/a.png">`},
	}
	h := &captureHandler{}
	if _, err := newStreamConnector(api).FetchStream(context.Background(), streamConfig(), nil, h); err != nil {
		t.Fatal(err)
	}
	if len(h.items) != 1 {
		t.Fatalf("emitted %d items, want 1", len(h.items))
	}
	content := string(h.items[0].Content)
	if !strings.Contains(content, "data:image/png;base64,") {
		t.Fatalf("private image was not inlined into Content:\n%s", content)
	}
	if strings.Contains(content, "/wiki/download/attachments") {
		t.Fatalf("original private Confluence URL leaked into Content:\n%s", content)
	}
	if api.imageCalls != 1 {
		t.Fatalf("image download calls = %d, want 1", api.imageCalls)
	}
}
