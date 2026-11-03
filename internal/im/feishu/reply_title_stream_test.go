package feishu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/stretchr/testify/require"
)

func TestReplyTitleSurvivesStreamFinalization(t *testing.T) {
	useTestHTTPClient(t)
	for _, region := range []Region{RegionFeishu, RegionLark} {
		t.Run(region.Label, func(t *testing.T) {
			createdTitle := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				response := map[string]any{"code": 0}
				switch {
				case strings.Contains(r.URL.Path, "tenant_access_token"):
					response["tenant_access_token"] = "test"
					response["expire"] = 7200
				case r.URL.Path == "/open-apis/cardkit/v1/cards":
					var req struct {
						Data string `json:"data"`
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
						http.Error(w, "invalid request", 400)
						return
					}
					var card struct {
						Header struct{ Title struct{ Content string } }
					}
					if err := json.Unmarshal([]byte(req.Data), &card); err != nil {
						t.Error(err)
						http.Error(w, "invalid card", 400)
						return
					}
					createdTitle <- card.Header.Title.Content
					response["data"] = map[string]string{"card_id": "title-card"}
				case strings.HasSuffix(r.URL.Path, "/reply"):
				case strings.Contains(r.URL.Path, "/elements/"):
				case strings.HasSuffix(r.URL.Path, "/settings"):
				default:
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			region.OpenBaseURL = server.URL
			adapter, err := NewAdapter(region, "id", "secret", "", "", "")
			require.NoError(t, err)
			incoming := &im.IncomingMessage{
				MessageID: "om_test", UserID: "ou_test", ChatType: im.ChatTypeDirect,
				ReplyCardTitle: "Research <assistant>",
			}
			id, err := adapter.StartStream(context.Background(), incoming)
			require.NoError(t, err)
			t.Cleanup(func() { feishuStreamsMu.Lock(); delete(feishuStreams, id); feishuStreamsMu.Unlock() })
			incoming.ReplyCardTitle = "changed later"
			require.NoError(t, adapter.UpdateStreamContent(context.Background(), incoming, id, "working"))
			require.NoError(t, adapter.FinalizeStream(context.Background(), incoming, id, "final answer"))
			require.NoError(t, adapter.EndStream(context.Background(), incoming, id))
			require.Equal(t, "Research <assistant>", <-createdTitle)
		})
	}
}
