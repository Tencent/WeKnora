package whatsapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/im"
)

const (
	testPhoneNumberID = "106540352242922"
	testAppSecret     = "app-secret"
	testVerifyToken   = "verify-token"
)

func newTestAdapter() *Adapter {
	return NewAdapter("access-token", testPhoneNumberID, testAppSecret, testVerifyToken)
}

func newTestContext(method, target, body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	return c, rec
}

func sign(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

func textPayload(phoneNumberID, messageType string) string {
	return `{"object":"whatsapp_business_account","entry":[{"id":"1","changes":[{"field":"messages","value":{
		"messaging_product":"whatsapp",
		"metadata":{"display_phone_number":"15550001111","phone_number_id":"` + phoneNumberID + `"},
		"contacts":[{"profile":{"name":"Alice"},"wa_id":"33612345678"}],
		"messages":[{"from":"33612345678","id":"wamid.ABC","timestamp":"1700000000",
			"type":"` + messageType + `","text":{"body":" hello "}}]
	}}]}]}`
}

func TestHandleURLVerification(t *testing.T) {
	a := newTestAdapter()

	c, rec := newTestContext(http.MethodGet,
		"/cb?hub.mode=subscribe&hub.verify_token="+testVerifyToken+"&hub.challenge=1158201444", "")
	if !a.HandleURLVerification(c) {
		t.Fatal("GET handshake must be handled")
	}
	if rec.Code != http.StatusOK || rec.Body.String() != "1158201444" {
		t.Errorf("got %d %q, want 200 with the challenge echoed", rec.Code, rec.Body.String())
	}

	c, rec = newTestContext(http.MethodGet, "/cb?hub.mode=subscribe&hub.verify_token=wrong&hub.challenge=1", "")
	if !a.HandleURLVerification(c) || rec.Code != http.StatusForbidden {
		t.Errorf("wrong verify token: got %d, want 403", rec.Code)
	}

	// An adapter without a verify token must never accept a handshake.
	c, rec = newTestContext(http.MethodGet, "/cb?hub.mode=subscribe&hub.verify_token=&hub.challenge=1", "")
	if !(&Adapter{}).HandleURLVerification(c) || rec.Code != http.StatusForbidden {
		t.Errorf("empty verify token: got %d, want 403", rec.Code)
	}

	c, _ = newTestContext(http.MethodPost, "/cb", "{}")
	if a.HandleURLVerification(c) {
		t.Error("POST events must not be treated as a handshake")
	}
}

func TestVerifyCallback(t *testing.T) {
	a := newTestAdapter()
	body := textPayload(testPhoneNumberID, "text")

	c, _ := newTestContext(http.MethodPost, "/cb", body)
	c.Request.Header.Set(signatureHeader, sign(testAppSecret, body))
	if err := a.VerifyCallback(c); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	// The body must still be readable by ParseCallback afterwards.
	msg, err := a.ParseCallback(c)
	if err != nil || msg == nil {
		t.Fatalf("ParseCallback after VerifyCallback: msg=%v err=%v", msg, err)
	}

	for name, header := range map[string]string{
		"missing":      "",
		"wrong secret": sign("other-secret", body),
		"wrong body":   sign(testAppSecret, body+" "),
		"not hex":      signaturePrefix + "zz",
		"no prefix":    strings.TrimPrefix(sign(testAppSecret, body), signaturePrefix),
	} {
		c, _ := newTestContext(http.MethodPost, "/cb", body)
		if header != "" {
			c.Request.Header.Set(signatureHeader, header)
		}
		if err := a.VerifyCallback(c); err == nil {
			t.Errorf("%s signature accepted", name)
		}
	}
}

func TestParseCallback_TextMessage(t *testing.T) {
	c, _ := newTestContext(http.MethodPost, "/cb", textPayload(testPhoneNumberID, "text"))
	msg, err := newTestAdapter().ParseCallback(c)
	if err != nil {
		t.Fatal(err)
	}
	if msg == nil {
		t.Fatal("expected a message")
	}

	want := im.IncomingMessage{
		Platform:    im.PlatformWhatsApp,
		MessageType: im.MessageTypeText,
		UserID:      "33612345678",
		UserName:    "Alice",
		ChatType:    im.ChatTypeDirect,
		Content:     "hello",
		MessageID:   "wamid.ABC",
	}
	if msg.Platform != want.Platform || msg.MessageType != want.MessageType || msg.UserID != want.UserID ||
		msg.UserName != want.UserName || msg.ChatType != want.ChatType || msg.Content != want.Content ||
		msg.MessageID != want.MessageID || msg.ChatID != "" {
		t.Errorf("got %+v, want %+v", *msg, want)
	}
}

func TestParseCallback_IgnoredEvents(t *testing.T) {
	for name, body := range map[string]string{
		"other phone number": textPayload("999", "text"),
		"non-text message":   textPayload(testPhoneNumberID, "image"),
		"status update": `{"object":"whatsapp_business_account","entry":[{"changes":[{"field":"messages","value":{
			"metadata":{"phone_number_id":"` + testPhoneNumberID + `"},
			"statuses":[{"id":"wamid.ABC","status":"delivered"}]}}]}]}`,
		"other object": `{"object":"page","entry":[]}`,
	} {
		c, _ := newTestContext(http.MethodPost, "/cb", body)
		msg, err := newTestAdapter().ParseCallback(c)
		if err != nil || msg != nil {
			t.Errorf("%s: got msg=%v err=%v, want nil, nil", name, msg, err)
		}
	}

	c, _ := newTestContext(http.MethodPost, "/cb", "not json")
	if _, err := newTestAdapter().ParseCallback(c); err == nil {
		t.Error("invalid JSON must be an error")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestSendReply(t *testing.T) {
	a := newTestAdapter()
	var bodies []map[string]any
	a.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got, want := req.URL.String(), graphAPIBaseURL+"/"+testPhoneNumberID+"/messages"; got != want {
			t.Errorf("URL = %s, want %s", got, want)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Errorf("Authorization = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, body)
		return jsonResponse(http.StatusOK, `{"messages":[{"id":"wamid.OUT"}]}`), nil
	})}

	incoming := &im.IncomingMessage{UserID: "33612345678"}
	if err := a.SendReply(context.Background(), incoming, &im.ReplyMessage{Content: "**Hi** there"}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 {
		t.Fatalf("sent %d messages, want 1", len(bodies))
	}
	if bodies[0]["to"] != "33612345678" || bodies[0]["type"] != "text" || bodies[0]["messaging_product"] != "whatsapp" {
		t.Errorf("unexpected body: %v", bodies[0])
	}
	if text := bodies[0]["text"].(map[string]any)["body"]; text != "*Hi* there" {
		t.Errorf("text body = %q", text)
	}

	// A reply above the WhatsApp limit is delivered as several messages.
	bodies = nil
	long := strings.Repeat("a", maxTextLength) + "\n" + strings.Repeat("b", 10)
	if err := a.SendReply(context.Background(), incoming, &im.ReplyMessage{Content: long}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 {
		t.Errorf("sent %d messages, want 2", len(bodies))
	}

	if err := a.SendReply(context.Background(), &im.IncomingMessage{}, &im.ReplyMessage{Content: "x"}); err == nil {
		t.Error("missing recipient must be an error")
	}
}

func TestStream_TypingIndicatorThenAnswer(t *testing.T) {
	a := newTestAdapter()
	var bodies []map[string]any
	a.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, body)
		return jsonResponse(http.StatusOK, `{"success":true}`), nil
	})}

	ctx := context.Background()
	incoming := &im.IncomingMessage{UserID: "33612345678", MessageID: "wamid.IN"}
	if !a.SupportsFullOutputProgress() {
		t.Fatal("full-output progress must be supported")
	}

	streamID, err := a.StartStream(ctx, incoming)
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 || bodies[0]["status"] != "read" || bodies[0]["message_id"] != "wamid.IN" ||
		bodies[0]["typing_indicator"].(map[string]any)["type"] != "text" {
		t.Fatalf("unexpected typing indicator request: %v", bodies)
	}

	// Intermediate content is never sent: a WhatsApp message cannot be edited.
	if err := a.UpdateStreamContent(ctx, incoming, streamID, "partial"); err != nil {
		t.Fatal(err)
	}
	if err := a.FinalizeStream(ctx, incoming, streamID, "**Done**"); err != nil {
		t.Fatal(err)
	}
	if err := a.EndStream(ctx, incoming, streamID); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 {
		t.Fatalf("sent %d requests, want typing indicator + answer", len(bodies))
	}
	if text := bodies[1]["text"].(map[string]any)["body"]; text != "*Done*" {
		t.Errorf("answer body = %q", text)
	}

	if _, err := a.StartStream(ctx, &im.IncomingMessage{UserID: "1"}); err == nil {
		t.Error("missing message ID must be an error")
	}
}

