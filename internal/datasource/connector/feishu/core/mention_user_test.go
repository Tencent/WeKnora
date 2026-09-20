package core

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// mentionBlk builds a text block whose only inline element is an @mention of
// the given OpenID.
func mentionBlk(id, openID string) DocxBlock {
	return DocxBlock{
		BlockID: id, BlockType: BlockTypeText,
		Text: &BlockText{Elements: []TextElement{{MentionUser: &MentionUser{UserID: openID}}}},
	}
}

// mentionUserCase renders blocks and asserts the resolved display names hit
// (named) and miss/403-degrade (generic @成员) paths.
func mentionUserCase(t *testing.T, name string, resolver func(string) string, blocks []DocxBlock, want []string) {
	t.Helper()
	md, _, _, err := blocksToMarkdown(context.Background(), nil, blocks, "", resolver)
	if err != nil {
		t.Fatalf("%s: err: %v", name, err)
	}
	for _, w := range want {
		if !strings.Contains(string(md), w) {
			t.Errorf("%s: missing %q, got:\n%s", name, w, md)
		}
	}
}

func TestBlocksToMarkdown_MentionUserName(t *testing.T) {
	// Hit: OpenID resolves to a real display name.
	resolved := map[string]string{"ou_zhang": "张三", "ou_li": "李四"}
	mentionUserCase(t, "hit", func(id string) string { return resolved[id] },
		[]DocxBlock{
			{BlockID: "p1", BlockType: BlockTypeText, Text: &BlockText{Elements: []TextElement{
				{TextRun: &TextRun{Content: "请 "}},
				{MentionUser: &MentionUser{UserID: "ou_zhang"}},
			}}},
			mentionBlk("p2", "ou_li"),
		},
		[]string{"@张三", "@李四"})

	// Miss (unknown user / API failure): degrade to the generic marker, and
	// the document still renders — the failure never surfaces as an error.
	var calls int
	mentionUserCase(t, "miss", func(id string) string {
		calls++
		return ""
	}, []DocxBlock{mentionBlk("p1", "ou_gone")},
		[]string{"@成员"})
	if calls != 1 {
		t.Errorf("resolver calls = %d, want 1", calls)
	}
}

func TestBlocksToMarkdown_MentionUserName_OncePerElement(t *testing.T) {
	var calls int
	resolver := func(id string) string {
		calls++
		return "张三"
	}
	blocks := []DocxBlock{mentionBlk("p1", "ou_x"), mentionBlk("p2", "ou_x"), mentionBlk("p3", "ou_y")}
	md, _, _, err := blocksToMarkdown(context.Background(), nil, blocks, "", resolver)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// The renderer consults the resolver once per mention element; dedup
	// caching lives in the userNameResolver closure (tested with real HTTP
	// traffic in TestUserNameResolver_NegativeCache).
	if calls != 3 {
		t.Errorf("resolver calls = %d, want 3 (one per mention element)", calls)
	}
	if got := strings.Count(string(md), "@张三"); got != 3 {
		t.Errorf("mentions rendered = %d, want 3, got:\n%s", got, md)
	}
}

func TestBlocksToMarkdown_NoMention_ZeroResolverCalls(t *testing.T) {
	var calls int
	resolver := func(id string) string {
		calls++
		return "张三"
	}
	blocks := []DocxBlock{
		{BlockID: "p1", BlockType: BlockTypeText, Text: txt("没有提及的普通段落")},
		{BlockID: "p2", BlockType: BlockTypeText, Text: txt("另一段")},
	}
	if _, _, _, err := blocksToMarkdown(context.Background(), nil, blocks, "", resolver); err != nil {
		t.Fatalf("err: %v", err)
	}
	if calls != 0 {
		t.Errorf("resolver calls = %d, want 0 (no API traffic without mentions)", calls)
	}
}

func TestUserName(t *testing.T) {
	ts, cfg := retryTestServer("/open-apis/contact/v3/users/ou_1", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("user_id_type") != "open_id" {
			t.Errorf("user_id_type = %q, want open_id", r.URL.Query().Get("user_id_type"))
		}
		writeJSON(w, map[string]any{"code": 0, "data": map[string]any{
			"user": map[string]any{"name": "张三"},
		}})
	})
	defer ts.Close()

	c := NewClient(cfg)
	name, err := c.UserName(context.Background(), "ou_1")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if name != "张三" {
		t.Errorf("name = %q, want 张三", name)
	}

	// 403 (no contact scope): error, caller degrades.
	ts403, cfg403 := retryTestServer("/open-apis/contact/v3/users/ou_1", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"code":99991679,"msg":"permission denied"}`))
	})
	defer ts403.Close()
	if _, err := NewClient(cfg403).UserName(context.Background(), "ou_1"); err == nil {
		t.Error("403 must return an error for the caller to degrade")
	}

	// HTTP 200 but business error code: also an error.
	tsCode, cfgCode := retryTestServer("/open-apis/contact/v3/users/ou_1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"code": 99991672, "msg": "no permission"})
	})
	defer tsCode.Close()
	if _, err := NewClient(cfgCode).UserName(context.Background(), "ou_1"); err == nil {
		t.Error("non-zero body code must return an error")
	}
}

func TestUserNameResolver_NegativeCache(t *testing.T) {
	var hits atomic.Int64
	ts, cfg := retryTestServer("/open-apis/contact/v3/users/", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if strings.Contains(r.URL.Path, "ou_ok") {
			writeJSON(w, map[string]any{"code": 0, "data": map[string]any{
				"user": map[string]any{"name": "张三"},
			}})
			return
		}
		// No permission for the other user, forever.
		w.WriteHeader(http.StatusForbidden)
	})
	defer ts.Close()

	resolver := userNameResolver(context.Background(), NewClient(cfg))
	// Same user three times: resolved once; failed user three times: one
	// negative-cached attempt. Total HTTP traffic must be 2.
	got := []string{
		resolver("ou_ok"), resolver("ou_ok"), resolver("ou_ok"),
		resolver("ou_bad"), resolver("ou_bad"), resolver("ou_bad"),
	}
	for i, g := range got {
		want := "张三"
		if i >= 3 {
			want = ""
		}
		if g != want {
			t.Errorf("resolver[%d] = %q, want %q", i, g, want)
		}
	}
	if n := hits.Load(); n != 2 {
		t.Errorf("contact API hits = %d, want 2 (per-user cache incl. negative)", n)
	}
	if _, err := NewClient(cfg).UserName(context.Background(), ""); err == nil {
		t.Error("empty user id must be rejected without an API call")
	}
}
