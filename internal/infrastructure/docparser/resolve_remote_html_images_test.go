package docparser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// remoteImageServer serves a valid PNG for every request and counts the hits so
// that budget and dedup behaviour can be asserted directly.
func remoteImageServer(t *testing.T) (*httptest.Server, *int64) {
	t.Helper()
	var hits int64
	png := createTestPNG(200, 200)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(png)
	}))
	t.Cleanup(ts.Close)
	return ts, &hits
}

func allowLocalhost(t *testing.T) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	secutils.ResetSSRFWhitelistForTest()
}

func resolve(t *testing.T, markdown string) (string, []StoredImage, *mockFileService) {
	t.Helper()
	fSvc := &mockFileService{}
	updated, images, err := NewImageResolver().ResolveRemoteImages(
		context.Background(), markdown, fSvc, 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return updated, images, fSvc
}

func TestResolveRemoteImages_HTMLTagRewritesSrcAndKeepsTag(t *testing.T) {
	allowLocalhost(t)
	ts, _ := remoteImageServer(t)

	markdown := fmt.Sprintf(
		`<p><img class="screenshot" src="%s/a.png" alt="login screen" width="600"></p>`, ts.URL)

	updated, images, _ := resolve(t, markdown)

	if len(images) != 1 {
		t.Fatalf("expected 1 stored image, got %d", len(images))
	}
	if strings.Contains(updated, ts.URL) {
		t.Errorf("original URL still present: %s", updated)
	}
	if !strings.Contains(updated, images[0].ServingURL) {
		t.Errorf("serving URL missing from output: %s", updated)
	}
	// The surrounding markup is the reason these documents use HTML at all.
	for _, want := range []string{`<img`, `class="screenshot"`, `alt="login screen"`, `width="600"`, `</p>`} {
		if !strings.Contains(updated, want) {
			t.Errorf("tag structure lost, %q missing from: %s", want, updated)
		}
	}
	if strings.Contains(updated, "![") {
		t.Errorf("tag was converted to markdown syntax: %s", updated)
	}
}

func TestResolveRemoteImages_MarkdownAndHTMLBothResolved(t *testing.T) {
	allowLocalhost(t)
	ts, _ := remoteImageServer(t)

	markdown := fmt.Sprintf("![md](%s/md.png)\n\n<img src=\"%s/html.png\">", ts.URL, ts.URL)

	updated, images, _ := resolve(t, markdown)

	if len(images) != 2 {
		t.Fatalf("expected 2 stored images, got %d", len(images))
	}
	if strings.Contains(updated, ts.URL) {
		t.Errorf("a remote URL survived: %s", updated)
	}
}

func TestResolveRemoteImages_IndependentBudgetPerSyntax(t *testing.T) {
	allowLocalhost(t)
	ts, _ := remoteImageServer(t)

	// Adding HTML support must not reduce how many Markdown images a document
	// already gets resolved: each syntax carries its own budget.
	var b strings.Builder
	for i := 0; i < maxRemoteImages; i++ {
		fmt.Fprintf(&b, "![md%d](%s/md%d.png)\n\n", i, ts.URL, i)
	}
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&b, "<img src=\"%s/html%d.png\">\n\n", ts.URL, i)
	}

	updated, images, _ := resolve(t, b.String())

	if len(images) != maxRemoteImages+5 {
		t.Fatalf("expected %d stored images, got %d", maxRemoteImages+5, len(images))
	}
	if strings.Contains(updated, ts.URL) {
		t.Errorf("a remote URL survived: %s", updated)
	}
}

func TestResolveRemoteImages_HTMLSrcNormalization(t *testing.T) {
	allowLocalhost(t)
	ts, _ := remoteImageServer(t)

	t.Run("uppercase scheme", func(t *testing.T) {
		upper := strings.Replace(ts.URL, "http://", "HTTP://", 1)
		updated, images, _ := resolve(t, fmt.Sprintf(`<img SRC="%s/up.png">`, upper))
		if len(images) != 1 {
			t.Fatalf("expected uppercase scheme to be fetched, got %d", len(images))
		}
		// The scheme is normalized because every fetcher downstream compares it
		// byte-for-byte; an image counted as resolved but unfetchable is worse
		// than one left alone.
		if strings.Contains(images[0].OriginalRef, "HTTP://") {
			t.Errorf("scheme was not normalized: %q", images[0].OriginalRef)
		}
		if strings.Contains(updated, "HTTP://") {
			t.Errorf("document still carries the uppercase scheme: %s", updated)
		}
	})

	t.Run("markdown uppercase scheme is left alone", func(t *testing.T) {
		// Markdown targets are handed through unchanged, so a document that
		// upstream leaves untouched stays untouched.
		upper := strings.Replace(ts.URL, "http://", "HTTP://", 1)
		markdown := fmt.Sprintf("![x](%s/md-up.png)", upper)
		updated, images, _ := resolve(t, markdown)
		if len(images) != 0 {
			t.Errorf("expected no markdown behaviour change, got %d image(s)", len(images))
		}
		if updated != markdown {
			t.Errorf("markdown content changed:\n got %q\nwant %q", updated, markdown)
		}
	})

	t.Run("padded src", func(t *testing.T) {
		_, images, _ := resolve(t, fmt.Sprintf(`<img src="  %s/pad.png  ">`, ts.URL))
		if len(images) != 1 {
			t.Fatalf("expected padded src to be fetched, got %d", len(images))
		}
	})

	t.Run("entity encoded query", func(t *testing.T) {
		var got string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.URL.RawQuery
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(createTestPNG(200, 200))
		}))
		defer srv.Close()

		_, images, _ := resolve(t, fmt.Sprintf(`<img src="%s/q.png?a=1&amp;b=2">`, srv.URL))
		if len(images) != 1 {
			t.Fatalf("expected 1 stored image, got %d", len(images))
		}
		if got != "a=1&b=2" {
			t.Errorf("entities were not decoded before the request: raw query %q", got)
		}
	})
}