// The indicator expires after 25 seconds, so it is refreshed while the answer
// is being prepared and stops once the stream ends.
func TestStream_TypingIndicatorIsRefreshed(t *testing.T) {
	prev := typingRefreshInterval
	typingRefreshInterval = 5 * time.Millisecond
	defer func() { typingRefreshInterval = prev }()

	a := newTestAdapter()
	var calls atomic.Int32
	a.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return jsonResponse(http.StatusOK, `{"success":true}`), nil
	})}

	ctx := context.Background()
	incoming := &im.IncomingMessage{UserID: "33612345678", MessageID: "wamid.IN"}
	streamID, err := a.StartStream(ctx, incoming)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := calls.Load(); got < 3 {
		t.Fatalf("typing indicator sent %d times, want it refreshed", got)
	}

	if err := a.EndStream(ctx, incoming, streamID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // let an in-flight refresh settle
	settled := calls.Load()
	time.Sleep(30 * time.Millisecond)
	if got := calls.Load(); got != settled {
		t.Errorf("typing indicator still refreshed after EndStream (%d -> %d)", settled, got)
	}
}

func TestSendReply_APIError(t *testing.T) {
	a := newTestAdapter()
	a.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusUnauthorized,
			`{"error":{"message":"Invalid OAuth access token","type":"OAuthException","code":190}}`), nil
	})}

	err := a.SendReply(context.Background(), &im.IncomingMessage{UserID: "1"}, &im.ReplyMessage{Content: "x"})
	if err == nil || !strings.Contains(err.Error(), "Invalid OAuth access token") ||
		!strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v, want the Graph API status and message", err)
	}
}

