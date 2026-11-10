package seafile

import (
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/utils"
)

// TestMain whitelists loopback for SSRF so the httptest servers are reachable.
// Production keeps the default strict policy.
func TestMain(m *testing.M) {
	_ = os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	utils.ResetSSRFWhitelistForTest()
	os.Exit(m.Run())
}

type fakeNode struct {
	Type  string
	ID    string
	Mtime int64
	Body  []byte
}

type fakeLocation struct{ repo, path string }

type fakeRequest struct {
	Headers  http.Header
	RawQuery string
}

type fakeFailure struct {
	status     int
	remaining  int // negative repeats forever
	retryAfter string
}

// fakeSeafile is an in-process stand-in for a Seafile 10.0.1 deployment with
// the API and the fileserver on separate servers. Requests are keyed as
// "ping", "repos", "dir:<path>", "file:<path>" and "download:<path>"; the
// same keys drive failure injection.
type fakeSeafile struct {
	api        *httptest.Server
	fileserver *httptest.Server
	// prefix simulates a deployment path such as "/seafile".
	prefix string

	mu        sync.Mutex
	repos     []repoInfo
	tree      map[string]map[string]*fakeNode // repo → path → node
	forbidden map[string]bool                 // directory listings answering 403

	failures        map[string]*fakeFailure
	rawBody         map[string]string // raw 200 body per key
	redirects       map[string]string // 302 target per key
	linkOID         map[string]string // path → oid header override
	linkURL         map[string]string // path → download link override
	chunkedOversize map[string]int64  // path → bytes streamed without Content-Length
	partialContent  map[string]bool   // path → answer 206

	tokens map[string]fakeLocation
	calls  map[string]int
	seen   map[string][]fakeRequest
}

func newFakeSeafile(t *testing.T) *fakeSeafile {
	t.Helper()
	f := &fakeSeafile{
		tree:            map[string]map[string]*fakeNode{},
		forbidden:       map[string]bool{},
		failures:        map[string]*fakeFailure{},
		rawBody:         map[string]string{},
		redirects:       map[string]string{},
		linkOID:         map[string]string{},
		linkURL:         map[string]string{},
		chunkedOversize: map[string]int64{},
		partialContent:  map[string]bool{},
		tokens:          map[string]fakeLocation{},
		calls:           map[string]int{},
		seen:            map[string][]fakeRequest{},
	}
	f.fileserver = httptest.NewServer(http.HandlerFunc(f.handleDownload))
	t.Cleanup(f.fileserver.Close)
	f.api = httptest.NewServer(http.HandlerFunc(f.handleAPI))
	t.Cleanup(f.api.Close)
	return f
}

func (f *fakeSeafile) baseURL() string { return f.api.URL + f.prefix }

func (f *fakeSeafile) addRepo(id, name, typ string, encrypted bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repos = append(f.repos, repoInfo{ID: id, Name: name, Type: typ, Encrypted: encrypted, Permission: "rw"})
	f.addDirLocked(id, "/")
}

func (f *fakeSeafile) renameRepo(id, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.repos {
		if f.repos[i].ID == id {
			f.repos[i].Name = name
		}
	}
}

func (f *fakeSeafile) addDirLocked(repo, p string) {
	if f.tree[repo] == nil {
		f.tree[repo] = map[string]*fakeNode{}
	}
	for {
		if f.tree[repo][p] == nil {
			f.tree[repo][p] = &fakeNode{Type: "dir", ID: "dir-" + p}
		}
		if p == "/" {
			return
		}
		p = path.Dir(p)
	}
}

func (f *fakeSeafile) addDir(repo, p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addDirLocked(repo, p)
}

func (f *fakeSeafile) addFile(repo, p string, body []byte, mtime int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addDirLocked(repo, path.Dir(p))
	f.tree[repo][p] = &fakeNode{
		Type: "file", ID: fmt.Sprintf("%x", sha1.Sum(body)), Mtime: mtime, Body: bytes.Clone(body),
	}
}

// removePath deletes a file or a directory subtree.
func (f *fakeSeafile) removePath(repo, p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for name := range f.tree[repo] {
		if name == p || strings.HasPrefix(name, strings.TrimSuffix(p, "/")+"/") {
			delete(f.tree[repo], name)
		}
	}
}

// movePath renames a file or directory subtree, keeping oids and mtimes.
func (f *fakeSeafile) movePath(repo, from, to string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	moved := map[string]*fakeNode{}
	for name, node := range f.tree[repo] {
		if name == from || strings.HasPrefix(name, from+"/") {
			moved[to+strings.TrimPrefix(name, from)] = node
			delete(f.tree[repo], name)
		}
	}
	f.addDirLocked(repo, path.Dir(to))
	for name, node := range moved {
		f.tree[repo][name] = node
	}
}

// setBody replaces a file's content, which changes its oid and bumps mtime.
func (f *fakeSeafile) setBody(repo, p string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if node := f.tree[repo][p]; node != nil {
		node.Body = bytes.Clone(body)
		node.ID = fmt.Sprintf("%x", sha1.Sum(body))
		node.Mtime++
	}
}

// touch bumps a file's mtime without changing its content or oid.
func (f *fakeSeafile) touch(repo, p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if node := f.tree[repo][p]; node != nil {
		node.Mtime++
	}
}

// fail makes the next count requests for key answer status (count < 0: always).
func (f *fakeSeafile) fail(key string, status, count int) {
	f.failWithRetryAfter(key, status, count, "")
}

