package nextcloud

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
)

func fakeClient(t *testing.T) (*fakeNextcloud, *client) {
	t.Helper()
	f := newFakeNextcloud(t)
	cfg, err := parseConfig(f.config("/"))
	if err != nil {
		t.Fatal(err)
	}
	c := newClient(cfg)
	t.Cleanup(c.http.CloseIdleConnections)
	return f, c
}

func TestPropfindDepthNamespacesAndEncodedPaths(t *testing.T) {
	f, c := fakeClient(t)
	f.add("/产品/100% + 说明.pdf", "pdf")
	f.add("/产品/literal%2F.pdf", "pdf")
	entries, err := c.propfind(t.Context(), "/产品", 1)
	if err != nil || len(entries) != 3 {
		t.Fatalf("listing = %+v, %v", entries, err)
	}
	if entries[0].Path != "/产品" || !entries[0].IsDir || entries[1].Path != "/产品/100% + 说明.pdf" ||
		entries[2].Path != "/产品/literal%2F.pdf" || entries[1].FileID == "" || entries[1].ETag != `"v1"` {
		t.Fatalf("unexpected decoded listing: %+v", entries)
	}
	entries, err = c.propfind(t.Context(), "/产品", 0)
	if err != nil || len(entries) != 1 {
		t.Fatalf("depth 0 = %+v, %v", entries, err)
	}
	if _, err = c.propfind(t.Context(), "/产品", -1); err == nil {
		t.Fatal("invalid depth accepted")
	}
	for _, h := range f.requests["PROPFIND:/产品"] {
		if h.Get("Authorization") == "" || h.Get("User-Agent") == "" {
			t.Fatal("missing auth or user agent")
		}
	}
}

func TestPropfindRejectsIncompleteOrUntrustedListings(t *testing.T) {
	f, c := fakeClient(t)
	const props = `<propstat><prop><resourcetype/><getetag>v1</getetag><getcontentlength>3</getcontentlength></prop>` +
		`<status>HTTP/1.1 200 OK</status></propstat>`
	for _, body := range []string{
		`<multistatus xmlns="DAV:"></multistatus>`,
		`<multistatus xmlns="wrong"><response/></multistatus>`,
		`<multistatus xmlns="DAV:"><response><href>` + testDAVPrefix + `/</href>`,
		`<multistatus xmlns="DAV:"><response><href>https://example.com/other</href>` + props +
			`</response></multistatus>`,
		`<multistatus xmlns="DAV:"><response><href>` + testDAVPrefix + `/../outside</href>` + props +
			`</response></multistatus>`,
		`<multistatus xmlns="DAV:"><response><href>` + testDAVPrefix + `/deep/file.pdf</href>` + props +
			`</response></multistatus>`,
		`<multistatus xmlns="DAV:"><response><href>` + testDAVPrefix +
			`/</href><status>HTTP/1.1 403 Forbidden</status>` +
			`</response></multistatus>`,
	} {
		f.raw["PROPFIND:/"] = body
		_, err := c.propfind(t.Context(), "/", 1)
		if !errors.Is(err, datasource.ErrFetchFailed) || strings.Contains(err.Error(), testPassword) {
			t.Fatalf("unsafe multistatus = %v", err)
		}
	}
}

func TestClientAuthAndRedirectsAreSafe(t *testing.T) {
	f, c := fakeClient(t)
	c.password = "wrong-password"
	_, err := c.propfind(t.Context(), "/", 0)
	if !errors.Is(err, datasource.ErrInvalidCredentials) || strings.Contains(err.Error(), testPassword) {
		t.Fatalf("auth error = %v", err)
	}
	c.password = testPassword
	for _, method := range []string{"PROPFIND", http.MethodGet} {
		f.add("/file.pdf", "pdf")
		f.redirect[method+":/file.pdf"] = "http://192.0.2.1/blocked?secret=" + testPassword
		if method == "PROPFIND" {
			_, err = c.propfind(t.Context(), "/file.pdf", 0)
		} else {
			_, err = c.download(t.Context(), entry{Path: "/file.pdf", Size: 3}, 10)
		}
		if !errors.Is(err, errSSRFBlocked) || strings.Contains(err.Error(), testPassword) {
			t.Fatalf("redirect error = %v", err)
		}
	}
}

