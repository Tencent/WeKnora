package feishu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/im"
)

// testRegion returns a Lark-shaped region pointed at a fake Open Platform, so
// the whole send path can be driven without touching the real cloud. Region
// carries the host as a field precisely so this is possible.
func testRegion(baseURL string) Region {
	r := RegionLark
	r.OpenBaseURL = baseURL
	return r
}

// fakeOpenPlatform records what the adapter asked for, and answers as the real
// Open Platform would.
type fakeOpenPlatform struct {
	mu sync.Mutex
	// paths records every request path in order.
	paths []string
	// replyAuth is the Authorization header seen on the reply call.
	replyAuth string
	// replyBody is the decoded JSON body of the reply call.
	replyBody map[string]any
	// sendBody is the decoded JSON body of the fallback send call.
	sendBody map[string]any
	// sendQuery is the raw query string of the fallback send call.
	sendQuery string
	// replyCode is returned from the reply endpoint (0 = success).
	replyCode int
}

func (f *fakeOpenPlatform) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.Path)
		f.mu.Unlock()

		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "tenant_access_token": "t-lark-xyz", "expire": 7200,
			})

		case strings.HasSuffix(r.URL.Path, "/reply"):
			f.mu.Lock()
			f.replyAuth = r.Header.Get("Authorization")
			_ = json.NewDecoder(r.Body).Decode(&f.replyBody)
			code := f.replyCode
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "msg": "mock"})

		case r.URL.Path == "/open-apis/im/v1/messages":
			f.mu.Lock()
			f.sendQuery = r.URL.RawQuery
			_ = json.NewDecoder(r.Body).Decode(&f.sendBody)
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok"})

		default:
			http.NotFound(w, r)
		}
	})
}

func (f *fakeOpenPlatform) sawPath(want string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.paths {
		if p == want {
			return true
		}
	}
	return false
}

// SendReply must fetch a token from the region's cloud and reply under the
// original message, carrying that token as a bearer credential.
func TestSendReply_EndToEnd_UsesRegionCloud(t *testing.T) {
	fake := &fakeOpenPlatform{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	a := NewAdapter(testRegion(srv.URL), "cli_app", "secret", "", "")
	incoming := &im.IncomingMessage{
		Platform:  im.PlatformLark,
		UserID:    "ou_user1",
		ChatType:  im.ChatTypeDirect,
		MessageID: "om_msg1",
	}

	if err := a.SendReply(context.Background(), incoming, &im.ReplyMessage{Content: "hello lark"}); err != nil {
		t.Fatalf("SendReply: %v", err)
	}

	if !fake.sawPath("/open-apis/auth/v3/tenant_access_token/internal") {
		t.Errorf("no token fetch; paths = %v", fake.paths)
	}
	if !fake.sawPath("/open-apis/im/v1/messages/om_msg1/reply") {
		t.Errorf("reply did not target the message; paths = %v", fake.paths)
	}
	if fake.replyAuth != "Bearer t-lark-xyz" {
		t.Errorf("reply Authorization = %q, want %q", fake.replyAuth, "Bearer t-lark-xyz")
	}
	if got := fake.replyBody["msg_type"]; got != "text" {
		t.Errorf("msg_type = %v, want text", got)
	}
	// content is a JSON-encoded string, per the Open Platform contract.
	var content struct {
		Text string `json:"text"`
	}
	raw, _ := fake.replyBody["content"].(string)
	if err := json.Unmarshal([]byte(raw), &content); err != nil {
		t.Fatalf("content is not a JSON string: %q", raw)
	}
	if content.Text != "hello lark" {
		t.Errorf("text = %q, want %q", content.Text, "hello lark")
	}
}

// A group that rejects reply-in-thread (230071) must still receive the answer
// via the plain send-message API, addressed to the chat.
func TestSendReply_EndToEnd_FallsBackToSendAPI(t *testing.T) {
	fake := &fakeOpenPlatform{replyCode: 230071}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	a := NewAdapter(testRegion(srv.URL), "cli_app", "secret", "", "")
	incoming := &im.IncomingMessage{
		Platform:  im.PlatformLark,
		UserID:    "ou_user1",
		ChatID:    "oc_chat1",
		ChatType:  im.ChatTypeGroup,
		MessageID: "om_msg1",
	}

	if err := a.SendReply(context.Background(), incoming, &im.ReplyMessage{Content: "grouped"}); err != nil {
		t.Fatalf("SendReply: %v", err)
	}

	if !fake.sawPath("/open-apis/im/v1/messages") {
		t.Fatalf("no fallback to send-message API; paths = %v", fake.paths)
	}
	if fake.sendQuery != "receive_id_type=chat_id" {
		t.Errorf("send query = %q, want receive_id_type=chat_id", fake.sendQuery)
	}
	if got := fake.sendBody["receive_id"]; got != "oc_chat1" {
		t.Errorf("receive_id = %v, want oc_chat1", got)
	}
}

// A non-fallback-eligible error must surface, not be silently swallowed.
func TestSendReply_EndToEnd_HardErrorSurfaces(t *testing.T) {
	fake := &fakeOpenPlatform{replyCode: 99991663} // app ticket invalid — not retryable
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	a := NewAdapter(testRegion(srv.URL), "cli_app", "secret", "", "")
	incoming := &im.IncomingMessage{
		Platform: im.PlatformLark, UserID: "ou_user1",
		ChatType: im.ChatTypeDirect, MessageID: "om_msg1",
	}

	err := a.SendReply(context.Background(), incoming, &im.ReplyMessage{Content: "x"})
	if err == nil {
		t.Fatal("SendReply returned nil for a non-retryable API error")
	}
	if !strings.Contains(err.Error(), "99991663") {
		t.Errorf("error %q does not mention the API code", err)
	}
	// The plain send API must not be tried for a hard error.
	if fake.sawPath("/open-apis/im/v1/messages") {
		t.Errorf("fell back to send-message API on a non-retryable error; paths = %v", fake.paths)
	}
}