func (f *fakeSeafile) failWithRetryAfter(key string, status, count int, retryAfter string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[key] = &fakeFailure{status: status, remaining: count, retryAfter: retryAfter}
}

func (f *fakeSeafile) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[key]
}

func (f *fakeSeafile) counts() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]int, len(f.calls))
	for key, n := range f.calls {
		out[key] = n
	}
	return out
}

func (f *fakeSeafile) resetCounts() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = map[string]int{}
	f.seen = map[string][]fakeRequest{}
}

// countPrefix sums the counters whose key starts with prefix ("dir:", "download:").
func (f *fakeSeafile) countPrefix(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for key, n := range f.calls {
		if strings.HasPrefix(key, prefix) {
			total += n
		}
	}
	return total
}

func (f *fakeSeafile) requests(key string) []fakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeRequest, 0, len(f.seen[key]))
	for _, req := range f.seen[key] {
		out = append(out, fakeRequest{Headers: req.Headers.Clone(), RawQuery: req.RawQuery})
	}
	return out
}

// The handler helpers below run with mu held.

func (f *fakeSeafile) record(key string, r *http.Request) {
	f.calls[key]++
	f.seen[key] = append(f.seen[key], fakeRequest{Headers: r.Header.Clone(), RawQuery: r.URL.RawQuery})
}

// injected serves a configured redirect, failure or raw body for key.
func (f *fakeSeafile) injected(w http.ResponseWriter, r *http.Request, key string) bool {
	if target, ok := f.redirects[key]; ok {
		http.Redirect(w, r, target, http.StatusFound)
		return true
	}
	if failure := f.failures[key]; failure != nil && failure.remaining != 0 {
		if failure.remaining > 0 {
			failure.remaining--
		}
		if failure.retryAfter != "" {
			w.Header().Set("Retry-After", failure.retryAfter)
		}
		w.WriteHeader(failure.status)
		_, _ = w.Write([]byte("injected failure"))
		return true
	}
	if body, ok := f.rawBody[key]; ok {
		_, _ = w.Write([]byte(body))
		return true
	}
	return false
}

func (f *fakeSeafile) handleAPI(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, f.prefix+"/api2/") {
		http.NotFound(w, r)
		return
	}
	route := strings.TrimPrefix(r.URL.Path, f.prefix)
	switch route {
	case "/api2/auth/ping/":
		f.record("ping", r)
		if !f.injected(w, r, "ping") {
			_ = json.NewEncoder(w).Encode("pong")
		}
		return
	case "/api2/repos/":
		f.record("repos", r)
		if !f.injected(w, r, "repos") {
			_ = json.NewEncoder(w).Encode(f.repos)
		}
		return
	}
	parts := strings.Split(strings.Trim(route, "/"), "/")
	if len(parts) != 4 || parts[0] != "api2" || parts[1] != "repos" || (parts[3] != "dir" && parts[3] != "file") {
		http.NotFound(w, r)
		return
	}
	repo, op, p := parts[2], parts[3], r.URL.Query().Get("p")
	key := op + ":" + p
	f.record(key, r)
	if f.injected(w, r, key) {
		return
	}
	node := f.tree[repo][p]
	if op == "dir" {
		if f.forbidden[p] {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if node == nil || node.Type != "dir" {
			http.NotFound(w, r)
			return
		}
		f.writeDir(w, repo, p)
		return
	}
	if node == nil || node.Type != "file" {
		http.NotFound(w, r)
		return
	}
	if r.URL.Query().Get("reuse") != "1" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	oid := node.ID
	if override, ok := f.linkOID[p]; ok {
		oid = override
	}
	w.Header().Set("oid", oid)
	token := fmt.Sprintf("download-token-%d", len(f.tokens)+1)
	f.tokens[token] = fakeLocation{repo: repo, path: p}
	link := f.fileserver.URL + "/files/" + token
	if override, ok := f.linkURL[p]; ok {
		link = override
	}
	_ = json.NewEncoder(w).Encode(link)
}

// writeDir renders one level like seahub: every entry carries id, name, type,
// mtime and permission; only files carry size.
func (f *fakeSeafile) writeDir(w http.ResponseWriter, repo, dir string) {
	entries := make([]map[string]any, 0)
	for name, node := range f.tree[repo] {
		if name == dir || path.Dir(name) != dir {
			continue
		}
		entry := map[string]any{
			"id": node.ID, "name": path.Base(name), "type": node.Type,
			"mtime": node.Mtime, "permission": "rw",
		}
		if node.Type == "file" {
			entry["size"] = len(node.Body)
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i]["name"].(string) < entries[j]["name"].(string)
	})
	_ = json.NewEncoder(w).Encode(entries)
}

func (f *fakeSeafile) handleDownload(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	location, ok := f.tokens[strings.TrimPrefix(r.URL.Path, "/files/")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	p := location.path
	key := "download:" + p
	f.record(key, r)
	if f.injected(w, r, key) {
		return
	}
	node := f.tree[location.repo][p]
	if node == nil {
		http.NotFound(w, r)
		return
	}
	status := http.StatusOK
	if f.partialContent[p] {
		status = http.StatusPartialContent
	}
	if n := f.chunkedOversize[p]; n > 0 {
		w.WriteHeader(status)
		_, _ = w.Write(bytes.Repeat([]byte("x"), int(n-1)))
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("x"))
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(node.Body)))
	w.WriteHeader(status)
	_, _ = w.Write(node.Body)
}
