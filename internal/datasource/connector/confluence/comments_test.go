package confluence

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFetchStreamConfluenceComments(t *testing.T) {
	for _, cloud := range []bool{false, true} {
		name := "Server"
		if cloud {
			name = "Cloud"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			api := &streamAPI{cloud: cloud, pages: []streamPage{{id: "p1", title: "Guide", version: 1}}}
			cfg := streamConfig()
			endpoint := "/wiki/rest/api/content/p1/child/comment"
			if cloud {
				cfg = cloudStreamConfig()
				endpoint = "/wiki/api/v2/pages/p1/footer-comments"
			}
			cfg.Settings = map[string]interface{}{"sync_comments": true}
			comment := "<p>The <strong>release</strong> ships Friday.</p>"
			if cloud {
				comment += "<pre><code>deploy --day friday</code></pre>"
			}
			includeComment := true
			secondPageStatus := http.StatusOK
			detailStatus := http.StatusOK
			commentCalls := 0
			detailCalls := 0
			connector := &Connector{newClient: func(cfg config) (*client, error) {
				return &client{cfg: cfg, http: &http.Client{Transport: roundTripper(
					func(req *http.Request) (*http.Response, error) {
						if cloud && req.URL.Path == "/wiki/api/v2/footer-comments/c2" {
							detailCalls++
							require.Equal(t, "view", req.URL.Query().Get("body-format"))
							res, err := api.jsonResponse(map[string]any{
								"id": "c2", "body": map[string]any{"view": map[string]string{"value": comment}},
							})
							res.StatusCode = detailStatus
							return res, err
						}
						if req.URL.Path != endpoint {
							return api.response(req)
						}
						commentCalls++
						query := req.URL.Query()
						if cloud {
							require.Empty(t, query.Get("body-format"))
							require.Equal(t, "current", query.Get("status"))
						} else {
							require.Equal(t, "body.view", query.Get("expand"))
							require.Equal(t, "footer", query.Get("location"))
							require.Equal(t, "root", query.Get("depth"))
						}
						if query.Get("start") != "" || query.Get("cursor") != "" {
							if secondPageStatus != http.StatusOK {
								res, err := api.jsonResponse(nil)
								res.StatusCode = secondPageStatus
								return res, err
							}
							item := map[string]any{"id": "c2"}
							if !cloud {
								item["body"] = map[string]any{"view": map[string]string{"value": comment}}
							}
							return api.jsonResponse(map[string]any{"results": []any{item}})
						}
						result := map[string]any{"results": []any{}}
						if includeComment {
							next := "?start=1"
							if cloud {
								next = "?cursor=next"
							}
							result["_links"] = map[string]string{"next": next}
						}
						return api.jsonResponse(result)
					},
				)}}, nil
			}}

			original := streamCursor(map[string]string{"p1": "v:1"})
			first := &captureHandler{}
			cursor, err := connector.FetchStream(ctx, cfg, original, first)
			require.NoError(t, err)
			require.Len(t, first.items, 1)
			require.Contains(t, string(first.items[0].Content), "Guide\n\n## Comments")
			require.Contains(t, string(first.items[0].Content), "The **release** ships Friday.")
			if cloud {
				require.Contains(t, string(first.items[0].Content), "deploy --day friday")
			}
			require.True(t, first.items[0].RetryOnFailure)
			require.Equal(t, 2, commentCalls)
			require.Equal(t, 1, api.bodyCalls)
			if cloud {
				require.Equal(t, 1, detailCalls)
			}

			unchanged := &captureHandler{}
			_, err = connector.FetchStream(ctx, cfg, cursor, unchanged)
			require.NoError(t, err)
			require.Empty(t, unchanged.items)
			require.Equal(t, 1, api.bodyCalls)

			secondPageStatus = http.StatusForbidden
			failed := &captureHandler{}
			_, err = connector.FetchStream(ctx, cfg, cursor, failed)
			require.ErrorContains(t, err, "status 403")
			require.Empty(t, failed.items)
			require.Empty(t, failed.checkpoints)

			secondPageStatus = http.StatusOK
			if cloud {
				detailStatus = http.StatusForbidden
				failedDetail := &captureHandler{}
				_, err = connector.FetchStream(ctx, cfg, cursor, failedDetail)
				require.ErrorContains(t, err, "status 403")
				require.Empty(t, failedDetail.items)
				require.Empty(t, failedDetail.checkpoints)
				detailStatus = http.StatusOK
			}
			editedText := "ships Monday"
			if cloud {
				// A code-only edit must re-emit the page even though its version and prose are unchanged.
				comment = strings.Replace(comment, "deploy --day friday", "deploy --day monday", 1)
				editedText = "deploy --day monday"
			} else {
				comment = "<p>The release ships Monday.</p>"
			}
			edited := &captureHandler{}
			cursor, err = connector.FetchStream(ctx, cfg, cursor, edited)
			require.NoError(t, err)
			require.Len(t, edited.items, 1)
			require.Contains(t, string(edited.items[0].Content), editedText)
			if cloud {
				require.Contains(t, string(edited.items[0].Content), "The **release** ships Friday.")
				require.NotContains(t, string(edited.items[0].Content), "deploy --day friday")
			}
			require.Equal(t, first.items[0].ExternalID, edited.items[0].ExternalID)

			includeComment = false
			deleted := &captureHandler{}
			_, err = connector.FetchStream(ctx, cfg, cursor, deleted)
			require.NoError(t, err)
			require.Len(t, deleted.items, 1)
			require.False(t, deleted.items[0].IsDeleted)
			require.Equal(t, "Guide", strings.TrimSpace(string(deleted.items[0].Content)))

			delete(cfg.Settings, "sync_comments")
			callsBefore := commentCalls
			disabled := &captureHandler{}
			_, err = connector.FetchStream(ctx, cfg, cursor, disabled)
			require.NoError(t, err)
			require.Len(t, disabled.items, 1)
			require.True(t, disabled.items[0].RetryOnFailure)
			require.Equal(t, "Guide", strings.TrimSpace(string(disabled.items[0].Content)))
			require.Equal(t, callsBefore, commentCalls)
		})
	}
}
