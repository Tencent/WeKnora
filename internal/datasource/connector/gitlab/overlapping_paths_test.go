package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestOverlappingSelectionsFetchEachFileOnce(t *testing.T) {
	for _, mode := range []string{"all", "incremental", "stream"} {
		t.Run(mode, func(t *testing.T) {
			allowLocalGitLabServer(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v4/projects/1":
					_, _ = fmt.Fprint(w, `{"id":1,"name":"repo","default_branch":"main"}`)
				case "/api/v4/projects/1/repository/commits/main":
					_, _ = fmt.Fprint(w, `{"id":"head"}`)
				case "/api/v4/projects/1/repository/tree":
					var entries []treeEntry
					switch r.URL.Query().Get("path") {
					case "docs":
						entries = []treeEntry{{Type: "tree", Path: "docs/sub"}}
					case "docs/sub":
						entries = []treeEntry{{Type: "blob", Path: "docs/sub/guide.md"}}
					case "docs-old":
						entries = []treeEntry{{Type: "blob", Path: "docs-old/archive.md"}}
					default:
						http.NotFound(w, r)
						return
					}
					_ = json.NewEncoder(w).Encode(entries)
				default:
					_, _ = fmt.Fprint(w, "# repository content")
				}
			}))
			defer server.Close()
			cfg := &types.DataSourceConfig{
				Credentials: map[string]interface{}{"base_url": server.URL, "access_token": "token"},
				Settings: map[string]interface{}{"projects": []interface{}{map[string]interface{}{
					"project_id": "1", "paths": []interface{}{"docs", "docs-old", "docs/sub"},
				}}},
			}
			connector := NewConnector()
			var items []types.FetchedItem
			var err error
			switch mode {
			case "all":
				items, err = connector.FetchAll(context.Background(), cfg, nil)
			case "incremental":
				items, _, err = connector.FetchIncremental(context.Background(), cfg, nil)
			case "stream":
				handler := &gitLabStreamRecorder{}
				_, err = connector.FetchStream(context.Background(), cfg, nil, handler)
				items = handler.items
			}
			require.NoError(t, err)
			paths := make([]string, 0, len(items))
			for _, item := range items {
				paths = append(paths, item.Metadata["gitlab_path"])
			}
			require.ElementsMatch(t, []string{"docs/sub/guide.md", "docs-old/archive.md"}, paths)
		})
	}
}
