package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/stretchr/testify/require"
)

func TestTopicReplyPolicyDeliveryPaths(t *testing.T) {
	useTestHTTPClient(t)
	gin.SetMode(gin.TestMode)
	var groupReads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "tenant_access_token"):
			_, _ = w.Write([]byte(`{"code":0,"tenant_access_token":"test","expire":7200}`))
		case r.URL.Path == "/open-apis/bot/v3/info":
			_, _ = w.Write([]byte(`{"code":0,"bot":{"open_id":"ou_bot"}}`))
		case strings.Contains(r.URL.Path, "/chats/"):
			groupReads.Add(1)
			if r.Header.Get("Authorization") != "Bearer test" {
				t.Error("missing tenant token")
			}
			switch {
			case strings.HasSuffix(r.URL.Path, "/topic"):
				_, _ = w.Write([]byte(`{"code":0,"data":{"chat_mode":"topic"}}`))
			case strings.HasSuffix(r.URL.Path, "/thread"):
				_, _ = w.Write([]byte(`{"code":0,"data":{"chat_mode":"group","group_message_type":"thread"}}`))
			case strings.HasSuffix(r.URL.Path, "/ordinary"):
				_, _ = w.Write([]byte(`{"code":0,"data":{"chat_mode":"group","group_message_type":"chat"}}`))
			case strings.HasSuffix(r.URL.Path, "/unknown"):
				_, _ = w.Write([]byte(`{"code":0,"data":{"chat_mode":"group"}}`))
			case strings.HasSuffix(r.URL.Path, "/denied"):
				_, _ = w.Write([]byte(`{"code":99991672}`))
			default:
				http.Error(w, "unavailable", http.StatusForbidden)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	for _, region := range []Region{RegionFeishu, RegionLark} {
		region.OpenBaseURL = server.URL
		for _, tc := range []struct {
			name, chat, root, parent, sender, mention, kind string
			enabled, want, lookupError                      bool
		}{
			{name: "default topic", chat: "topic", sender: "user"},
			{name: "enabled topic", chat: "topic", sender: "user", enabled: true, want: true},
			{name: "thread group", chat: "thread", sender: "user", enabled: true, want: true},
			{
				name: "explicit topic type", chat: "topic", kind: "topic_group",
				sender: "user", enabled: true, want: true,
			},
			{name: "self root", chat: "topic", root: "om_message", sender: "user", enabled: true, want: true},
			{name: "ordinary", chat: "ordinary", sender: "user", enabled: true},
			{name: "reply", chat: "topic", root: "om_root", sender: "user", enabled: true},
			{name: "parent reply", chat: "topic", parent: "om_root", sender: "user", enabled: true},
			{
				name: "mentioned reply", chat: "topic", root: "om_root",
				sender: "user", mention: "ou_bot", enabled: true, want: true,
			},
			{name: "default mention", chat: "ordinary", sender: "user", mention: "ou_bot", want: true},
			{name: "other bot ordinary", chat: "ordinary", sender: "user", mention: "ou_other", enabled: true},
			{
				name: "other bot reply", chat: "topic", root: "om_root",
				sender: "user", mention: "ou_other", enabled: true,
			},
			{name: "bot sender", chat: "topic", sender: "app", enabled: true},
			{name: "unknown sender", chat: "topic", enabled: true},
			{name: "missing mode", chat: "unknown", sender: "user", enabled: true},
			{name: "permission denied", chat: "denied", sender: "user", enabled: true},
			{name: "http failure", chat: "failed", sender: "user", enabled: true},
			{name: "config failure", chat: "topic", sender: "user", enabled: true, lookupError: true},
			{name: "direct", chat: "ordinary", kind: "p2p", sender: "user", want: true},
		} {
			t.Run(region.Label+"/"+tc.name, func(t *testing.T) {
				a, err := NewAdapter(region, "id", "secret", "", "", "")
				require.NoError(t, err)
				a.SetTopicReplyPolicy(func(context.Context) (bool, error) {
					if tc.lookupError {
						return false, errors.New("unavailable")
					}
					return tc.enabled, nil
				})
				kind := tc.kind
				if kind == "" {
					kind = "group"
				}
				event := &larkim.P2MessageReceiveV1{Event: &larkim.P2MessageReceiveV1Data{
					Sender: &larkim.EventSender{
						SenderType: stringPtr(tc.sender), SenderId: &larkim.UserId{OpenId: stringPtr("ou_user")},
					},
					Message: &larkim.EventMessage{
						MessageType: stringPtr("text"), ChatType: stringPtr(kind), ChatId: stringPtr(tc.chat),
						MessageId: stringPtr("om_message"), RootId: stringPtr(tc.root), ParentId: stringPtr(tc.parent),
						Content: stringPtr(`{"text":"hello"}`),
					},
				}}
				if tc.mention != "" {
					event.Event.Message.Mentions = []*larkim.MentionEvent{{
						MentionedType: stringPtr("bot"),
						Id:            &larkim.UserId{OpenId: stringPtr(tc.mention)},
					}}
				}
				client := &LongConnClient{region: region, botOpenIDProvider: a}
				before := groupReads.Load()
				got := client.convertIncoming(context.Background(), event)
				require.Equal(t, tc.want, got != nil, "websocket")
				body, err := json.Marshal(map[string]any{
					"header": map[string]any{"event_type": "im.message.receive_v1"},
					"event":  event.Event,
				})
				require.NoError(t, err)
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader(string(body)))
				webhook, err := a.ParseCallback(c)
				require.NoError(t, err)
				require.Equal(t, tc.want, webhook != nil, "webhook")
				if got != nil {
					require.Equal(t, got.Content, webhook.Content)
					require.Equal(t, got.ChatType, webhook.ChatType)
				}
				if !tc.enabled || tc.root == "om_root" || tc.parent != "" || tc.sender != "user" || tc.lookupError {
					require.Equal(t, before, groupReads.Load(), "ineligible messages must not query group details")
				}
			})
		}
	}
}

func TestTopicReplyPolicyMissingOrDisabledProvider(t *testing.T) {
	a := &Adapter{botOpenID: "ou_bot"}
	allow, err := a.allowGroupMessage(context.Background(), "topic", "message", "", "", "user", nil)
	require.NoError(t, err)
	require.False(t, allow)
	a.SetTopicReplyPolicy(func(context.Context) (bool, error) { return false, nil })
	allow, err = a.allowGroupMessage(context.Background(), "topic", "message", "", "", "user", nil)
	require.NoError(t, err)
	require.False(t, allow)
}
