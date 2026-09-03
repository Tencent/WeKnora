package feishu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFeishuThreadID_ThreadedReply(t *testing.T) {
	// Simulate: message is a reply in a thread (root_id is set)
	msg := &feishuMessage{
		MessageID: "msg-reply-1",
		RootID:    "msg-root-1",
		ParentID:  "msg-parent-1",
	}

	threadID := msg.RootID
	if threadID == "" {
		threadID = msg.MessageID
	}

	if threadID != "msg-root-1" {
		t.Errorf("threadID = %q, want %q", threadID, "msg-root-1")
	}
}

func TestFeishuThreadID_TopLevelMessage(t *testing.T) {
	// Simulate: top-level message (root_id is empty)
	msg := &feishuMessage{
		MessageID: "msg-top-1",
		RootID:    "",
		ParentID:  "",
	}

	threadID := msg.RootID
	if threadID == "" {
		threadID = msg.MessageID
	}

	if threadID != "msg-top-1" {
		t.Errorf("threadID = %q, want %q (should use MessageID as fallback)", threadID, "msg-top-1")
	}
}

func TestFeishuMessageStruct_JSONFields(t *testing.T) {
	// Verify the struct fields exist and have correct zero values
	msg := feishuMessage{}
	if msg.RootID != "" {
		t.Errorf("RootID zero value = %q, want empty", msg.RootID)
	}
	if msg.ParentID != "" {
		t.Errorf("ParentID zero value = %q, want empty", msg.ParentID)
	}
	if msg.MessageID != "" {
		t.Errorf("MessageID zero value = %q, want empty", msg.MessageID)
	}
}

