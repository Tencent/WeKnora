package yuque

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/logger"
)

// paginationHopCap mirrors maxPaginationHops in client.go. It is spelled as a
// literal so this file also compiles — and then fails on behaviour — against a
// tree that has no guard at all.
const paginationHopCap = 10000

// paginationBudget is how many requests the "always a full page" fakes below
// serve before answering 400. A walk that lacks a guard therefore fails on the
// budget error after paginationBudget+1 requests (and on the request-count
// assertion) instead of spinning until the go test timeout.
const paginationBudget = 300

// fullDocPage builds a full-ish page of docs with ids startID..startID+n-1.
func fullDocPage(startID int64, n int) []v2Doc {
	docs := make([]v2Doc, 0, n)
	for i := 0; i < n; i++ {
		docs = append(docs, v2Doc{
			ID:               startID + int64(i),
			Type:             "Doc",
			Status:           "1",
			Title:            fmt.Sprintf("D%d", startID+int64(i)),
			ContentUpdatedAt: "2026-04-20T00:00:00Z",
		})
	}
	return docs
}

// fullRepoPage builds a full-ish page of repos with ids startID..startID+n-1.
func fullRepoPage(startID int64, n int) []v2Repo {
	repos := make([]v2Repo, 0, n)
	for i := 0; i < n; i++ {
		repos = append(repos, v2Repo{
			ID:        startID + int64(i),
			Type:      "Book",
			Namespace: fmt.Sprintf("alice/book-%d", startID+int64(i)),
		})
	}
	return repos
}

// minimalPage renders a page of defaultPageSize id-only entries, so the hop cap
// test can serve 10000 pages without materialising gigabytes of JSON.
func minimalPage(prefix string, startID int64) string {
	var sb strings.Builder
	sb.WriteString(`{"data":[`)
	for i := 0; i < defaultPageSize; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `{"%s":%d}`, prefix, startID+int64(i))
	}
	sb.WriteString(`]}`)
	return sb.String()
}

// alwaysFullPageServer answers every offset with the same full page: a gateway
// or self-hosted deployment that ignores `offset`. Without a repeated-page
// guard the walk keeps appending identical entries.
func alwaysFullPageServer(t *testing.T, body interface{}) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) > paginationBudget {
			http.Error(w, "pagination guard missing: too many requests", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

// A docs listing whose meta.total is already covered by the first page must end
// there. The fake keeps answering a full page for every offset, so before the
// fix the walk ran until the fake's budget cut it off 300 requests later.
func TestListBookDocs_StopsAtMetaTotal(t *testing.T) {
	resp := v2DocListResponse{Data: fullDocPage(1, defaultPageSize)}
	resp.Meta.Total = 5 // far below what this page alone hands out
	server, requests := alwaysFullPageServer(t, resp)

	c := newClient(&Config{APIToken: "tok", BaseURL: server.URL})
	docs, err := c.ListBookDocs(context.Background(), 4242)

	require.NoError(t, err, "meta.total covered by the first page must end the walk")
	require.Len(t, docs, defaultPageSize)
	require.EqualValues(t, 1, requests.Load(),
		"the walk must stop on the page that reaches meta.total")
}

// A full page whose first id repeats the previous page means the offset walk is
// not making progress. Each endpoint must report that instead of appending the
// same page forever.
func TestPaginationStopsOnRepeatedPage(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		call func(context.Context, *client) (int, error)
	}{
		{
			name: "book docs without meta.total",
			path: "/api/v2/repos/4242/docs",
			call: func(ctx context.Context, c *client) (int, error) {
				docs, err := c.ListBookDocs(ctx, 4242)
				return len(docs), err
			},
		},
		{
			name: "user repos",
			path: "/api/v2/users/alice/repos",
			call: func(ctx context.Context, c *client) (int, error) {
				repos, err := c.ListUserRepos(ctx, "alice")
				return len(repos), err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body interface{}
			if strings.HasSuffix(tc.path, "/docs") {
				body = v2DocListResponse{Data: fullDocPage(1, defaultPageSize)}
			} else {
				body = v2RepoListResponse{Data: fullRepoPage(1, defaultPageSize)}
			}
			server, requests := alwaysFullPageServer(t, body)

			c := newClient(&Config{APIToken: "tok", BaseURL: server.URL})
			n, err := tc.call(context.Background(), c)

			require.Error(t, err, "a repeated full page must not be followed forever")
			require.Contains(t, err.Error(), "repeated page")
			require.Zero(t, n, "a stalled walk must not hand back a partial listing")
			require.LessOrEqual(t, requests.Load(), int64(2),
				"the guard must reject the repeated page on the second request")
		})
	}
}

// A server that never repeats an id but also never returns a short page can only
// be stopped by the hop cap. It must be reported, and the log must carry it too:
// visually this failure looks like a healthy, still-running sync.
func TestPaginationHopCapStopsWalk(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(context.Context, *client) error
	}{
		{
			name: "book docs",
			call: func(ctx context.Context, c *client) error {
				_, err := c.ListBookDocs(ctx, 4242)
				return err
			},
		},
		{
			name: "user repos",
			call: func(ctx context.Context, c *client) error {
				_, err := c.ListUserRepos(ctx, "alice")
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := requests.Add(1)
				if n > paginationHopCap+10 {
					http.Error(w, "hop cap missing: too many requests", http.StatusBadRequest)
					return
				}
				offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
				if err != nil {
					http.Error(w, "unexpected offset", http.StatusBadRequest)
					return
				}
				// Fresh ids on every page and no meta.total, so only the hop cap
				// can end the walk.
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(minimalPage("id", int64(offset)+1)))
			}))
			t.Cleanup(server.Close)

			c := newClient(&Config{APIToken: "tok", BaseURL: server.URL})

			var logs bytes.Buffer
			logger.SetOutput(&logs)
			t.Cleanup(logger.ConfigureFromEnv)

			err := tc.call(context.Background(), c)

			require.ErrorContains(t, err, "pagination exceeded 10000 pages")
			require.EqualValues(t, paginationHopCap, requests.Load())
			require.Contains(t, logs.String(), "pagination exceeded 10000 pages",
				"hitting the hop cap must leave a warning in the log")
		})
	}
}

