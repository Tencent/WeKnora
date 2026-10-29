package line

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/im"
)

// newTestContext builds a gin.Context bound to a recorder with the given
// POST body. It mirrors how the IM webhook router feeds a request.
func newTestContext(t *testing.T, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/callback", bytes.NewReader([]byte(body)))
	c.Request = req
	return c, w
}

// TestVerifySignatureHMAC confirms HMAC-SHA256 over the raw body matches the
// expected Base64 signature, and that VerifyCallback rejects tampering.
func TestVerifySignatureHMAC(t *testing.T) {
	secret := "secret-key-123"
	adapter := NewWebhookAdapter("access-token", secret)

	body := []byte(`{"events":[{"type":"message"}]}`)
	// Independently compute the expected signature (oracle for verifySignature).
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	cases := []struct {
		name       string
		signature  string
		wantErr    bool
	}{
		{"valid signature", expected, false},
		{"missing header", "", true},
		{"wrong signature", "not-a-valid-signature", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestContext(t, string(body))
			if tc.signature != "" {
				c.Request.Header.Set("X-Line-Signature", tc.signature)
			}
			err := adapter.VerifyCallback(c)
			if tc.wantErr && err == nil {
				t.Fatalf("expected signature error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestParseCallbackTextMessage decodes a LINE text message into IncomingMessage.
func TestParseCallbackTextMessage(t *testing.T) {
	adapter := NewWebhookAdapter("access-token", "secret-key-123")

	const payload = `{
		"destination": "U-destination",
		"events": [
			{
				"type": "message",
				"token": "tok-1",
				"source": {"type": "user", "userId": "U-abcd123"},
				"replyToken": "rtk-987",
				"message": {"type": "Text", "id": "msg-555", "text": " 你好 世界 "}
			}
		]
	}`

	c, _ := newTestContext(t, payload)
	incoming, err := adapter.ParseCallback(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if incoming == nil {
		t.Fatal("expected IncomingMessage, got nil")
	}
	if incoming.Platform != im.PlatformLine {
		t.Errorf("Platform = %q, want %q", incoming.Platform, im.PlatformLine)
	}
	if incoming.UserID != "U-abcd123" {
		t.Errorf("UserID = %q, want U-abcd123", incoming.UserID)
	}
	if incoming.Content != "你好 世界" {
		t.Errorf("Content = %q, want trimmed text", incoming.Content)
	}
	if incoming.MessageID != "msg-555" {
		t.Errorf("MessageID = %q, want msg-555", incoming.MessageID)
	}
	if incoming.ChatType != im.ChatTypeDirect {
		t.Errorf("ChatType = %q, want direct", incoming.ChatType)
	}
	if incoming.Extra["replyToken"] != "rtk-987" {
		t.Errorf("replyToken extra = %q, want rtk-987", incoming.Extra["replyToken"])
	}
}

// TestParseCallbackChatMessage verifies group/room mapping to ChatTypeGroup.
func TestParseCallbackChatMessage(t *testing.T) {
	adapter := NewWebhookAdapter("access-token", "secret-key-123")

	const payload = `{
		"events": [
			{
				"type": "message",
				"source": {"type": "group", "userId": "", "groupId": "G-42"},
				"replyToken": "rtk-g",
				"message": {"type": "Text", "id": "msg-g", "text": "ping"}
			}
		]
	}`

	c, _ := newTestContext(t, payload)
	incoming, err := adapter.ParseCallback(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if incoming == nil {
		t.Fatal("expected IncomingMessage, got nil")
	}
	if incoming.ChatType != im.ChatTypeGroup {
		t.Errorf("ChatType = %q, want group", incoming.ChatType)
	}
	if incoming.ChatID != "G-42" {
		t.Errorf("ChatID = %q, want G-42", incoming.ChatID)
	}
}

// TestParseCallbackEmptyEvents confirms a payload without events yields no message.
func TestParseCallbackEmptyEvents(t *testing.T) {
	adapter := NewWebhookAdapter("access-token", "secret-key-123")

	c, _ := newTestContext(t, `{"destination":"U-", "events":[]}`)
	incoming, err := adapter.ParseCallback(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if incoming != nil {
		t.Fatalf("expected nil message for empty events, got %+v", incoming)
	}
}

// TestHandleURLVerificationNoEvents checks that a request without events is
// treated as verification (HTTP 200, handled = true).
func TestHandleURLVerificationNoEvents(t *testing.T) {
	adapter := NewWebhookAdapter("access-token", "secret-key-123")

	c, w := newTestContext(t, `{"destination":"U-"}`)
	handled := adapter.HandleURLVerification(c)
	if !handled {
		t.Fatal("expected HandleURLVerification = true for no-events request")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// TestHandleURLVerificationWithEvents ensures a real message event is NOT
// reported as a verification request.
func TestHandleURLVerificationWithEvents(t *testing.T) {
	adapter := NewWebhookAdapter("access-token", "secret-key-123")

	const payload = `{"events":[{"type":"message","message":{"type":"Text","text":"hi"}}]}`
	c, _ := newTestContext(t, payload)
	handled := adapter.HandleURLVerification(c)
	if handled {
		t.Fatal("expected HandleURLVerification = false when events present")
	}
}

// TestPlatform confirms the adapter reports the LINE platform.
func TestPlatform(t *testing.T) {
	adapter := NewWebhookAdapter("access-token", "secret")
	if adapter.Platform() != im.PlatformLine {
		t.Errorf("Platform() = %q, want %q", adapter.Platform(), im.PlatformLine)
	}
}

// TestNewFactoryRequiresCredentials verifies the factory rejects missing creds.
func TestNewFactoryRequiresCredentials(t *testing.T) {
	factory := NewFactory()

	_, _, err := factory(context.Background(), &im.IMChannel{
		Credentials: []byte(`{"channel_access_token":""}`),
	}, nil)
	if err == nil {
		t.Fatal("expected error for empty channel_access_token")
	}

	adapter, cancel, err := factory(context.Background(), &im.IMChannel{
		Credentials: []byte(`{"channel_access_token":"tok","channel_secret":"sec"}`),
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cancel != nil {
		defer cancel()
	}
	if adapter == nil {
		t.Fatal("expected non-nil adapter for valid credentials")
	}
}