func TestResolveRemoteImages_HTMLSrcFormsOutOfScope(t *testing.T) {
	allowLocalhost(t)
	ts, hits := remoteImageServer(t)

	cases := map[string]string{
		"unquoted src": fmt.Sprintf(`<img src=%s/unquoted.png>`, ts.URL),
		"srcset only":  fmt.Sprintf(`<img srcset="%s/set.png 2x">`, ts.URL),
		"relative src": `<img src="./images/local.png">`,
		"data uri src": `<img src="data:image/png;base64,AAAA">`,
		"provider src": `<img src="local://images/x.png">`,
		"non-img tag":  fmt.Sprintf(`<video src="%s/v.mp4">`, ts.URL),
	}

	for name, markdown := range cases {
		t.Run(name, func(t *testing.T) {
			before := atomic.LoadInt64(hits)
			updated, images, _ := resolve(t, markdown)
			if len(images) != 0 {
				t.Errorf("expected no stored images, got %d", len(images))
			}
			if updated != markdown {
				t.Errorf("content changed:\n got %q\nwant %q", updated, markdown)
			}
			if got := atomic.LoadInt64(hits) - before; got != 0 {
				t.Errorf("expected no fetch, got %d", got)
			}
		})
	}
}

func TestResolveRemoteImages_HyphenatedAttributeIsNotSrc(t *testing.T) {
	allowLocalhost(t)
	ts, _ := remoteImageServer(t)

	// A lazy-loading tag carries a placeholder in src and the real image in
	// data-src. Treating data-src as src would capture whichever came first and
	// leave the other unvisited, so only src counts.
	markdown := fmt.Sprintf(`<img data-src="/placeholder.svg" src="%s/real.png">`, ts.URL)

	updated, images, _ := resolve(t, markdown)

	if len(images) != 1 {
		t.Fatalf("expected the src image to be resolved, got %d", len(images))
	}
	if images[0].OriginalRef != ts.URL+"/real.png" {
		t.Errorf("expected src to be the fetched reference, got %s", images[0].OriginalRef)
	}
	if !strings.Contains(updated, `data-src="/placeholder.svg"`) {
		t.Errorf("data-src must be left alone: %s", updated)
	}
	if strings.Contains(updated, ts.URL) {
		t.Errorf("src was not rewritten: %s", updated)
	}
}

func TestResolveRemoteImages_HyphenatedAttributeOnlyIsNotResolved(t *testing.T) {
	allowLocalhost(t)
	ts, hits := remoteImageServer(t)

	// The other half of the same trade-off, pinned deliberately. Requiring
	// whitespace before src means a tag carrying only a hyphenated *-src is no
	// longer treated as an image at all — the previous boundary did resolve it.
	// Recovering that needs an attribute parser rather than a regex; capturing a
	// lazy-loading placeholder while the real src goes unvisited is the worse of
	// the two failures.
	for _, attr := range []string{"data-src", "ng-src", "data-original-src"} {
		t.Run(attr, func(t *testing.T) {
			before := atomic.LoadInt64(hits)
			markdown := fmt.Sprintf(`<img %s="%s/only.png">`, attr, ts.URL)

			updated, images, _ := resolve(t, markdown)

			if len(images) != 0 {
				t.Errorf("expected no stored images, got %d", len(images))
			}
			if updated != markdown {
				t.Errorf("content changed:\n got %q\nwant %q", updated, markdown)
			}
			if got := atomic.LoadInt64(hits) - before; got != 0 {
				t.Errorf("expected no fetch, got %d", got)
			}
		})
	}
}

