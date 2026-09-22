package seafile

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/utils"
)

// noWait replaces the package sleep with a recorder so tests never wait.
func noWait(t *testing.T) *[]time.Duration {
	t.Helper()
	original := sleep
	var waits []time.Duration
	sleep = func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		return ctx.Err()
	}
	t.Cleanup(func() { sleep = original })
	return &waits
}

func testClient(t *testing.T) (*fakeSeafile, *client, *[]time.Duration) {
	t.Helper()
	waits := noWait(t)
	f := newFakeSeafile(t)
	f.addRepo(testRepoID, "Library", "repo", false)
	c := newClient(config{baseURL: f.baseURL(), token: testToken})
	t.Cleanup(c.api.CloseIdleConnections)
	t.Cleanup(c.files.CloseIdleConnections)
	return f, c, waits
}

func assertNoCredentials(t *testing.T, requests []fakeRequest) {
	t.Helper()
	for _, req := range requests {
		if req.Headers.Get("Authorization") != "" || req.Headers.Get("Cookie") != "" {
			t.Fatal("fileserver received credentials")
		}
	}
}

func TestPing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		status int
		want   error
	}{
		{name: "pong"},
		{name: "unauthorized", status: 401, want: datasource.ErrInvalidCredentials},
		{name: "forbidden", status: 403, want: datasource.ErrInvalidCredentials},
		{name: "object", body: `{"ok":true}`, want: datasource.ErrFetchFailed},
		{name: "wrong string", body: `"not pong"`, want: datasource.ErrFetchFailed},
		{name: "malformed", body: testToken, want: datasource.ErrFetchFailed},
		{name: "server error", status: 500, want: datasource.ErrFetchFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c, waits := testClient(t)
			if tc.body != "" {
				f.rawBody["ping"] = tc.body
			}
			if tc.status != 0 {
				f.fail("ping", tc.status, 1)
			}
			err := c.ping(t.Context())
			if !errors.Is(err, tc.want) {
				t.Fatalf("ping = %v, want %v", err, tc.want)
			}
			if err != nil && strings.Contains(err.Error(), testToken) {
				t.Fatalf("ping error leaked the token: %v", err)
			}
			if f.count("ping") != 1 || len(*waits) != 0 {
				t.Fatalf("unexpected retries: counts=%v waits=%v", f.counts(), *waits)
			}
		})
	}
}

func TestAPIHeadersAndDeploymentPrefix(t *testing.T) {
	f, c, _ := testClient(t)
	f.prefix = "/seafile"
	c.baseURL = f.baseURL()
	f.addFile(testRepoID, "/file", []byte("body"), 123)
	f.addRepo(otherRepoID, "Encrypted", "srepo", true)

	if c.api.Timeout != apiTimeout || c.files.Timeout != downloadTimeout ||
		c.api.CheckRedirect == nil || c.files.CheckRedirect == nil {
		t.Fatal("clients must come from the shared SSRF-guarded constructor")
	}
	if err := c.ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	repos, err := c.listRepos(t.Context())
	if err != nil || len(repos) != 2 || !repos[1].Encrypted || repos[1].Type != "srepo" {
		t.Fatalf("listRepos = %+v, %v", repos, err)
	}
	if _, err := c.listDir(t.Context(), testRepoID, "/"); err != nil {
		t.Fatal(err)
	}
	link, oid, err := c.downloadLink(t.Context(), testRepoID, "/file")
	if err != nil || oid == "" {
		t.Fatalf("downloadLink oid = %q, %v", oid, err)
	}
	body, err := c.download(t.Context(), link, 4, 4)
	if err != nil || string(body) != "body" {
		t.Fatalf("download = %q, %v", body, err)
	}
	for _, key := range []string{"ping", "repos", "dir:/", "file:/file"} {
		requests := f.requests(key)
		if len(requests) != 1 {
			t.Fatalf("%s requests = %d", key, len(requests))
		}
		h := requests[0].Headers
		if h.Get("Authorization") != "Token "+testToken || h.Get("Accept") != "application/json" ||
			h.Get("User-Agent") != userAgent {
			t.Fatalf("%s missing API headers: %v", key, h)
		}
	}
	assertNoCredentials(t, f.requests("download:/file"))
}

