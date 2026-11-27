package im

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

func TestParseAdminDigestConfig(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		enabled bool
		wantIDs []string
	}{
		{name: "empty disables", raw: "", enabled: false, wantIDs: nil},
		{name: "empty object disables", raw: "{}", enabled: false, wantIDs: nil},
		{name: "invalid JSON disables", raw: "{nope", enabled: false, wantIDs: nil},
		{name: "enabled without ids", raw: `{"enabled": true}`, enabled: true, wantIDs: []string{}},
		{
			name:    "trims dedupes and keeps order",
			raw:     `{"enabled": true, "user_ids": [" ou_1 ", "ou_2", "ou_1", "", "ou_3"]}`,
			enabled: true,
			wantIDs: []string{"ou_1", "ou_2", "ou_3"},
		},
		{
			name:    "caps at ten admins",
			raw:     `{"enabled": true, "user_ids": ["a1","a2","a3","a4","a5","a6","a7","a8","a9","a10","a11","a12"]}`,
			enabled: true,
			wantIDs: []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9", "a10"},
		},
		{
			name:    "explicitly disabled with ids stays disabled",
			raw:     `{"enabled": false, "user_ids": ["ou_1"]}`,
			enabled: false,
			wantIDs: []string{"ou_1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseAdminDigestConfig(types.JSON(tt.raw))
			if got.Enabled != tt.enabled {
				t.Errorf("Enabled = %v, want %v", got.Enabled, tt.enabled)
			}
			if len(got.UserIDs) != len(tt.wantIDs) {
				t.Fatalf("UserIDs = %v, want %v", got.UserIDs, tt.wantIDs)
			}
			for i, id := range tt.wantIDs {
				if got.UserIDs[i] != id {
					t.Errorf("UserIDs[%d] = %q, want %q", i, got.UserIDs[i], id)
				}
			}
		})
	}
}

func TestTruncateDigestRunes(t *testing.T) {
	if got := truncateDigestRunes("hello", 10); got != "hello" {
		t.Errorf("short string = %q, want %q", got, "hello")
	}
	if got := truncateDigestRunes("hello world", 5); got != "hello…" {
		t.Errorf("truncated = %q, want %q", got, "hello…")
	}
	// CJK counts as one rune each, not three bytes.
	if got := truncateDigestRunes("你好世界你好", 4); got != "你好世界…" {
		t.Errorf("CJK truncated = %q, want %q", got, "你好世界…")
	}
	if got := truncateDigestRunes("hello", 0); got != "hello" {
		t.Errorf("non-positive max = %q, want unchanged", got)
	}
}

func TestAdminDigestSupportedPlatform(t *testing.T) {
	supported := []struct{ platform, mode string }{
		{"feishu", "websocket"},
		{"lark", "websocket"},
		{"slack", "websocket"},
		{"telegram", "websocket"},
		{"qqbot", "websocket"},
		{"yunzhijia", "webhook"},
		{"wecom", "webhook"},
	}
	for _, tt := range supported {
		if !adminDigestSupportedPlatform(tt.platform, tt.mode) {
			t.Errorf("expected %s/%s to support admin digest", tt.platform, tt.mode)
		}
	}
	unsupported := []struct{ platform, mode string }{
		{"dingtalk", "websocket"},
		{"wechat", "longpoll"},
		{"mattermost", "webhook"},
		{"wecom", "websocket"},
		{"wecom", ""},
		{"unknown", "websocket"},
	}
	for _, tt := range unsupported {
		if adminDigestSupportedPlatform(tt.platform, tt.mode) {
			t.Errorf("expected %s/%s NOT to support admin digest", tt.platform, tt.mode)
		}
	}
}

func TestBuildAdminDigestText(t *testing.T) {
	channel := &IMChannel{ID: "ch-1", Name: "客服助手", Platform: "feishu"}
	msg := &IncomingMessage{UserID: "ou_user", UserName: "张三", Content: "怎么退货？"}
	got := buildAdminDigestText(channel, msg, "sess-1", "请在订单页申请退货。")
	wants := []string{"客服助手", "feishu", "张三", "怎么退货？", "请在订单页申请退货。", "sess-1"}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("digest text missing %q:\n%s", want, got)
		}
	}
	// Long question/answer are truncated to excerpts, not dumped in full.
	long := strings.Repeat("问", 400)
	got = buildAdminDigestText(channel, msg, "sess-1", strings.Repeat("答", 600))
	if strings.Contains(got, long) {
		t.Error("digest contains the full 400-rune question, want truncation")
	}
	if !strings.Contains(got, "…") {
		t.Error("digest has no truncation marker for over-long content")
	}
	// Empty channel name falls back to the channel ID; empty user name to the user ID.
	channel.Name = ""
	msg.UserName = ""
	got = buildAdminDigestText(channel, msg, "sess-1", "答")
	if !strings.Contains(got, "ch-1") || !strings.Contains(got, "ou_user") {
		t.Errorf("digest fallbacks missing:\n%s", got)
	}
}

// adminDigestFakeAdapter records synthetic digest sends.
type adminDigestFakeAdapter struct {
	sent    []adminDigestSent
	sendErr error
}