func TestImageCacheKey_StripsQuery(t *testing.T) {
	cases := map[string]string{
		"https://host/a.png?sig=1&t=2": "https://host/a.png",
		"https://host/a.png":           "https://host/a.png",
	}
	for in, want := range cases {
		if got := imageCacheKey(in); got != want {
			t.Errorf("imageCacheKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveMarkdownImages_NoImageUnchanged(t *testing.T) {
	a := &Adapter{region: RegionFeishu}
	in := "hello **world** [link](https://example.com)"
	if got := a.resolveMarkdownImages(context.Background(), "tok", in); got != in {
		t.Errorf("content without image was modified: %q", got)
	}
}

func TestResolveMarkdownImages_FallbackToLinkOnFailure(t *testing.T) {
	a := &Adapter{region: RegionFeishu}
	// A direct-IP loopback URL fails SSRF validation before any network call,
	// so the image must degrade to a plain markdown link (never left as ![]()).
	in := "see ![diagram](http://127.0.0.1/x.png) here"
	got := a.resolveMarkdownImages(context.Background(), "tok", in)
	if strings.Contains(got, "![") {
		t.Errorf("failed image should not remain as image markdown: %q", got)
	}
	if !strings.Contains(got, "[diagram](http://127.0.0.1/x.png)") {
		t.Errorf("expected link fallback with alt text, got: %q", got)
	}
}

func TestResolveMarkdownImages_ResourceHandleFallsBackToLink(t *testing.T) {
	a := &Adapter{region: RegionFeishu}
	in := "see ![diagram](resource://Zb2IrqfTdlCFkbUqFtmS5A) here"
	want := "see [diagram](resource://Zb2IrqfTdlCFkbUqFtmS5A) here"
	if got := a.resolveMarkdownImages(context.Background(), "tok", in); got != want {
		t.Errorf("resource image fallback = %q, want %q", got, want)
	}
}

// An image with no alt text falls back to the region's own label, so Lark users
// do not get a Chinese link label.
func TestResolveMarkdownImages_FallbackLabelFollowsRegion(t *testing.T) {
	for _, region := range []Region{RegionFeishu, RegionLark} {
		a := &Adapter{region: region}
		got := a.resolveMarkdownImages(context.Background(), "tok", "![](http://127.0.0.1/x.png)")
		want := "[" + region.ImageFallbackLabel + "](http://127.0.0.1/x.png)"
		if got != want {
			t.Errorf("%s fallback = %q, want %q", region.Label, got, want)
		}
	}
}

func TestEndStreamFinalizesCardSummary(t *testing.T) {
	useTestHTTPClient(t)
	type recordedRequest struct {
		method        string
		path          string
		authorization string
		sequence      int
		content       string
		settings      string
		statePresent  bool
	}
	var requests []recordedRequest

	const cardID = "card-final-summary"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "tenant_access_token": "test-token", "expire": 7200,
			})
			return
		}

		var body struct {
			Sequence int    `json:"sequence"`
			Content  string `json:"content"`
			Settings string `json:"settings"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode %s: %v", r.URL.Path, err)
		}
		feishuStreamsMu.Lock()
		_, statePresent := feishuStreams[cardID]
		feishuStreamsMu.Unlock()
		requests = append(requests, recordedRequest{
			method: r.Method, path: r.URL.Path, authorization: r.Header.Get("Authorization"),
			sequence: body.Sequence, content: body.Content, settings: body.Settings,
			statePresent: statePresent,
		})
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok"})
	}))
	defer srv.Close()

	adapter, _ := NewAdapter(testRegion(srv.URL), "app", "secret", "", "", "")
	feishuStreamsMu.Lock()
	feishuStreams[cardID] = &feishuStreamState{}
	feishuStreamsMu.Unlock()
	t.Cleanup(func() {
		feishuStreamsMu.Lock()
		delete(feishuStreams, cardID)
		feishuStreamsMu.Unlock()
	})

	ctx := context.Background()
	if err := adapter.UpdateStreamContent(ctx, nil, cardID, "progress"); err != nil {
		t.Fatalf("UpdateStreamContent: %v", err)
	}
	const finalContent = "  最终\n回答  ✅  "
	if err := adapter.FinalizeStream(ctx, nil, cardID, finalContent); err != nil {
		t.Fatalf("FinalizeStream: %v", err)
	}
	if err := adapter.EndStream(ctx, nil, cardID); err != nil {
		t.Fatalf("EndStream: %v", err)
	}

	if len(requests) != 3 {
		t.Fatalf("CardKit request count = %d, want 3", len(requests))
	}
	for i, request := range requests {
		if request.authorization != "Bearer test-token" {
			t.Errorf("request %d Authorization = %q", i, request.authorization)
		}
		if request.sequence != i+1 {
			t.Errorf("request %d sequence = %d, want %d", i, request.sequence, i+1)
		}
	}
	if requests[1].content != finalContent {
		t.Errorf("final content = %q, want %q", requests[1].content, finalContent)
	}
	if requests[2].method != http.MethodPatch || requests[2].path != "/open-apis/cardkit/v1/cards/"+cardID+"/settings" {
		t.Errorf("final request = %s %s", requests[2].method, requests[2].path)
	}
	if requests[2].statePresent {
		t.Error("stream state still present when final settings were sent")
	}

	var settings struct {
		Config *struct {
			StreamingMode *bool `json:"streaming_mode"`
			Summary       struct {
				Content string `json:"content"`
			} `json:"summary"`
		} `json:"config"`
	}
	if err := json.Unmarshal([]byte(requests[2].settings), &settings); err != nil {
		t.Fatalf("decode final settings: %v", err)
	}
	if settings.Config == nil || settings.Config.StreamingMode == nil || *settings.Config.StreamingMode {
		t.Fatalf("final settings did not disable streaming under config: %s", requests[2].settings)
	}
	if settings.Config.Summary.Content != "最终 回答 ✅" {
		t.Errorf("summary = %q, want %q", settings.Config.Summary.Content, "最终 回答 ✅")
	}

	if err := adapter.EndStream(ctx, nil, "missing-card"); err != nil {
		t.Fatalf("EndStream missing state: %v", err)
	}
	if len(requests) != 4 {
		t.Fatalf("request count after missing state = %d, want 4", len(requests))
	}
	assertStreamingClosed(t, requests[3].settings, "")
	if requests[3].sequence != 0 {
		t.Errorf("missing-state sequence = %d, want 0", requests[3].sequence)
	}
}

func TestEndStreamSummaryUsesLastSuccessfulContent(t *testing.T) {
	useTestHTTPClient(t)
	const cardID = "card-failed-final-content"
	var finalSettings string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "tenant_access_token": "test-token", "expire": 7200,
			})
			return
		}

		var body struct {
			Content  string `json:"content"`
			Settings string `json:"settings"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode %s: %v", r.URL.Path, err)
			return
		}
		if body.Content == "failed final content" {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 1, "msg": "rejected"})
			return
		}
		if r.Method == http.MethodPatch {
			finalSettings = body.Settings
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok"})
	}))
	defer srv.Close()

	adapter, _ := NewAdapter(testRegion(srv.URL), "app", "secret", "", "", "")
	feishuStreamsMu.Lock()
	feishuStreams[cardID] = &feishuStreamState{}
	feishuStreamsMu.Unlock()
	t.Cleanup(func() {
		feishuStreamsMu.Lock()
		delete(feishuStreams, cardID)
		feishuStreamsMu.Unlock()
	})

	ctx := context.Background()
	if err := adapter.UpdateStreamContent(ctx, nil, cardID, "visible progress"); err != nil {
		t.Fatalf("UpdateStreamContent: %v", err)
	}
	if err := adapter.FinalizeStream(ctx, nil, cardID, "failed final content"); err == nil {
		t.Fatal("FinalizeStream succeeded, want CardKit rejection")
	}
	if err := adapter.EndStream(ctx, nil, cardID); err != nil {
		t.Fatalf("EndStream: %v", err)
	}

	var settings struct {
		Config struct {
			Summary struct {
				Content string `json:"content"`
			} `json:"summary"`
		} `json:"config"`
	}
	if err := json.Unmarshal([]byte(finalSettings), &settings); err != nil {
		t.Fatalf("decode final settings: %v", err)
	}
	if settings.Config.Summary.Content != "visible progress" {
		t.Errorf("summary = %q, want last successful content", settings.Config.Summary.Content)
	}
}