func TestListReposCredentials(t *testing.T) {
	for _, status := range []int{401, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f, c, waits := testClient(t)
			f.fail("repos", status, -1)
			_, err := c.listRepos(t.Context())
			if !errors.Is(err, datasource.ErrInvalidCredentials) || statusOf(err) != status ||
				f.count("repos") != 1 || len(*waits) != 0 {
				t.Fatalf("listRepos = %v, counts = %v", err, f.counts())
			}
		})
	}
}

func TestListDirQueryAndOptionalSize(t *testing.T) {
	f, c, _ := testClient(t)
	f.addDir(testRepoID, "/产品/Z目录")
	f.addFile(testRepoID, "/产品/a.pdf", []byte("pdf"), 42)
	entries, err := c.listDir(t.Context(), testRepoID, "/产品")
	if err != nil || len(entries) != 2 {
		t.Fatalf("listDir = %+v, %v", entries, err)
	}
	if entries[0].Type != "dir" || entries[0].Size != nil ||
		entries[1].Size == nil || *entries[1].Size != 3 || entries[1].Mtime != 42 {
		t.Fatalf("unexpected entries: %+v", entries)
	}
	if got := f.requests("dir:/产品")[0].RawQuery; got != "p=%2F%E4%BA%A7%E5%93%81" {
		t.Fatalf("path encoded incorrectly: %s", got)
	}
	f.addDir(testRepoID, "/a:%2Fb")
	if _, err := c.listDir(t.Context(), testRepoID, "/a:%2Fb"); err != nil {
		t.Fatal(err)
	}
	if got := f.requests("dir:/a:%2Fb")[0].RawQuery; got != "p=%2Fa%3A%252Fb" {
		t.Fatalf("literal percent encoded incorrectly: %s", got)
	}
}

func TestListDirRetries(t *testing.T) {
	twoWaits := []time.Duration{time.Second, 2 * time.Second}
	for _, tc := range []struct {
		name       string
		status     int
		failures   int
		retryAfter string
		calls      int
		final      int
		waits      []time.Duration
		tooLong    bool
	}{
		{name: "503 twice", status: 503, failures: 2, calls: 3, waits: twoWaits},
		{name: "503 exhausted", status: 503, failures: 3, calls: 3, final: 503, waits: twoWaits},
		{name: "502", status: 502, failures: 1, calls: 2, waits: []time.Duration{time.Second}},
		{name: "504", status: 504, failures: 1, calls: 2, waits: []time.Duration{time.Second}},
		{
			name: "429 retry-after", status: 429, failures: 1, retryAfter: "3",
			calls: 2, waits: []time.Duration{3 * time.Second},
		},
		{name: "429 too long", status: 429, failures: 3, retryAfter: "120", calls: 1, final: 429, tooLong: true},
		{name: "401", status: 401, failures: 3, calls: 1, final: 401},
		{name: "403", status: 403, failures: 3, calls: 1, final: 403},
		{name: "404", status: 404, failures: 3, calls: 1, final: 404},
		{name: "500", status: 500, failures: 3, calls: 1, final: 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c, waits := testClient(t)
			f.failWithRetryAfter("dir:/", tc.status, tc.failures, tc.retryAfter)
			_, err := c.listDir(t.Context(), testRepoID, "/")
			if statusOf(err) != tc.final || (err == nil) != (tc.final == 0) {
				t.Fatalf("listDir = %v, want status %d", err, tc.final)
			}
			if errors.Is(err, datasource.ErrInvalidCredentials) {
				t.Fatal("listDir must not map directory 401/403 to ErrInvalidCredentials")
			}
			if tc.tooLong && (!errors.Is(err, errRetryAfterTooLong) || !errors.Is(err, datasource.ErrFetchFailed)) {
				t.Fatalf("missing run-fatal Retry-After sentinels: %v", err)
			}
			if tc.final != 0 && !errors.Is(err, datasource.ErrFetchFailed) {
				t.Fatalf("status error must wrap ErrFetchFailed: %v", err)
			}
			if f.count("dir:/") != tc.calls || !slices.Equal(*waits, tc.waits) {
				t.Fatalf("counts = %v, waits = %v", f.counts(), *waits)
			}
		})
	}
}

