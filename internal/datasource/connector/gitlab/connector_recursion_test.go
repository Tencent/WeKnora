package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// Exercise the real tree and raw-file APIs, including a subdirectory returned
// on the second page. Browsing remains shallow; fetching must visit every level.
func TestConnectorFetchesNestedDirectories(t *testing.T) {
	for _, mode := range []string{
		"all", "incremental", "stream", "incremental compare fallback", "stream compare fallback",
	} {
		t.Run(mode, func(t *testing.T) {
			allowLocalGitLabServer(t)
			dirs := []string{"docs", "docs/a", "docs/a/b", "docs/a/b/c", "docs/a/b/c/d"}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v4/projects/1":
					_, _ = w.Write([]byte(`{
						"id":1,"name":"docs","path_with_namespace":"group/docs","default_branch":"main"
					}`))
				case "/api/v4/projects/1/repository/commits/main":
					_, _ = w.Write([]byte(`{"id":"head"}`))
				case "/api/v4/projects/1/repository/compare":
					_, _ = w.Write([]byte(`{"compare_timeout":true}`))
				case "/api/v4/projects/1/repository/tree":
					dir := r.URL.Query().Get("path")
					index := slices.Index(dirs, dir)
					if index < 0 || r.URL.Query().Get("ref") != "main" {
						http.Error(w, "unexpected tree scope", http.StatusBadRequest)
						return
					}
					var entries []treeEntry
					switch r.URL.Query().Get("page") {
					case "1":
						entries = append(entries, treeEntry{Name: "README.md", Path: dir + "/README.md", Type: "blob"})
						if index < len(dirs)-1 {
							w.Header().Set("X-Next-Page", "2")
						}
					case "2":
						if index < len(dirs)-1 {
							child := dirs[index+1]
							entries = append(entries, treeEntry{Name: path.Base(child), Path: child, Type: "tree"})
						}
					default:
						http.Error(w, "unexpected page", http.StatusBadRequest)
						return
					}
					_ = json.NewEncoder(w).Encode(entries)
				default:
					if strings.HasPrefix(r.URL.Path, "/api/v4/projects/1/repository/files/docs/") &&
						strings.HasSuffix(r.URL.Path, "/raw") {
						_, _ = fmt.Fprint(w, "# same content")
						return
					}
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			cfg := &types.DataSourceConfig{
				Credentials: map[string]interface{}{"base_url": server.URL, "access_token": "token"},
				Settings: map[string]interface{}{"projects": []interface{}{
					map[string]interface{}{"project_id": "1", "paths": []interface{}{"docs", "docs/a"}},
				}},
			}
			connector := NewConnector()
			var old *types.SyncCursor
			if strings.Contains(mode, "fallback") {
				old = gitLabCursor(cursor{Projects: map[string]string{"1": "previous"}})
			}
			var items []types.FetchedItem
			var err error
			switch {
			case mode == "all":
				items, err = connector.FetchAll(context.Background(), cfg, nil)
			case strings.HasPrefix(mode, "incremental"):
				items, _, err = connector.FetchIncremental(context.Background(), cfg, old)
			default:
				handler := &gitLabStreamRecorder{}
				_, err = connector.FetchStream(context.Background(), cfg, old, handler)
				items = handler.items
				require.Len(t, handler.checkpoints, 1)
			}
			require.NoError(t, err)
			require.Len(t, items, len(dirs), "overlapping selected roots must not duplicate files")
			for i, dir := range dirs {
				require.Equal(t, "docs-main/"+dir+"/README.md", items[i].FileName)
				require.Equal(t, dir+"/README.md", items[i].Metadata["gitlab_path"])
			}

			resources, err := connector.ListResources(context.Background(), cfg, "1:docs")
			require.NoError(t, err)
			require.Len(t, resources, 1)
			require.Equal(t, "1:docs/a", resources[0].ExternalID)
		})
	}
}
