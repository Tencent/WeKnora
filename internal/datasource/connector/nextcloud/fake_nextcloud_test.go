package nextcloud

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	testPassword  = "app-password-for-tests"
	testDAVPrefix = "/cloud/remote.php/dav/files/alice"
)

type fakeNode struct {
	dir                bool
	id, etag, modified string
	body               []byte
}

// fakeNextcloud uses DAV namespaces and percent-encoded hrefs, including a
// failed optional property group as returned by servers without that property.
type fakeNextcloud struct {
	server       *httptest.Server
	mu           sync.Mutex
	nodes        map[string]fakeNode
	status       map[string]int
	raw          map[string]string
	redirect     map[string]string
	requests     map[string][]http.Header
	chunked      map[string][]byte
	noValidators bool
	afterGET     map[string]fakeNode
}

func newFakeNextcloud(t *testing.T) *fakeNextcloud {
	t.Helper()
	allowTestServer(t)
	f := &fakeNextcloud{
		nodes: map[string]fakeNode{"/": {dir: true}}, status: map[string]int{}, raw: map[string]string{},
		redirect: map[string]string{}, requests: map[string][]http.Header{}, chunked: map[string][]byte{},
		afterGET: map[string]fakeNode{},
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeNextcloud) config(roots ...string) *types.DataSourceConfig {
	return &types.DataSourceConfig{Type: "nextcloud", Credentials: map[string]any{
		"base_url": f.server.URL + "/cloud", "username": "alice", "app_password": testPassword,
	}, ResourceIDs: roots}
}

func (f *fakeNextcloud) add(p, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for dir := path.Dir(p); ; dir = path.Dir(dir) {
		f.nodes[dir] = fakeNode{dir: true}
		if dir == "/" {
			break
		}
	}
	f.nodes[p] = fakeNode{id: "id-" + p, etag: `"v1"`, modified: "Wed, 21 Oct 2026 07:28:00 GMT", body: []byte(body)}
}

func (f *fakeNextcloud) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests[key])
}

func (f *fakeNextcloud) serveHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := strings.TrimPrefix(r.URL.Path, testDAVPrefix)
	if p == "" {
		p = "/"
	}
	p = strings.TrimSuffix(p, "/")
	if p == "" {
		p = "/"
	}
	key := r.Method + ":" + p
	f.requests[key] = append(f.requests[key], r.Header.Clone())
	user, password, ok := r.BasicAuth()
	if !ok || user != "alice" || password != testPassword {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(testPassword))
		return
	}
	if target := f.redirect[key]; target != "" {
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
		return
	}
	if status := f.status[key]; status != 0 {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(testPassword))
		return
	}
	node, ok := f.nodes[p]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method == "PROPFIND" {
		depth := r.Header.Get("Depth")
		if depth != "0" && depth != "1" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusMultiStatus)
		if raw, ok := f.raw[key]; ok {
			_, _ = w.Write([]byte(raw))
			return
		}
		paths := []string{p}
		if depth == "1" && node.dir {
			for child := range f.nodes {
				if child != p && path.Dir(child) == p {
					paths = append(paths, child)
				}
			}
		}
		sort.Strings(paths)
		_, _ = w.Write([]byte(`<d:multistatus xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns">`))
		for _, entry := range paths {
			f.writeEntry(w, entry, f.nodes[entry])
		}
		_, _ = w.Write([]byte(`</d:multistatus>`))
		return
	}
	if r.Method != http.MethodGet || node.dir {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if match := r.Header.Get("If-Match"); match != "" && match != node.etag {
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	if !f.noValidators {
		w.Header().Set("ETag", node.etag)
		w.Header().Set("Last-Modified", node.modified)
	}
	if body, ok := f.chunked[p]; ok {
		w.(http.Flusher).Flush()
		_, _ = w.Write(body)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(node.body)))
	_, _ = w.Write(node.body)
	if updated, ok := f.afterGET[p]; ok {
		f.nodes[p] = updated
	}
}

func (f *fakeNextcloud) writeEntry(w http.ResponseWriter, p string, n fakeNode) {
	u := url.URL{Path: testDAVPrefix + p}
	if n.dir && !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	_, _ = fmt.Fprintf(w, "<d:response><d:href>%s</d:href><d:propstat><d:prop>", xmlEscape(u.EscapedPath()))
	if n.dir {
		_, _ = w.Write([]byte(`<d:resourcetype><d:collection/></d:resourcetype>`))
	} else {
		_, _ = fmt.Fprintf(w, `<d:resourcetype/><d:getetag>%s</d:getetag><d:getlastmodified>%s</d:getlastmodified>`+
			`<d:getcontentlength>%d</d:getcontentlength>`, xmlEscape(n.etag), n.modified, len(n.body))
	}
	_, _ = fmt.Fprintf(w, `</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>`+
		`<d:propstat><d:prop><oc:fileid>%s</oc:fileid></d:prop><d:status>HTTP/1.1 200 OK</d:status>`+
		`</d:propstat><d:propstat><d:prop><d:displayname/></d:prop><d:status>HTTP/1.1 404 Not Found</d:status>`+
		`</d:propstat></d:response>`, xmlEscape(n.id))
}

func xmlEscape(s string) string {
	var out strings.Builder
	_ = xml.EscapeText(&out, []byte(s))
	return out.String()
}