func TestListDirInvalidJSON(t *testing.T) {
	for _, body := range []string{"{", "null", `[] []`} {
		t.Run(body, func(t *testing.T) {
			f, c, waits := testClient(t)
			f.rawBody["dir:/"] = body
			_, err := c.listDir(t.Context(), testRepoID, "/")
			if !errors.Is(err, errInvalidResponse) || !errors.Is(err, datasource.ErrFetchFailed) ||
				f.count("dir:/") != 1 || len(*waits) != 0 {
				t.Fatalf("invalid listing = %v, counts = %v", err, f.counts())
			}
		})
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		value string
		want  time.Duration
		fatal bool
	}{
		{"", 0, false},
		{"0", 0, false},
		{"3", 3 * time.Second, false},
		{"60", 60 * time.Second, false},
		{"61", 0, true},
		{"120", 0, true},
		{"9223372037", 0, true},
		{"999999999999999999999999999999", 0, true},
		{"invalid", 0, false},
		{now.Add(-time.Hour).Format(http.TimeFormat), 0, false},
		{now.Add(30 * time.Second).Format(http.TimeFormat), 30 * time.Second, false},
		{now.Add(61 * time.Second).Format(http.TimeFormat), 0, true},
	} {
		wait, err := retryAfter(tc.value, now)
		if wait != tc.want || errors.Is(err, errRetryAfterTooLong) != tc.fatal {
			t.Fatalf("retryAfter(%q) = (%v, %v), want (%v, fatal=%v)", tc.value, wait, err, tc.want, tc.fatal)
		}
	}
}

