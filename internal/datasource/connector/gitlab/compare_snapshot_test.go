package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectorRollbackEmitsSnapshotChanges(t *testing.T) {
	for _, mode := range []string{"incremental", "stream"} {
		t.Run(mode, func(t *testing.T) {
			allowLocalGitLabServer(t)
			server := rollbackGitLabServer(t)
			cfg := &types.DataSourceConfig{
				Credentials: map[string]interface{}{"base_url": server.URL, "access_token": "token"},
				Settings: map[string]interface{}{"projects": []interface{}{
					map[string]interface{}{"project_id": "1", "ref": "main"},
				}},
			}
			old := gitLabCursor(cursor{Projects: map[string]string{"1": "descendant"}})
			connector := NewConnector()
			var items []types.FetchedItem
			var next *types.SyncCursor
			var err error
			if mode == "incremental" {
				items, next, err = connector.FetchIncremental(context.Background(), cfg, old)
			} else {
				handler := &gitLabStreamRecorder{}
				next, err = connector.FetchStream(context.Background(), cfg, old, handler)
				items = handler.items
				require.Len(t, handler.checkpoints, 1)
			}
			require.NoError(t, err)
			require.Len(t, items, 2, "rollback must restore changed files and delete files absent from the target")
			require.Equal(t, []byte("# original README"), items[0].Content)
			require.Equal(t, "README.md", items[0].Metadata["gitlab_path"])
			require.True(t, items[1].IsDeleted)
			require.Equal(t, "added.md", items[1].Metadata["gitlab_path"])
			require.Equal(t, server.URL+"/api/v4:1:main:added.md", strings.TrimPrefix(items[1].ExternalID, "gitlab:"))
			require.Equal(t, "ancestor", next.ConnectorCursor["projects"].(map[string]string)["1"])
		})
	}
}

func rollbackGitLabServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v4/projects/1":
			_, _ = fmt.Fprint(w, `{"id":1,"name":"docs","default_branch":"main"}`)
		case "/api/v4/projects/1/repository/commits/main":
			_, _ = fmt.Fprint(w, `{"id":"ancestor"}`)
		case "/api/v4/projects/1/repository/compare":
			assert.Equal(t, "descendant", r.URL.Query().Get("from"))
			assert.Equal(t, "ancestor", r.URL.Query().Get("to"))
			// GitLab's default merge-base comparison returns no diffs when to
			// is an ancestor of from. Direct comparison returns the rollback.
			if r.URL.Query().Get("straight") != "true" {
				_, _ = fmt.Fprint(w, `{"diffs":[]}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"diffs":[
				{"old_path":"README.md","new_path":"README.md"},
				{"old_path":"added.md","new_path":"added.md","deleted_file":true}
			]}`)
		case "/api/v4/projects/1/repository/files/README.md/raw":
			assert.Equal(t, "main", r.URL.Query().Get("ref"))
			_, _ = fmt.Fprint(w, "# original README")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}
