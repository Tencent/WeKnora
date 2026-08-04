package im

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"gorm.io/gorm"
)

func TestValidateSessionMode(t *testing.T) {
	tests := []struct {
		name        string
		sessionMode string
		wantErr     bool
	}{
		{"user mode", "user", false},
		{"thread mode", "thread", false},
		{"empty defaults to user in BeforeCreate", "", false},
		{"invalid mode", "invalid", true},
		{"random string", "foo", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := &IMChannel{SessionMode: tt.sessionMode}
			err := ch.validateSessionMode()

			if tt.sessionMode == "" {
				// empty is handled by BeforeCreate, not validateSessionMode
				// validateSessionMode treats empty as invalid
				if err == nil {
					t.Error("expected error for empty session_mode in validateSessionMode")
				}
				return
			}

			if (err != nil) != tt.wantErr {
				t.Errorf("validateSessionMode(%q) error = %v, wantErr %v", tt.sessionMode, err, tt.wantErr)
			}
		})
	}
}

func TestIMChannelBeforeCreate_SessionModeDefault(t *testing.T) {
	tests := []struct {
		name         string
		inputMode    string
		expectedMode string
	}{
		{"empty defaults to user", "", "user"},
		{"user preserved", "user", "user"},
		{"thread preserved", "thread", "thread"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := &IMChannel{
				TenantID:    1,
				AgentID:     "agent-1",
				Platform:    "slack",
				SessionMode: tt.inputMode,
				Credentials: []byte("{}"),
			}
			err := ch.BeforeCreate(&gorm.DB{})
			if err != nil {
				t.Fatalf("BeforeCreate error: %v", err)
			}
			if ch.SessionMode != tt.expectedMode {
				t.Errorf("SessionMode = %q, want %q", ch.SessionMode, tt.expectedMode)
			}
		})
	}
}

func TestIMChannelBeforeCreate_InvalidSessionMode(t *testing.T) {
	ch := &IMChannel{
		TenantID:    1,
		AgentID:     "agent-1",
		Platform:    "slack",
		SessionMode: "invalid",
		Credentials: []byte("{}"),
	}
	err := ch.BeforeCreate(&gorm.DB{})
	if err == nil {
		t.Error("expected error for invalid session_mode")
	}
}

func TestIMChannelBeforeSave_SessionModeValidation(t *testing.T) {
	tests := []struct {
		name        string
		sessionMode string
		wantMode    string
		wantErr     bool
	}{
		{"empty defaults to user", "", "user", false},
		{"user preserved", "user", "user", false},
		{"thread preserved", "thread", "thread", false},
		{"invalid rejected", "invalid", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := &IMChannel{
				SessionMode: tt.sessionMode,
				Credentials: []byte("{}"),
			}
			err := ch.BeforeSave(&gorm.DB{})
			if tt.wantErr {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ch.SessionMode != tt.wantMode {
				t.Errorf("SessionMode = %q, want %q", ch.SessionMode, tt.wantMode)
			}
		})
	}
}

func TestSessionModeConstants(t *testing.T) {
	if SessionModeUser != "user" {
		t.Errorf("SessionModeUser = %q, want %q", SessionModeUser, "user")
	}
	if SessionModeThread != "thread" {
		t.Errorf("SessionModeThread = %q, want %q", SessionModeThread, "thread")
	}
}

// BotIdentity carries a unique index, so Feishu and Lark channels must never
// derive the same identity — otherwise a Lark bot would be rejected as a
// duplicate of an unrelated Feishu bot that happens to share an app_id.
func TestComputeBotIdentity_FeishuAndLarkAreDistinct(t *testing.T) {
	const creds = `{"app_id":"cli_a1b2c3","app_secret":"s"}`

	feishu := &IMChannel{Platform: "feishu", Credentials: []byte(creds)}
	lark := &IMChannel{Platform: "lark", Credentials: []byte(creds)}

	feishuID := feishu.computeBotIdentity()
	larkID := lark.computeBotIdentity()

	if feishuID != "feishu:cli_a1b2c3" {
		t.Errorf("feishu identity = %q, want %q", feishuID, "feishu:cli_a1b2c3")
	}
	if larkID != "lark:cli_a1b2c3" {
		t.Errorf("lark identity = %q, want %q", larkID, "lark:cli_a1b2c3")
	}
	if feishuID == larkID {
		t.Errorf("feishu and lark share identity %q", feishuID)
	}
}

func TestComputeBotIdentity_LarkWithoutAppID(t *testing.T) {
	ch := &IMChannel{Platform: "lark", Credentials: []byte(`{"app_secret":"s"}`)}
	if got := ch.computeBotIdentity(); got != "" {
		t.Errorf("identity = %q, want empty when app_id is missing", got)
	}
}

func TestIMChannelComputeBotIdentity_YunzhijiaUsesYZJToken(t *testing.T) {
	makeChannel := func(sendMsgURL string) *IMChannel {
		return &IMChannel{
			Platform: "yunzhijia",
			Credentials: []byte(fmt.Sprintf(
				`{"send_msg_url":%q}`,
				sendMsgURL,
			)),
		}
	}

	first := makeChannel("https://open.yunzhijia.com/gateway/robot/webhook/send?yzjtoken=token-a&foo=1")
	second := makeChannel("https://open.yunzhijia.com/gateway/robot/webhook/send?yzjtoken=token-b&foo=1")
	firstWant := fmt.Sprintf("yunzhijia:%x", sha256.Sum256([]byte("token-a")))
	secondWant := fmt.Sprintf("yunzhijia:%x", sha256.Sum256([]byte("token-b")))
	if first.computeBotIdentity() != firstWant {
		t.Errorf("first identity = %q, want %q", first.computeBotIdentity(), firstWant)
	}
	if second.computeBotIdentity() != secondWant {
		t.Errorf("second identity = %q, want %q", second.computeBotIdentity(), secondWant)
	}
	if first.computeBotIdentity() == second.computeBotIdentity() {
		t.Error("different yzjtoken values must produce different Yunzhijia bot identities")
	}

	sameTokenDifferentQuery := makeChannel(
		"https://open.yunzhijia.com/gateway/robot/webhook/send?foo=2&yzjtoken=token-a",
	)
	if sameTokenDifferentQuery.computeBotIdentity() != first.computeBotIdentity() {
		t.Error("same yzjtoken must produce the same Yunzhijia bot identity")
	}

	missingToken := makeChannel("https://open.yunzhijia.com/gateway/robot/webhook/send?foo=1")
	if missingToken.computeBotIdentity() != "" {
		t.Errorf("missing yzjtoken identity = %q, want empty", missingToken.computeBotIdentity())
	}
}

func TestChannelSessionThreadIDField(t *testing.T) {
	cs := ChannelSession{
		Platform: "slack",
		UserID:   "U123",
		ChatID:   "C456",
		ThreadID: "1234567890.123456",
	}
	if cs.ThreadID != "1234567890.123456" {
		t.Errorf("ThreadID = %q, want %q", cs.ThreadID, "1234567890.123456")
	}

	// empty ThreadID for user-mode sessions
	csUser := ChannelSession{
		Platform: "slack",
		UserID:   "U123",
		ChatID:   "C456",
	}
	if csUser.ThreadID != "" {
		t.Errorf("ThreadID = %q, want empty", csUser.ThreadID)
	}
}
