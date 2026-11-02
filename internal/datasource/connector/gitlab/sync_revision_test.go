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

// A branch can move immediately after its head is resolved. Every tree page
// and raw blob in this round must come from the recorded commit, while item
// identities continue to describe the tracked branch rather than that commit.
func TestSyncReadsRecordedRevision(t *testing.T) {
	for _, mode := range []string{"incremental", "stream"} {
		for _, initial := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/initial=%t", mode, initial), func(t *testing.T) {
				allowLocalGitLabServer(t)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/api/v4/projects/1":
						_, _ = fmt.Fprint(w, `{"id":1,"name":"docs","path_with_namespace":"group/docs",
"default_branch":"main","web_url":"https://gitlab.example/group/docs"}`)
					case "/api/v4/projects/1/repository/commits/main":
						_, _ = fmt.Fprint(w, `{"id":"snapshot-a"}`)
					case "/api/v4/projects/1/repository/tree":
						if r.URL.Query().Get("ref") != "snapshot-a" {
							// main advanced to B, which has a different set of files.
							_, _ = fmt.Fprint(w, `[{"type":"blob","path":"new-at-b.md","name":"new-at-b.md"}]`)
						} else {
							_, _ = fmt.Fprint(w, `[{"type":"blob","path":"README.md","name":"README.md"}]`)
						}
					case "/api/v4/projects/1/repository/compare":
						if r.URL.Query().Get("to") != "snapshot-a" {
							t.Error("compare did not use the recorded head")
						}
						_, _ = fmt.Fprint(w, `{"diffs":[{"old_path":"README.md","new_path":"README.md"}]}`)
					case "/api/v4/projects/1/repository/files/README.md/raw":
						if r.URL.Query().Get("ref") == "snapshot-a" {
							_, _ = fmt.Fprint(w, "content at A")
						} else {
							_, _ = fmt.Fprint(w, "content at B")
						}
					case "/api/v4/projects/1/repository/files/new-at-b.md/raw":
						_, _ = fmt.Fprint(w, "new content at B")
					default:
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				cfg := &types.DataSourceConfig{
					Credentials: map[string]interface{}{"base_url": server.URL, "access_token": "token"},
					Settings: map[string]interface{}{"projects": []interface{}{
						map[string]interface{}{"project_id": "1", "ref": "main"},
					}},
				}
				var old *types.SyncCursor
				if !initial {
					old = gitLabCursor(cursor{Projects: map[string]string{"1": "previous"}})
				}
				var items []types.FetchedItem
				var next *types.SyncCursor
				var err error
				if mode == "incremental" {
					items, next, err = NewConnector().FetchIncremental(context.Background(), cfg, old)
				} else {
					recorder := &gitLabStreamRecorder{}
					next, err = NewConnector().FetchStream(context.Background(), cfg, old, recorder)
					items = recorder.items
					require.Len(t, recorder.checkpoints, 1)
				}
				require.NoError(t, err)
				require.Len(t, items, 1)
				require.Equal(t, "README.md", items[0].Metadata["gitlab_path"])
				require.Equal(t, []byte("content at A"), items[0].Content)
				require.Equal(t, "main", items[0].Metadata["gitlab_ref"])
				require.Equal(t, "docs-main/README.md", items[0].FileName)
				require.Equal(t, "gitlab:"+server.URL+"/api/v4:1:main:README.md", items[0].ExternalID)
				require.Equal(t, "https://gitlab.example/group/docs/-/blob/main/README.md",
					items[0].Metadata["gitlab_url"])
				require.Equal(t, "snapshot-a", next.ConnectorCursor["projects"].(map[string]string)["1"])
			})
		}
	}
}
