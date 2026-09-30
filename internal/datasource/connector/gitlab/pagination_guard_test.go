package gitlab

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/stretchr/testify/require"
)

// guardTestTimeout bounds every walk here so that a missing guard fails the
// test instead of hanging it.
const guardTestTimeout = 5 * time.Second

// repeatedPageServer always advertises the same X-Next-Page value and refuses
// to serve more than three requests: a walk without a repeated-page guard then
// fails quickly with an unexpected error instead of spinning until the context
// deadline.
func repeatedPageServer(t *testing.T) (*httptest.Server, *int64) {
	t.Helper()
	allowLocalGitLabServer(t)
	var requests int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&requests, 1) > 3 {
			http.Error(w, "pagination guard missing: too many requests", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Next-Page", "2")
		if strings.HasSuffix(r.URL.Path, "/repository/tree") {
			_, _ = w.Write([]byte(`[{"name":"one.md","type":"blob","path":"one.md"}]`))
			return
		}
		_, _ = w.Write([]byte(`[{"id":1,"path_with_namespace":"group/project-001"}]`))
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

func TestProjectsStopsOnRepeatedNextPage(t *testing.T) {
	server, requests := repeatedPageServer(t)
	c, err := newClient(server.URL, "test-token")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), guardTestTimeout)
	defer cancel()

	projects, err := c.projects(ctx)

	require.Error(t, err, "a repeated X-Next-Page must not be treated as the end of the list")
	require.Nil(t, projects)
	require.Contains(t, err.Error(), `gitlab project pagination repeated page "2"`)
	require.LessOrEqual(t, atomic.LoadInt64(requests), int64(3), "must stop before re-reading the repeated page")
}

func TestTreeStopsOnRepeatedNextPage(t *testing.T) {
	server, requests := repeatedPageServer(t)
	c, err := newClient(server.URL, "test-token")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), guardTestTimeout)
	defer cancel()

	entries, err := c.tree(ctx, "1", "main", "")

	require.Error(t, err, "a repeated X-Next-Page must not be treated as the end of the list")
	require.Nil(t, entries)
	require.Contains(t, err.Error(), `gitlab tree pagination repeated page "2"`)
	require.LessOrEqual(t, atomic.LoadInt64(requests), int64(3), "must stop before re-reading the repeated page")
}

// TestPaginationHopCapStopsWalk drives each walk with a server that always
// advertises a fresh page number, so only the hop cap can end it.
func TestPaginationHopCapStopsWalk(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
		walk func(context.Context, *client) error
	}{
		{
			name: "projects",
			want: "gitlab project pagination exceeded 10000 pages",
			walk: func(ctx context.Context, c *client) error {
				_, err := c.projects(ctx)
				return err
			},
		},
		{
			name: "tree",
			want: "gitlab tree pagination exceeded 10000 pages",
			walk: func(ctx context.Context, c *client) error {
				_, err := c.tree(ctx, "1", "main", "")
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allowLocalGitLabServer(t)
			var requests int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := atomic.AddInt64(&requests, 1)
				if n > maxPaginationHops+10 {
					http.Error(w, "hop cap missing: too many requests", http.StatusTooManyRequests)
					return
				}
				page, convErr := strconv.Atoi(r.URL.Query().Get("page"))
				if convErr != nil {
					http.Error(w, "unexpected page", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Next-Page", strconv.Itoa(page+1))
				_, _ = w.Write([]byte(`[]`))
			}))
			t.Cleanup(server.Close)
			c, err := newClient(server.URL, "test-token")
			require.NoError(t, err)

			var logs bytes.Buffer
			logger.SetOutput(&logs)
			t.Cleanup(logger.ConfigureFromEnv)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			err = tc.walk(ctx, c)

			require.EqualError(t, err, tc.want)
			require.Equal(t, int64(maxPaginationHops), atomic.LoadInt64(&requests))
			require.Contains(t, logs.String(), "pagination exceeded 10000 pages",
				"hitting the cap must leave a warning in the log")
		})
	}
}

func TestTrackPageAllowsForwardPages(t *testing.T) {
	seen := make(map[string]struct{})
	for _, page := range []string{"1", "2", "3"} {
		require.NoError(t, trackPage(context.Background(), "project", seen, page))
	}
	require.Error(t, trackPage(context.Background(), "project", seen, "2"))
}
