package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestGitLabExpandedScopeImportsUnchangedFiles(t *testing.T) {
	for _, mode := range []string{"incremental", "stream"} {
		t.Run(mode, func(t *testing.T) {
			allowLocalGitLabServer(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v4/projects/1":
					_, _ = fmt.Fprint(w, `{"id":1,"name":"docs","default_branch":"main"}`)
				case "/api/v4/projects/1/repository/commits/main":
					_, _ = fmt.Fprint(w, `{"id":"unchanged"}`)
				case "/api/v4/projects/1/repository/tree":
					p := r.URL.Query().Get("path")
					_, _ = fmt.Fprintf(w, `[{"type":"blob","path":%q}]`, p+"/guide.md")
				default:
					_, _ = fmt.Fprint(w, "# guide")
				}
			}))
			defer server.Close()
			cfg := &types.DataSourceConfig{
				Credentials: map[string]interface{}{"base_url": server.URL, "access_token": "token"},
				Settings: map[string]interface{}{"projects": []interface{}{
					map[string]interface{}{"project_id": "1", "ref": "main", "paths": []interface{}{"old"}},
				}},
			}
			fetch := func(old *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor) {
				if mode == "stream" {
					h := &gitLabStreamRecorder{}
					next, err := NewConnector().FetchStream(context.Background(), cfg, old, h)
					require.NoError(t, err)
					return h.items, next
				}
				items, next, err := NewConnector().FetchIncremental(context.Background(), cfg, old)
				require.NoError(t, err)
				return items, next
			}
			items, next := fetch(nil)
			require.Len(t, items, 1)
			proj := cfg.Settings["projects"].([]interface{})[0].(map[string]interface{})
			proj["paths"] = []interface{}{"old", "added"}
			items, _ = fetch(next)
			found := false
			for _, item := range items {
				found = found || item.Metadata["gitlab_path"] == "added/guide.md"
			}
			require.True(t, found, "newly selected unchanged files must be imported")
		})
	}
}

// TestGitLabExpandedScopeWithNewCommitImportsAddedAndChangedOnce verifies that
// expanding paths in the same sync window as a new commit imports existing
// untouched files under the newly selected root, emits diff changes and
// deletions, and does not duplicate files touched in the newly added root.
func TestGitLabExpandedScopeWithNewCommitImportsAddedAndChangedOnce(t *testing.T) {
	for _, mode := range []string{"incremental", "stream"} {
		t.Run(mode, func(t *testing.T) {
			allowLocalGitLabServer(t)
			head := "c1"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v4/projects/1":
					_, _ = fmt.Fprint(w, `{"id":1,"name":"docs","default_branch":"main"}`)
				case "/api/v4/projects/1/repository/commits/main":
					_, _ = fmt.Fprintf(w, `{"id":%q}`, head)
				case "/api/v4/projects/1/repository/compare":
					_, _ = fmt.Fprint(w, `{"diffs":[
						{"old_path":"old/removed.md","new_path":"old/removed.md","deleted_file":true},
						{"old_path":"old/updated.md","new_path":"old/updated.md"},
						{"old_path":"added/touched.md","new_path":"added/touched.md"}
					]}`)
				case "/api/v4/projects/1/repository/tree":
					switch r.URL.Query().Get("path") {
					case "old":
						_, _ = fmt.Fprint(w, `[{"type":"blob","path":"old/updated.md"}]`)
					case "added":
						_, _ = fmt.Fprint(w, `[
							{"type":"blob","path":"added/touched.md"},
							{"type":"blob","path":"added/existing.md"}
						]`)
					default:
						http.NotFound(w, r)
					}
				default:
					_, _ = fmt.Fprint(w, "# content")
				}
			}))
			defer server.Close()

			proj := map[string]interface{}{
				"project_id": "1",
				"ref":        "main",
				"paths":      []interface{}{"old"},
			}
			cfg := &types.DataSourceConfig{
				Credentials: map[string]interface{}{"base_url": server.URL, "access_token": "token"},
				Settings:    map[string]interface{}{"projects": []interface{}{proj}},
			}
			fetch := func(old *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor) {
				if mode == "stream" {
					h := &gitLabStreamRecorder{}
					next, err := NewConnector().FetchStream(context.Background(), cfg, old, h)
					require.NoError(t, err)
					return h.items, next
				}
				items, next, err := NewConnector().FetchIncremental(context.Background(), cfg, old)
				require.NoError(t, err)
				return items, next
			}

			items, cursor1 := fetch(nil)
			require.Len(t, items, 1)

			// Advance HEAD and expand directory selection before the next sync.
			head = "c2"
			proj["paths"] = []interface{}{"old", "added"}
			items, cursor2 := fetch(cursor1)

			seen := map[string]int{}
			deleted := map[string]bool{}
			for _, item := range items {
				p := item.Metadata["gitlab_path"]
				if item.IsDeleted {
					deleted[p] = true
				} else {
					seen[p]++
				}
			}
			require.True(t, deleted["old/removed.md"], "deleted file in previous scope must be emitted")
			require.Equal(t, 1, seen["old/updated.md"], "updated file in previous scope must be emitted once")
			require.Equal(t, 1, seen["added/touched.md"], "touched file in added scope must not be duplicated")
			require.Equal(t, 1, seen["added/existing.md"], "untouched file in added scope must be imported")
			require.Len(t, seen, 3)

			// A third sync with unchanged commit and scope must emit nothing.
			items, _ = fetch(cursor2)
			require.Empty(t, items, "unchanged commit and scope must remain incremental")
		})
	}
}

