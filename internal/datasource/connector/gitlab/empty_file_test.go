package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// An emptied file is still a file. Ingestion treats an item with neither content
// nor URL as nothing to ingest and acknowledges the sync, so emitting the
// emptied blob verbatim leaves the body indexed from an earlier commit
// searchable forever — and no later sync revisits it, because a full sync still
// sees the path in the tree and an incremental sync has already moved its head
// past the commit that emptied it.
//
// The Notion (b9be7d0d), Confluence and IMA (b71b31c8) connectors emit the
// document's title in this case so ingestion replaces the stale body. GitLab
// does the same with the file's path, on both sync paths that can observe the
// change.
//
// Only a zero-byte blob is rewritten. A blob holding whitespace carries content
// the repository still has, so its bytes must reach ingestion untouched.
func TestConnectorEmitsPathWhenAFileIsEmptied(t *testing.T) {
	for _, mode := range []string{"full", "incremental", "stream"} {
		t.Run(mode, func(t *testing.T) {
			allowLocalGitLabServer(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v4/projects/1":
					_, _ = w.Write([]byte(`{
						"id":1,"name":"docs","path_with_namespace":"group/docs",
						"web_url":"https://gitlab.test/group/docs","default_branch":"main"
					}`))
				case "/api/v4/projects/1/repository/commits/main":
					_, _ = w.Write([]byte(`{"id":"head"}`))
				case "/api/v4/projects/1/repository/compare":
					_, _ = w.Write([]byte(`{"diffs":[
						{"old_path":"runbook.md","new_path":"runbook.md"},
						{"old_path":"indented.md","new_path":"indented.md"},
						{"old_path":"kept.md","new_path":"kept.md"}
					]}`))
				case "/api/v4/projects/1/repository/tree":
					_, _ = w.Write([]byte(`[
						{"name":"runbook.md","path":"runbook.md","type":"blob"},
						{"name":"indented.md","path":"indented.md","type":"blob"},
						{"name":"kept.md","path":"kept.md","type":"blob"}
					]`))
				default:
					if strings.Contains(r.URL.Path, "/repository/files/") {
						switch {
						case strings.Contains(r.URL.Path, "runbook.md"):
							// 200 with no bytes at all: the emptied commit.
						case strings.Contains(r.URL.Path, "indented.md"):
							_, _ = w.Write([]byte("  \n\t\n"))
						default:
							_, _ = w.Write([]byte("# still here"))
						}
						return
					}
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			cfg := &types.DataSourceConfig{
				Credentials: map[string]interface{}{"base_url": server.URL, "access_token": "token"},
				Settings: map[string]interface{}{"projects": []interface{}{
					map[string]interface{}{"project_id": "1", "paths": []interface{}{}},
				}},
			}
			connector := NewConnector()
			var items []types.FetchedItem
			var err error

			switch mode {
			case "full":
				items, err = connector.FetchAll(context.Background(), cfg, nil)
			case "incremental":
				items, _, err = connector.FetchIncremental(
					context.Background(), cfg, gitLabCursor(cursor{Projects: map[string]string{"1": "previous"}}))
			default:
				handler := &gitLabStreamRecorder{}
				_, err = connector.FetchStream(
					context.Background(), cfg,
					gitLabCursor(cursor{Projects: map[string]string{"1": "previous"}}), handler)
				items = handler.items
			}
			require.NoError(t, err)
			require.Len(t, items, 3)

			byPath := make(map[string]types.FetchedItem, len(items))
			for _, item := range items {
				byPath[item.Metadata["gitlab_path"]] = item
			}

			emptied := byPath["runbook.md"]
			require.False(t, emptied.IsDeleted)
			require.Equal(t,
				"# group/docs/runbook.md\n",
				string(emptied.Content),
				"an emptied file must still reach ingestion, or its stale body stays searchable")
			require.Equal(t, "group/docs/runbook.md", emptied.Title)

			// Whitespace is content: it must not be mistaken for an emptied file.
			require.Equal(t, "  \n\t\n", string(byPath["indented.md"].Content))

			require.Equal(t, "# still here", string(byPath["kept.md"].Content))
		})
	}
}