func TestDownloadLink(t *testing.T) {
	for _, body := range []string{
		`"http://user:secret@localhost/files/token"`, `"/relative"`,
		`"ftp://localhost/file"`, `""`, `null`, `{"link":"x"}`,
		`"http://localhost/file" "extra"`,
	} {
		t.Run(body, func(t *testing.T) {
			f, c, waits := testClient(t)
			f.addFile(testRepoID, "/file", []byte("body"), 1)
			f.rawBody["file:/file"] = body
			_, _, err := c.downloadLink(t.Context(), testRepoID, "/file")
			if !errors.Is(err, errInvalidResponse) || !errors.Is(err, datasource.ErrFetchFailed) ||
				f.count("file:/file") != 1 || len(*waits) != 0 {
				t.Fatalf("invalid link = %v", err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("invalid link leaked in error: %v", err)
			}
		})
	}
	for _, oid := range []string{"override-oid", ""} {
		t.Run("oid="+oid, func(t *testing.T) {
			f, c, _ := testClient(t)
			f.addFile(testRepoID, "/a:b%.pdf", []byte("body"), 1)
			f.linkOID["/a:b%.pdf"] = oid
			link, got, err := c.downloadLink(t.Context(), testRepoID, "/a:b%.pdf")
			if err != nil || got != oid || !strings.HasPrefix(link, f.fileserver.URL+"/files/") {
				t.Fatalf("downloadLink = (%q, %q, %v)", link, got, err)
			}
			if query := f.requests("file:/a:b%.pdf")[0].RawQuery; query != "p=%2Fa%3Ab%25.pdf&reuse=1" {
				t.Fatalf("file query = %s", query)
			}
		})
	}
}

func TestJSONCaps(t *testing.T) {
	for _, tc := range []struct {
		name  string
		key   string
		body  string
		limit int64
		call  func(*client, context.Context) error
	}{
		{"repos", "repos", "[]", listingJSONLimit, func(c *client, ctx context.Context) error {
			_, err := c.listRepos(ctx)
			return err
		}},
		{"dir", "dir:/", "[]", listingJSONLimit, func(c *client, ctx context.Context) error {
			_, err := c.listDir(ctx, testRepoID, "/")
			return err
		}},
		{"ping", "ping", `"pong"`, smallJSONLimit, func(c *client, ctx context.Context) error {
			return c.ping(ctx)
		}},
		{
			"link", "file:/file", `"http://localhost/files/token"`, smallJSONLimit,
			func(c *client, ctx context.Context) error {
				_, _, err := c.downloadLink(ctx, testRepoID, "/file")
				return err
			},
		},
	} {
		for _, extra := range []int{0, 1} {
			t.Run(fmt.Sprintf("%s/extra=%d", tc.name, extra), func(t *testing.T) {
				f, c, waits := testClient(t)
				f.addFile(testRepoID, "/file", []byte("body"), 1)
				f.rawBody[tc.key] = tc.body + strings.Repeat(" ", int(tc.limit)-len(tc.body)+extra)
				err := tc.call(c, t.Context())
				if extra == 0 && err != nil {
					t.Fatalf("body at cap rejected: %v", err)
				}
				if extra == 1 && (!errors.Is(err, errInvalidResponse) || !errors.Is(err, datasource.ErrFetchFailed)) {
					t.Fatalf("body above cap accepted: %v", err)
				}
				if f.count(tc.key) != 1 || len(*waits) != 0 {
					t.Fatalf("unexpected retries: %v", f.counts())
				}
			})
		}
	}
}

func TestDownload(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     string
		expected int64
		chunked  int64
		partial  bool
		want     error
		status   int
	}{
		{name: "exact limit", body: "body", expected: 4},
		{name: "content-length oversize", body: "large", expected: 5, want: errTooLarge},
		{name: "chunked oversize", body: "body", expected: 4, chunked: 5, want: errTooLarge},
		{
			name: "partial content", body: "body", expected: 4, partial: true,
			want: datasource.ErrFetchFailed, status: 206,
		},
		{name: "source changed", body: "body", expected: 5, want: errSourceChanged},
		{name: "empty", expected: 0, want: errEmptyFile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c, waits := testClient(t)
			f.addFile(testRepoID, "/file", []byte(tc.body), 1)
			f.chunkedOversize["/file"] = tc.chunked
			f.partialContent["/file"] = tc.partial
			link, _, err := c.downloadLink(t.Context(), testRepoID, "/file")
			if err != nil {
				t.Fatal(err)
			}
			body, err := c.download(t.Context(), link, tc.expected, 4)
			if !errors.Is(err, tc.want) || statusOf(err) != tc.status {
				t.Fatalf("download = %v, want %v", err, tc.want)
			}
			if err == nil && string(body) != tc.body {
				t.Fatalf("body = %q", body)
			}
			if err != nil && (strings.Contains(err.Error(), link) || strings.Contains(err.Error(), testToken)) {
				t.Fatalf("download error leaked the link or token: %v", err)
			}
			if f.count("download:/file") != 1 || len(*waits) != 0 {
				t.Fatalf("unexpected retries: %v", f.counts())
			}
			assertNoCredentials(t, f.requests("download:/file"))
		})
	}
}

func TestDownloadRetries(t *testing.T) {
	f, c, waits := testClient(t)
	f.addFile(testRepoID, "/file", []byte("body"), 1)
	f.fail("download:/file", 503, 2)
	link, _, err := c.downloadLink(t.Context(), testRepoID, "/file")
	if err != nil {
		t.Fatal(err)
	}
	body, err := c.download(t.Context(), link, 4, 4)
	if err != nil || string(body) != "body" || f.count("download:/file") != 3 ||
		!slices.Equal(*waits, []time.Duration{time.Second, 2 * time.Second}) {
		t.Fatalf("download retries = %v, counts = %v, waits = %v", err, f.counts(), *waits)
	}
	assertNoCredentials(t, f.requests("download:/file"))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDownloadSSRFBlocked(t *testing.T) {
	_, c, waits := testClient(t)
	calls := 0
	c.files.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected request")
	})
	link := "http://169.254.169.254/x"
	_, err := c.download(t.Context(), link, 1, 4)
	if !errors.Is(err, errSSRFBlocked) || !errors.Is(err, datasource.ErrFetchFailed) || calls != 0 || len(*waits) != 0 {
		t.Fatalf("download = %v, calls = %d", err, calls)
	}
	if strings.Contains(err.Error(), link) {
		t.Fatalf("blocked download leaked the URL: %v", err)
	}
}