// TestGitLabWidenedScopeToWholeRepoImportsRootAndKeepsDeletions verifies that
// widening from specific paths to the whole repository imports root files,
// keeps deletions from the commit diff, and records whole-repo coverage.
func TestGitLabWidenedScopeToWholeRepoImportsRootAndKeepsDeletions(t *testing.T) {
	for _, mode := range []string{"incremental", "stream"} {
		t.Run(mode, func(t *testing.T) {
			allowLocalGitLabServer(t)
			head := "c1"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v4/projects/1":
					_, _ = fmt.Fprint(w, `{"id":1,"name":"docs","default_branch":"main"}`)
				case "/api/v4/projects/1/repository/commits/main":
					_, _ = fmt.Fprintf(w, `{"id":%q}`, head)
				case "/api/v4/projects/1/repository/compare":
					_, _ = fmt.Fprint(w, `{"diffs":[
						{"old_path":"old/removed.md","new_path":"old/removed.md","deleted_file":true},
						{"old_path":"old/guide.md","new_path":"old/guide.md"}
					]}`)
				case "/api/v4/projects/1/repository/tree":
					switch r.URL.Query().Get("path") {
					case "old":
						_, _ = fmt.Fprint(w, `[{"type":"blob","path":"old/guide.md"}]`)
					case "":
						_, _ = fmt.Fprint(w, `[
							{"type":"blob","path":"README.md"},
							{"type":"blob","path":"old/guide.md"}
						]`)
					default:
						http.NotFound(w, r)
					}
				default:
					_, _ = fmt.Fprint(w, "# content")
				}
			}))
			defer server.Close()

			proj := map[string]interface{}{
				"project_id": "1",
				"ref":        "main",
				"paths":      []interface{}{"old"},
			}
			cfg := &types.DataSourceConfig{
				Credentials: map[string]interface{}{"base_url": server.URL, "access_token": "token"},
				Settings:    map[string]interface{}{"projects": []interface{}{proj}},
			}
			fetch := func(old *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor) {
				if mode == "stream" {
					h := &gitLabStreamRecorder{}
					next, err := NewConnector().FetchStream(context.Background(), cfg, old, h)
					require.NoError(t, err)
					return h.items, next
				}
				items, next, err := NewConnector().FetchIncremental(context.Background(), cfg, old)
				require.NoError(t, err)
				return items, next
			}

			items, cursor1 := fetch(nil)
			require.Len(t, items, 1)

			// Widen to whole repository while a new commit deletes old/removed.md.
			head = "c2"
			proj["paths"] = []interface{}{}
			items, cursor2 := fetch(cursor1)

			seen := map[string]int{}
			deleted := map[string]bool{}
			for _, item := range items {
				p := item.Metadata["gitlab_path"]
				if item.IsDeleted {
					deleted[p] = true
				} else {
					seen[p]++
				}
			}
			require.True(t, deleted["old/removed.md"])
			require.Equal(t, 1, seen["README.md"])
			require.Equal(t, 1, seen["old/guide.md"])
			require.Len(t, seen, 2)

			items, _ = fetch(cursor2)
			require.Empty(t, items, "whole-repo cursor must cover future unchanged syncs")
		})
	}
}

// TestGitLabLegacyCursorWithoutPathsReimportsOnce pins the upgrade path:
// cursors written before paths were recorded carry no scope information,
// so the next sync re-imports the current selection once (upsert by
// ExternalID) and records the scope; the sync after that is incremental.
func TestGitLabLegacyCursorWithoutPathsReimportsOnce(t *testing.T) {
	allowLocalGitLabServer(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v4/projects/1":
			_, _ = fmt.Fprint(w, `{"id":1,"name":"docs","default_branch":"main"}`)
		case "/api/v4/projects/1/repository/commits/main":
			_, _ = fmt.Fprint(w, `{"id":"unchanged"}`)
		case "/api/v4/projects/1/repository/tree":
			p := r.URL.Query().Get("path")
			_, _ = fmt.Fprintf(w, `[{"type":"blob","path":%q}]`, p+"/guide.md")
		default:
			_, _ = fmt.Fprint(w, "# guide")
		}
	}))
	defer server.Close()
	cfg := &types.DataSourceConfig{
		Credentials: map[string]interface{}{"base_url": server.URL, "access_token": "token"},
		Settings: map[string]interface{}{"projects": []interface{}{
			map[string]interface{}{"project_id": "1", "ref": "main", "paths": []interface{}{"old"}},
		}},
	}
	legacy := &types.SyncCursor{ConnectorCursor: map[string]interface{}{
		"projects": map[string]interface{}{"1": "unchanged"},
	}}
	items, next, err := NewConnector().FetchIncremental(context.Background(), cfg, legacy)
	require.NoError(t, err)
	require.Len(t, items, 1, "legacy cursor must re-import the current selection once")
	items, _, err = NewConnector().FetchIncremental(context.Background(), cfg, next)
	require.NoError(t, err)
	require.Empty(t, items, "selection unchanged after scope was recorded")
}