func TestToWhatsAppText(t *testing.T) {
	for in, want := range map[string]string{
		"plain text":                      "plain text",
		"**bold** and __bold__":           "*bold* and *bold*",
		"~~gone~~":                        "~gone~",
		"## Title":                        "*Title*",
		"### **Already bold** ##":         "*Already bold*",
		"see [the docs](https://x.io/a)":  "see the docs (https://x.io/a)",
		"[https://x.io](https://x.io)":    "https://x.io",
		"![chart](https://x.io/c.png)":    "chart (https://x.io/c.png)",
		"- item\n- **two**":               "- item\n- *two*",
		"a\n```\n**raw** # x\n```\n**b**": "a\n```\n**raw** # x\n```\n*b*",
	} {
		if got := toWhatsAppText(in); got != want {
			t.Errorf("toWhatsAppText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitText(t *testing.T) {
	if got := splitText("", 10); got != nil {
		t.Errorf("empty text: got %v, want nil", got)
	}
	if got := splitText("short", 10); len(got) != 1 || got[0] != "short" {
		t.Errorf("short text: got %v", got)
	}

	// Breaks on the last newline inside the window.
	got := splitText("aaaa\nbbbb\ncccc", 10)
	if len(got) != 2 || got[0] != "aaaa\nbbbb" || got[1] != "cccc" {
		t.Errorf("line split: got %q", got)
	}

	// No newline available: hard cut, counted in characters rather than bytes.
	got = splitText(strings.Repeat("é", 25), 10)
	if len(got) != 3 {
		t.Fatalf("hard split: got %d chunks, want 3", len(got))
	}
	for _, chunk := range got {
		if utf8.RuneCountInString(chunk) > 10 || !utf8.ValidString(chunk) {
			t.Errorf("chunk %q exceeds the limit or is not valid UTF-8", chunk)
		}
	}
}