type adminDigestSent struct {
	userID   string
	chatType ChatType
	content  string
}

func (a *adminDigestFakeAdapter) Platform() Platform                  { return PlatformFeishu }
func (a *adminDigestFakeAdapter) VerifyCallback(_ *gin.Context) error { return nil }
func (a *adminDigestFakeAdapter) ParseCallback(_ *gin.Context) (*IncomingMessage, error) {
	return nil, nil
}
func (a *adminDigestFakeAdapter) HandleURLVerification(_ *gin.Context) bool { return false }
func (a *adminDigestFakeAdapter) SendReply(ctx context.Context, incoming *IncomingMessage, reply *ReplyMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.sendErr != nil {
		return a.sendErr
	}
	content := ""
	if reply != nil {
		content = reply.Content
	}
	a.sent = append(a.sent, adminDigestSent{userID: incoming.UserID, chatType: incoming.ChatType, content: content})
	return nil
}

func digestChannel(platform, mode, raw string) *IMChannel {
	return &IMChannel{ID: "ch-1", Name: "客服助手", Platform: platform, Mode: mode, AdminDigest: types.JSON(raw)}
}

func TestSendAdminDigest(t *testing.T) {
	service := &Service{}
	msg := &IncomingMessage{Platform: PlatformFeishu, UserID: "ou_user", UserName: "张三", Content: "怎么退货？"}
	channel := digestChannel("feishu", "websocket", `{"enabled": true, "user_ids": ["ou_admin1", "ou_admin2"]}`)

	adapter := &adminDigestFakeAdapter{}
	service.sendAdminDigest(context.Background(), adapter, channel, msg, "sess-1", "请在订单页申请退货。")
	if len(adapter.sent) != 2 {
		t.Fatalf("sent %d digests, want 2", len(adapter.sent))
	}
	for i, want := range []string{"ou_admin1", "ou_admin2"} {
		if adapter.sent[i].userID != want {
			t.Errorf("sent[%d].userID = %q, want %q", i, adapter.sent[i].userID, want)
		}
		if adapter.sent[i].chatType != ChatTypeDirect {
			t.Errorf("sent[%d].chatType = %q, want direct", i, adapter.sent[i].chatType)
		}
		content := adapter.sent[i].content
		if !strings.Contains(content, "怎么退货？") || !strings.Contains(content, "sess-1") {
			t.Errorf("sent[%d].content missing question/session:\n%s", i, adapter.sent[i].content)
		}
	}
}

func TestSendAdminDigestSkips(t *testing.T) {
	service := &Service{}
	msg := &IncomingMessage{Platform: PlatformFeishu, UserID: "ou_user", Content: "怎么退货？"}
	cases := []struct {
		name     string
		platform string
		mode     string
		raw      string
		msg      *IncomingMessage
		answer   string
	}{
		{
			name: "disabled", platform: "feishu", mode: "websocket",
			raw: `{"enabled": false, "user_ids": ["ou_a"]}`, msg: msg, answer: "答",
		},
		{
			name: "no admins", platform: "feishu", mode: "websocket",
			raw: `{"enabled": true, "user_ids": []}`, msg: msg, answer: "答",
		},
		{name: "bad json", platform: "feishu", mode: "websocket", raw: `{broken`, msg: msg, answer: "答"},
		{
			name: "unsupported platform", platform: "dingtalk", mode: "websocket",
			raw: `{"enabled": true, "user_ids": ["ou_a"]}`, msg: msg, answer: "答",
		},
		{
			name: "wecom websocket", platform: "wecom", mode: "websocket",
			raw: `{"enabled": true, "user_ids": ["ou_a"]}`, msg: msg, answer: "答",
		},
		{
			name: "empty answer", platform: "feishu", mode: "websocket",
			raw: `{"enabled": true, "user_ids": ["ou_a"]}`, msg: msg, answer: "  ",
		},
		{
			name: "sender is the admin", platform: "feishu", mode: "websocket",
			raw: `{"enabled": true, "user_ids": ["ou_user"]}`, msg: msg, answer: "答",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			adapter := &adminDigestFakeAdapter{}
			channel := digestChannel(tt.platform, tt.mode, tt.raw)
			service.sendAdminDigest(context.Background(), adapter, channel, tt.msg, "sess-1", tt.answer)
			if len(adapter.sent) != 0 {
				t.Errorf("sent %d digests, want 0", len(adapter.sent))
			}
		})
	}
}

func TestSendAdminDigestSendErrorDoesNotFail(t *testing.T) {
	service := &Service{}
	msg := &IncomingMessage{Platform: PlatformFeishu, UserID: "ou_user", Content: "怎么退货？"}
	channel := digestChannel("feishu", "websocket", `{"enabled": true, "user_ids": ["ou_a", "ou_b"]}`)
	adapter := &adminDigestFakeAdapter{sendErr: errors.New("platform down")}
	// Must not panic and must still attempt the second admin.
	service.sendAdminDigest(context.Background(), adapter, channel, msg, "sess-1", "答")
	if len(adapter.sent) != 0 {
		t.Errorf("failed sends recorded %d digests, want 0", len(adapter.sent))
	}
}