// pagedServer serves `total` items in defaultPageSize-sized offset pages, with
// the docs response advertising meta.total. It is the well-behaved listing the
// guards must not disturb.
func pagedServer(t *testing.T, kind string, total int) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
		if err != nil {
			http.Error(w, "unexpected offset", http.StatusBadRequest)
			return
		}
		size := total - offset
		if size > defaultPageSize {
			size = defaultPageSize
		}
		if size < 0 {
			size = 0
		}
		w.Header().Set("Content-Type", "application/json")
		if kind == "docs" {
			resp := v2DocListResponse{Data: fullDocPage(int64(offset)+1, size)}
			resp.Meta.Total = total
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		_ = json.NewEncoder(w).Encode(v2RepoListResponse{Data: fullRepoPage(int64(offset)+1, size)})
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

// The guards must not truncate a listing that genuinely spans several pages.
func TestPaginationCollectsRealPages(t *testing.T) {
	for _, tc := range []struct {
		name     string
		kind     string
		total    int
		wantReqs int64
		call     func(context.Context, *client) (int, error)
	}{
		{
			name:     "docs reaching meta.total exactly",
			kind:     "docs",
			total:    2 * defaultPageSize,
			wantReqs: 2,
			call: func(ctx context.Context, c *client) (int, error) {
				docs, err := c.ListBookDocs(ctx, 4242)
				return len(docs), err
			},
		},
		{
			name:     "docs with a short final page",
			kind:     "docs",
			total:    defaultPageSize + 30,
			wantReqs: 2,
			call: func(ctx context.Context, c *client) (int, error) {
				docs, err := c.ListBookDocs(ctx, 4242)
				return len(docs), err
			},
		},
		{
			name:     "repos with a short final page",
			kind:     "repos",
			total:    defaultPageSize + 30,
			wantReqs: 2,
			call: func(ctx context.Context, c *client) (int, error) {
				repos, err := c.ListUserRepos(ctx, "alice")
				return len(repos), err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, requests := pagedServer(t, tc.kind, tc.total)

			c := newClient(&Config{APIToken: "tok", BaseURL: server.URL})
			got, err := tc.call(context.Background(), c)

			require.NoError(t, err)
			require.Equal(t, tc.total, got, "every item of a real multi-page listing must survive")
			require.EqualValues(t, tc.wantReqs, requests.Load())
		})
	}
}