func TestDownloadRejectsUserinfo(t *testing.T) {
	_, c, waits := testClient(t)
	calls := 0
	c.files.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected request")
	})
	_, err := c.download(t.Context(), "http://user:secret@localhost/files/x", 1, 4)
	if !errors.Is(err, errInvalidResponse) || calls != 0 || len(*waits) != 0 {
		t.Fatalf("download = %v, calls = %d", err, calls)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("download error leaked userinfo: %v", err)
	}
}

func TestNetworkRetries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failure  error
		failures int
		attempts int
	}{
		{"dial recovered", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New(testToken)}, 2, 3},
		{"dial exhausted", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New(testToken)}, 3, 3},
		{"timeout recovered", &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}, 2, 3},
		{"other error", errors.New(testToken), 3, 1},
		{"SSRF redirect", utils.ErrSSRFRedirectBlocked, 3, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, c, waits := testClient(t)
			base := c.api.Transport
			attempts := 0
			c.api.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				attempts++
				if attempts <= tc.failures {
					return nil, tc.failure
				}
				return base.RoundTrip(r)
			})
			err := c.ping(t.Context())
			if (err == nil) != (tc.failures < tc.attempts) || attempts != tc.attempts || len(*waits) != tc.attempts-1 {
				t.Fatalf("ping = %v, attempts = %d, waits = %v", err, attempts, *waits)
			}
			if err != nil && strings.Contains(err.Error(), testToken) {
				t.Fatalf("transport error leaked the token: %v", err)
			}
			if err != nil && !errors.Is(err, datasource.ErrFetchFailed) {
				t.Fatalf("transport error must wrap ErrFetchFailed: %v", err)
			}
		})
	}
}

func TestCanceledContext(t *testing.T) {
	f, c, waits := testClient(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := c.ping(ctx)
	if !errors.Is(err, context.Canceled) || f.count("ping") != 0 || len(*waits) != 0 {
		t.Fatalf("canceled ping = %v, counts = %v", err, f.counts())
	}
	if err := sleepCtx(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleepCtx ignored cancellation: %v", err)
	}
}

func TestRedirectPolicy(t *testing.T) {
	t.Run("download redirected to private address", func(t *testing.T) {
		f, c, waits := testClient(t)
		f.addFile(testRepoID, "/file", []byte("body"), 1)
		f.redirects["download:/file"] = "http://169.254.169.254/private-signed-link"
		link, _, err := c.downloadLink(t.Context(), testRepoID, "/file")
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.download(t.Context(), link, 4, 4)
		if !errors.Is(err, errSSRFBlocked) || f.count("download:/file") != 1 || len(*waits) != 0 {
			t.Fatalf("redirect = %v, counts = %v", err, f.counts())
		}
		if strings.Contains(err.Error(), "private-signed-link") {
			t.Fatalf("redirect error leaked the target: %v", err)
		}
	})
	t.Run("api redirected to private address", func(t *testing.T) {
		f, c, _ := testClient(t)
		f.redirects["repos"] = "http://169.254.169.254/"
		_, err := c.listRepos(t.Context())
		if !errors.Is(err, datasource.ErrFetchFailed) || errors.Is(err, datasource.ErrInvalidCredentials) {
			t.Fatalf("listRepos = %v", err)
		}
	})
	t.Run("cross-origin hop strips the token", func(t *testing.T) {
		f, c, _ := testClient(t)
		f.addFile(testRepoID, "/pong", []byte(`"pong"`), 1)
		link, _, err := c.downloadLink(t.Context(), testRepoID, "/pong")
		if err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		f.redirects["ping"] = link
		f.mu.Unlock()
		if err := c.ping(t.Context()); err != nil {
			t.Fatal(err)
		}
		requests := f.requests("download:/pong")
		if len(requests) != 1 {
			t.Fatalf("download requests = %d", len(requests))
		}
		assertNoCredentials(t, requests)
	})
}