func TestResolveRemoteImages_WhitelistedHostAgreesWithDocument(t *testing.T) {
	var hits int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		if got := r.URL.RawQuery; got != "a=1&b=2" {
			t.Errorf("expected entities decoded for the request, got raw query %q", got)
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(createTestPNG(200, 200))
	}))
	defer ts.Close()

	t.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	t.Setenv("IMAGE_HOST_KEEP_URL", strings.TrimPrefix(ts.URL, "http://"))
	secutils.ResetSSRFWhitelistForTest()

	markdown := fmt.Sprintf(`<img src="  %s/q.png?a=1&amp;b=2  " alt="kept">`, ts.URL)

	updated, images, fSvc := resolve(t, markdown)

	if len(images) != 1 {
		t.Fatalf("expected 1 stored image, got %d", len(images))
	}
	if atomic.LoadInt64(&hits) != 1 {
		t.Fatalf("expected the image to be downloaded once, got %d", hits)
	}
	if len(fSvc.saved) != 0 {
		t.Errorf("a whitelisted image must not be uploaded, got %d uploads", len(fSvc.saved))
	}

	serving := images[0].ServingURL
	// ServingURL carries two obligations that have to agree: later stages locate
	// the image by searching the document for it, and the multimodal stage fetches
	// that same string.
	if !strings.Contains(updated, serving) {
		t.Errorf("ServingURL %q is not present in the document %q", serving, updated)
	}
	if serving != ts.URL+"/q.png?a=1&b=2" {
		t.Errorf("ServingURL must be the normalized fetchable URL, got %q", serving)
	}
	if strings.TrimSpace(serving) != serving {
		t.Errorf("ServingURL must not carry padding: %q", serving)
	}
	if strings.Contains(serving, "&amp;") {
		t.Errorf("ServingURL must not carry HTML entities: %q", serving)
	}
	if !strings.Contains(updated, `alt="kept"`) {
		t.Errorf("the rest of the tag must survive: %s", updated)
	}
}

func TestResolveRemoteImages_CodeExampleIsRewritten(t *testing.T) {
	allowLocalhost(t)
	ts, _ := remoteImageServer(t)

	// Accepted limitation: this resolver does not parse Markdown structure, so a
	// tag shown as a documentation example inside a fence is rewritten like any
	// other. Recognising code spans reliably needs a Markdown parser; a
	// hand-rolled scanner risks the opposite and far worse failure of treating a
	// document's real screenshots as examples.
	markdown := fmt.Sprintf("```html\n<img src=\"%s/example.png\">\n```", ts.URL)

	updated, images, _ := resolve(t, markdown)

	if len(images) != 1 {
		t.Fatalf("expected the example to be resolved, got %d", len(images))
	}
	if strings.Contains(updated, ts.URL) {
		t.Errorf("expected the example to be rewritten: %s", updated)
	}
}

func TestResolveRemoteImages_CancelledContextStops(t *testing.T) {
	allowLocalhost(t)
	ts, hits := remoteImageServer(t)

	var b strings.Builder
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&b, "<img src=\"%s/c%d.png\">\n\n", ts.URL, i)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, images, err := NewImageResolver().ResolveRemoteImages(ctx, b.String(), &mockFileService{}, 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(images) != 0 {
		t.Errorf("expected no images after cancellation, got %d", len(images))
	}
	if got := atomic.LoadInt64(hits); got != 0 {
		t.Errorf("expected no fetch after cancellation, got %d", got)
	}
}

func TestResolveAndStoreThenResolveRemoteImages(t *testing.T) {
	allowLocalhost(t)
	ts, _ := remoteImageServer(t)

	// Mirrors the real ingest order, where ResolveAndStore runs first and
	// ResolveRemoteImages then works on its output. This is the only place the
	// two HTML resolvers can interfere with each other.
	markdown := fmt.Sprintf(
		"<img src=\"images/relative.png\">\n\n<img src=\"%s/remote.png\">", ts.URL)

	fSvc := &mockFileService{}
	resolver := NewImageResolver()

	afterStore, storeImages, err := resolver.ResolveAndStore(context.Background(), &types.ReadResult{
		MarkdownContent: markdown,
		ImageRefs: []types.ImageRef{{
			OriginalRef: "images/relative.png",
			Filename:    "relative.png",
			MimeType:    "image/png",
			ImageData:   createTestPNG(200, 200),
		}},
	}, fSvc, 42)
	if err != nil {
		t.Fatalf("ResolveAndStore: %v", err)
	}
	if len(storeImages) != 1 {
		t.Fatalf("expected the relative image stored, got %d", len(storeImages))
	}

	afterRemote, remoteImages, err := resolver.ResolveRemoteImages(
		context.Background(), afterStore, fSvc, 42)
	if err != nil {
		t.Fatalf("ResolveRemoteImages: %v", err)
	}
	if len(remoteImages) != 1 {
		t.Fatalf("expected the remote image stored, got %d", len(remoteImages))
	}
	if strings.Contains(afterRemote, ts.URL) {
		t.Errorf("remote URL survived: %s", afterRemote)
	}
	if strings.Contains(afterRemote, "images/relative.png") {
		t.Errorf("relative reference was not rewritten by the earlier pass: %s", afterRemote)
	}
	if n := strings.Count(afterRemote, "<img"); n != 2 {
		t.Errorf("expected both tags preserved, found %d", n)
	}
}
