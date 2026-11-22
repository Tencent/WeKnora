package feishu

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/im"
)

type recordedReactionRequest struct {
	method string
	path   string
	auth   string
	body   string
}

// newReactionAdapter starts a fake Feishu API that answers the token endpoint
// like the real one and records every other request for assertions.
func newReactionAdapter(
	t *testing.T, respond func(path string, body []byte) (int, string),
) (*Adapter, *[]recordedReactionRequest) {
	t.Helper()
	useTestHTTPClient(t)
	var requests []recordedReactionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "tenant_access_token": "test-token", "expire": 7200,
			})
			return
		}
		raw, _ := io.ReadAll(r.Body)
		requests = append(requests, recordedReactionRequest{
			method: r.Method, path: r.URL.Path,
			auth: r.Header.Get("Authorization"), body: string(raw),
		})
		status, payload := respond(r.URL.Path, raw)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)
	adapter, err := NewAdapter(testRegion(srv.URL), "app", "secret", "", "", "")
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	return adapter, &requests
}

func reactionOKPayload(reactionID string) func(string, []byte) (int, string) {
	return func(string, []byte) (int, string) {
		return http.StatusOK, `{"code":0,"msg":"ok","data":{"reaction_id":"` + reactionID + `"}}`
	}
}

func reactionCodePayload(code int, msg string) func(string, []byte) (int, string) {
	return func(string, []byte) (int, string) {
		body, _ := json.Marshal(map[string]any{"code": code, "msg": msg})
		return http.StatusOK, string(body)
	}
}

func TestAddReaction_Success(t *testing.T) {
	adapter, requests := newReactionAdapter(t, reactionOKPayload("reaction-1"))
	ctx := context.Background()
	id, err := adapter.AddReaction(ctx, &im.IncomingMessage{MessageID: "om_123"}, "DONE")
	if err != nil {
		t.Fatalf("AddReaction: %v", err)
	}
	if id != "reaction-1" {
		t.Errorf("reaction ID = %q, want %q", id, "reaction-1")
	}
	if len(*requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(*requests))
	}
	req := (*requests)[0]
	if req.method != http.MethodPost || req.path != "/open-apis/im/v1/messages/om_123/reactions" {
		t.Errorf("request = %s %s, want POST /open-apis/im/v1/messages/om_123/reactions", req.method, req.path)
	}
	if req.auth != "Bearer test-token" {
		t.Errorf("Authorization = %q, want %q", req.auth, "Bearer test-token")
	}
	var body struct {
		ReactionType struct {
			EmojiType string `json:"emoji_type"`
		} `json:"reaction_type"`
	}
	if err := json.Unmarshal([]byte(req.body), &body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if body.ReactionType.EmojiType != "DONE" {
		t.Errorf("emoji_type = %q, want %q", body.ReactionType.EmojiType, "DONE")
	}
}

func TestAddReaction_InvalidMessageID(t *testing.T) {
	adapter, requests := newReactionAdapter(t, reactionOKPayload("reaction-1"))
	ctx := context.Background()
	for _, messageID := range []string{"", "bad/id", "a b", "../x"} {
		if _, err := adapter.AddReaction(ctx, &im.IncomingMessage{MessageID: messageID}, "OnIt"); err == nil {
			t.Errorf("AddReaction(%q) succeeded, want error", messageID)
		}
	}
	if len(*requests) != 0 {
		t.Errorf("invalid message IDs must not hit the API: %d requests recorded", len(*requests))
	}
	if _, err := adapter.AddReaction(ctx, nil, "OnIt"); err == nil {
		t.Error("AddReaction(nil message) succeeded, want error")
	}
}

func TestAddReaction_APIError(t *testing.T) {
	adapter, _ := newReactionAdapter(t, reactionCodePayload(10001, "no permission"))
	_, err := adapter.AddReaction(context.Background(), &im.IncomingMessage{MessageID: "om_123"}, "DONE")
	if err == nil {
		t.Fatal("AddReaction with API error succeeded, want error")
	}
	if !strings.Contains(err.Error(), "10001") {
		t.Errorf("error = %v, want it to mention code 10001", err)
	}
}

func TestAddReaction_MissingReactionID(t *testing.T) {
	adapter, _ := newReactionAdapter(t, reactionCodePayload(0, "ok"))
	_, err := adapter.AddReaction(context.Background(), &im.IncomingMessage{MessageID: "om_123"}, "DONE")
	if err == nil {
		t.Error("AddReaction with empty reaction_id succeeded, want error")
	}
}

func TestRemoveReaction_Success(t *testing.T) {
	adapter, requests := newReactionAdapter(t, reactionCodePayload(0, "ok"))
	ctx := context.Background()
	if err := adapter.RemoveReaction(ctx, &im.IncomingMessage{MessageID: "om_123"}, "reaction-1"); err != nil {
		t.Fatalf("RemoveReaction: %v", err)
	}
	if len(*requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(*requests))
	}
	req := (*requests)[0]
	wantPath := "/open-apis/im/v1/messages/om_123/reactions/reaction-1"
	if req.method != http.MethodDelete || req.path != wantPath {
		t.Errorf("request = %s %s, want DELETE %s", req.method, req.path, wantPath)
	}
	if req.auth != "Bearer test-token" {
		t.Errorf("Authorization = %q, want %q", req.auth, "Bearer test-token")
	}
}

func TestRemoveReaction_APIError(t *testing.T) {
	adapter, _ := newReactionAdapter(t, reactionCodePayload(10001, "no permission"))
	err := adapter.RemoveReaction(context.Background(), &im.IncomingMessage{MessageID: "om_123"}, "reaction-1")
	if err == nil {
		t.Fatal("RemoveReaction with API error succeeded, want error")
	}
	if !strings.Contains(err.Error(), "10001") {
		t.Errorf("error = %v, want it to mention code 10001", err)
	}
}

func TestRemoveReaction_InvalidIDs(t *testing.T) {
	adapter, requests := newReactionAdapter(t, reactionCodePayload(0, "ok"))
	ctx := context.Background()
	msg := &im.IncomingMessage{MessageID: "om_123"}
	if err := adapter.RemoveReaction(ctx, msg, ""); err == nil {
		t.Error("RemoveReaction with empty reaction_id succeeded, want error")
	}
	if err := adapter.RemoveReaction(ctx, msg, "bad/id"); err == nil {
		t.Error("RemoveReaction with unsafe reaction_id succeeded, want error")
	}
	if err := adapter.RemoveReaction(ctx, &im.IncomingMessage{MessageID: "bad/id"}, "reaction-1"); err == nil {
		t.Error("RemoveReaction with unsafe message_id succeeded, want error")
	}
	if err := adapter.RemoveReaction(ctx, nil, "reaction-1"); err == nil {
		t.Error("RemoveReaction(nil message) succeeded, want error")
	}
	if len(*requests) != 0 {
		t.Errorf("invalid IDs must not hit the API: %d requests recorded", len(*requests))
	}
}