func TestEndStreamFinalizeOnlyUsesFinalContent(t *testing.T) {
	useTestHTTPClient(t)
	const cardID = "card-full-output"
	var finalSettings string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "tenant_access_token": "test-token", "expire": 7200,
			})
			return
		}
		var body struct {
			Settings string `json:"settings"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode %s: %v", r.URL.Path, err)
			return
		}
		if r.Method == http.MethodPatch {
			finalSettings = body.Settings
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok"})
	}))
	defer srv.Close()

	adapter, _ := NewAdapter(testRegion(srv.URL), "app", "secret", "", "", "")
	feishuStreamsMu.Lock()
	feishuStreams[cardID] = &feishuStreamState{}
	feishuStreamsMu.Unlock()
	t.Cleanup(func() {
		feishuStreamsMu.Lock()
		delete(feishuStreams, cardID)
		feishuStreamsMu.Unlock()
	})

	ctx := context.Background()
	if err := adapter.FinalizeStream(ctx, nil, cardID, "最终答案"); err != nil {
		t.Fatalf("FinalizeStream: %v", err)
	}
	if err := adapter.EndStream(ctx, nil, cardID); err != nil {
		t.Fatalf("EndStream: %v", err)
	}
	assertStreamingClosed(t, finalSettings, "最终答案")
}

func TestEndStreamOmitsEmptySummary(t *testing.T) {
	useTestHTTPClient(t)
	const cardID = "card-empty-summary"
	var finalSettings string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "tenant_access_token": "test-token", "expire": 7200,
			})
			return
		}
		var body struct {
			Settings string `json:"settings"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode %s: %v", r.URL.Path, err)
			return
		}
		if r.Method == http.MethodPatch {
			finalSettings = body.Settings
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok"})
	}))
	defer srv.Close()

	adapter, _ := NewAdapter(testRegion(srv.URL), "app", "secret", "", "", "")
	feishuStreamsMu.Lock()
	feishuStreams[cardID] = &feishuStreamState{}
	feishuStreamsMu.Unlock()
	t.Cleanup(func() {
		feishuStreamsMu.Lock()
		delete(feishuStreams, cardID)
		feishuStreamsMu.Unlock()
	})

	if err := adapter.EndStream(context.Background(), nil, cardID); err != nil {
		t.Fatalf("EndStream: %v", err)
	}
	assertStreamingClosed(t, finalSettings, "")
}

func TestCardSummaryPreview(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "collapse whitespace", in: "  最终\n回答\t✅  ", want: "最终 回答 ✅"},
		{name: "image keeps alt only", in: "![架构图](https://cdn.example/a.png?sig=secret) 说明", want: "架构图 说明"},
		{name: "link keeps text only", in: "见 [文档](https://cdn.example/a.png?sig=secret) 说明", want: "见 文档 说明"},
		{name: "unicode limit", in: strings.Repeat("界", 121), want: strings.Repeat("界", 120)},
		{name: "empty", in: " \n\t ", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := cardSummaryPreview(test.in); got != test.want {
				t.Errorf("cardSummaryPreview() = %q, want %q", got, test.want)
			}
		})
	}
}

type cardSettings struct {
	Config *struct {
		StreamingMode *bool `json:"streaming_mode"`
		Summary       *struct {
			Content string `json:"content"`
		} `json:"summary"`
	} `json:"config"`
}

func decodeCardSettings(t *testing.T, raw string) cardSettings {
	t.Helper()
	var settings cardSettings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		t.Fatalf("decode settings %q: %v", raw, err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &top); err != nil {
		t.Fatalf("decode settings map %q: %v", raw, err)
	}
	if _, ok := top["streaming_mode"]; ok {
		t.Fatalf("streaming_mode must be nested under config: %s", raw)
	}
	return settings
}

func assertStreamingClosed(t *testing.T, raw, wantSummary string) {
	t.Helper()
	settings := decodeCardSettings(t, raw)
	if settings.Config == nil || settings.Config.StreamingMode == nil || *settings.Config.StreamingMode {
		t.Fatalf("settings did not disable streaming under config: %s", raw)
	}
	if wantSummary == "" {
		if settings.Config.Summary != nil {
			t.Fatalf("settings included empty summary: %s", raw)
		}
		return
	}
	if settings.Config.Summary == nil || settings.Config.Summary.Content != wantSummary {
		t.Fatalf("summary = %v, want %q in %s", settings.Config.Summary, wantSummary, raw)
	}
}