func TestDownloadCapsAndVersionChecks(t *testing.T) {
	f, c := fakeClient(t)
	f.add("/说明.pdf", "pdf")
	e := entry{Path: "/说明.pdf", Size: 3, ETag: `"v1"`}
	body, err := c.download(t.Context(), e, 3)
	if err != nil || string(body) != "pdf" {
		t.Fatalf("download = %q, %v", body, err)
	}
	if _, err = c.download(t.Context(), e, 2); !errors.Is(err, errTooLarge) {
		t.Fatalf("header cap = %v", err)
	}
	f.chunked[e.Path] = []byte("four")
	if _, err = c.download(t.Context(), e, 3); !errors.Is(err, errTooLarge) {
		t.Fatalf("stream cap = %v", err)
	}
	delete(f.chunked, e.Path)
	e.ETag = `"v0"`
	if _, err = c.download(t.Context(), e, 3); !errors.Is(err, errSourceChanged) {
		t.Fatalf("If-Match = %v", err)
	}
	e.ETag = ""
	e.Size = 2
	if _, err = c.download(t.Context(), e, 4); !errors.Is(err, errSourceChanged) {
		t.Fatalf("size mismatch = %v", err)
	}
	f.add("/empty.pdf", "")
	if _, err = c.download(t.Context(), entry{Path: "/empty.pdf"}, 3); !errors.Is(err, errEmptyFile) {
		t.Fatalf("empty file = %v", err)
	}
}

func TestDownloadRevalidatesMissingAndWeakValidators(t *testing.T) {
	for _, tag := range []string{"", `W/"v1"`, `"v1"`} {
		t.Run(tag, func(t *testing.T) {
			f, c := fakeClient(t)
			f.add("/file.pdf", "pdf")
			f.noValidators = true
			node := f.nodes["/file.pdf"]
			node.etag = tag
			f.nodes["/file.pdf"] = node
			entries, err := c.propfind(t.Context(), "/file.pdf", 0)
			if err != nil {
				t.Fatal(err)
			}
			node.modified = "Thu, 22 Oct 2026 07:28:00 GMT"
			f.afterGET["/file.pdf"] = node
			_, err = c.download(t.Context(), entries[0], 10)
			if !errors.Is(err, errSourceChanged) {
				t.Fatalf("same-size race accepted: %v", err)
			}
			if tag == "" || strings.HasPrefix(tag, "W/") {
				if f.requests["GET:/file.pdf"][0].Get("If-Unmodified-Since") == "" {
					t.Fatal("missing date precondition")
				}
			}
		})
	}
}

func TestListingByteLimitRejectsAnOtherwiseValidResponse(t *testing.T) {
	f, c := fakeClient(t)
	root := `<multistatus xmlns="DAV:"><response><href>` + testDAVPrefix + `/</href><propstat>` +
		`<prop><resourcetype><collection/></resourcetype></prop><status>HTTP/1.1 200 OK</status>` +
		`</propstat></response></multistatus>`
	f.raw["PROPFIND:/"] = root + strings.Repeat(" ", int(listingLimit))
	if _, err := c.propfind(t.Context(), "/", 1); !errors.Is(err, errInvalidResponse) {
		t.Fatalf("oversized valid XML accepted: %v", err)
	}
}

func TestWhitelistedCrossOriginRedirectNeverReceivesCredentials(t *testing.T) {
	f, c := fakeClient(t)
	var calls atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer sink.Close()
	f.add("/file.pdf", "pdf")
	f.redirect["GET:/file.pdf"] = sink.URL + "/file.pdf"
	_, err := c.download(t.Context(), entry{Path: "/file.pdf", Size: 3}, 10)
	if !errors.Is(err, errSSRFBlocked) || calls.Load() != 0 {
		t.Fatalf("credentials followed a cross-origin redirect: calls=%d, error=%v", calls.Load(), err)
	}
}
