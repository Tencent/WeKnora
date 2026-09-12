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
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestTenantMessageReadsKeepForwardOwnershipAndParentMetadata(t *testing.T) {
	useTestHTTPClient(t)
	tokenCalls, botCalls := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			tokenCalls++
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"test-tenant-token","expire":7200}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-tenant-token" {
			t.Errorf("missing tenant identity")
		}
		switch r.URL.Path {
		case "/open-apis/bot/v3/info":
			botCalls++
			_, _ = io.WriteString(w, `{"code":0,"bot":{"open_id":"ou_bot"}}`)
		case "/open-apis/im/v1/messages/outer":
			if r.URL.Query().Get("user_id_type") != "open_id" {
				t.Error("message sender IDs must use open_id")
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"items":[
				{"message_id":"outer","msg_type":"merge_forward","body":{"content":"{}"}},
				{"message_id":"inner","upper_message_id":"outer","msg_type":"merge_forward","body":{"content":"{}"}},
				{"message_id":"original","upper_message_id":"inner","parent_id":"outside",
				 "chat_id":"source_chat","create_time":"123456","sender":{"id":"ou_author"},"msg_type":"post",
				 "body":{"content":"{\"content\":[[{\"tag\":\"text\",\"text\":\"A\"},`+
				`{\"tag\":\"img\",\"image_key\":\"image_from_outer\"},{\"tag\":\"text\",\"text\":\"B\"},`+
				`{\"tag\":\"a\",\"text\":\"plan\",\"href\":\"https://example.com/?v=1&literal=&amp;&q=%2f#part\"}]]}"}},
				{"message_id":"card","upper_message_id":"outer","parent_id":"original",
				 "msg_type":"interactive","body":{"content":"{}"}},
				{"message_id":"broken","upper_message_id":"outer","parent_id":"original",
				 "msg_type":"text","body":{"content":"not json"}}
			]}}`)
		case "/open-apis/im/v1/messages/outside":
			_, _ = io.WriteString(w, `{"code":0,"data":{"items":[
				{"message_id":"outside","chat_id":"other_chat","msg_type":"image",
				 "body":{"content":"{\"image_key\":\"own_image\"}"}}]}}`)
		case "/open-apis/im/v1/messages/forbidden":
			_, _ = io.WriteString(w, `{"code":230050,"msg":"message invisible"}`)
		case "/open-apis/im/v1/messages/outer/resources/image_from_outer":
			if r.URL.Query().Get("type") != "image" {
				t.Error("wrong resource type")
			}
			_, _ = io.WriteString(w, "image bytes")
		default:
			t.Errorf("unexpected API path: %s", r.URL.Path)
			http.Error(w, "wrong message/resource context", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	a := &Adapter{region: RegionFeishu, apiBaseURL: srv.URL, appID: "test-app", appSecret: "test-secret"}
	items, err := a.ReadMessage(t.Context(), "outer")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 ||
		items[2].ResourceMessageID != "outer" ||
		items[2].UpperMessageID != "inner" ||
		items[2].ParentID != "outside" ||
		items[2].ChatID != "source_chat" ||
		items[2].SenderID != "ou_author" ||
		items[2].CreateTime != "123456" {
		t.Fatalf("forward metadata lost: %+v", items)
	}
	if items[3].CardStatus != "empty" ||
		items[3].ParentID != "original" ||
		items[4].Unavailable == "" ||
		items[4].ParentID != "original" {
		t.Fatal("empty card or malformed body lost legal parent metadata")
	}
	var text strings.Builder
	for _, part := range items[2].Parts {
		text.WriteString(part.Text)
	}
	if text.String() != "ABplan（https://example.com/?v=1&literal=&amp;&q=%2f#part）\n" {
		t.Fatalf("message read lost the link or its position: %q", text.String())
	}
	part := items[2].Parts[1]
	resource := &im.IncomingMessage{
		MessageID: items[2].ResourceMessageID, FileKey: part.FileKey, MessageType: part.Type, FileName: part.FileName,
	}
	reader, _, err := a.DownloadFile(t.Context(), resource)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(data) != "image bytes" || resource.FileSize != int64(len(data)) {
		t.Fatal("resource body/length not preserved")
	}
	outside, err := a.ReadMessage(t.Context(), "outside")
	if err != nil || outside[0].ResourceMessageID != "outside" || outside[0].Parts[0].FileKey != "own_image" {
		t.Fatal("cross-chat parent inherited the forward's resource context")
	}
	if _, err := a.ReadMessage(t.Context(), "forbidden"); err == nil {
		t.Fatal("invisible message was accepted")
	}
	if _, err := a.ReadMessage(t.Context(), "../outside"); err == nil {
		t.Fatal("unsafe message path was accepted")
	}
	key, id := "@_user_1", "ou_bot"
	message := &feishuMessage{
		MessageID: "current", MessageType: "text", ChatType: "group", Content: `{"text":"@_user_1 question"}`,
		Mentions: []*larkim.MentionEvent{{Key: &key, Id: &larkim.UserId{OpenId: &id}}},
	}
	for range 2 {
		msg, err := a.parseIncoming(context.Background(), message, "user", "")
		if err != nil || msg == nil || msg.Content != "question" {
			t.Fatalf("mention validation failed: msg=%+v err=%v", msg, err)
		}
	}
	if tokenCalls != 1 || botCalls != 1 {
		t.Fatalf("identity cache not reused: token=%d bot=%d", tokenCalls, botCalls)
	}
}

func TestMalformedPostDoesNotDropValidSiblingsSilently(t *testing.T) {
	material := &im.MessageMaterial{Type: "post"}
	_, _, _, err := parseMaterialBody(material, `{"content":[[{"tag":"text","text":"x"},17]]}`, nil, "")
	if err == nil {
		t.Fatal("malformed rich text should be a reported parse failure")
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(`{"content":[[
		{"tag":"at","user_id":"ou_other"},{"tag":"img","image_key":"one"},{"tag":"media"}
	]]}`), &raw); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(raw)
	material = &im.MessageMaterial{Type: "post"}
	text, mentioned, _, err := parseMaterialBody(material, string(body), nil, "ou_bot")
	if err != nil ||
		mentioned ||
		!strings.Contains(text, "@ou_other") ||
		len(material.Parts) != 4 ||
		material.Parts[2].Type != "media" {
		t.Fatal("other mentions or unsupported rich-text elements were discarded")
	}
}

func TestCurrentMaterialTextMatchesValidatedQuery(t *testing.T) {
	padding := strings.Repeat(" ", 4097)
	body, err := json.Marshal(postBody{Title: padding, Content: [][]postElement{{
		{Tag: "img", ImageKey: "image"}, {Tag: "text", Text: padding + "question" + padding},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	a := &Adapter{region: RegionFeishu}
	msg, err := a.parseIncoming(t.Context(), &feishuMessage{
		MessageID: "current", MessageType: "post", Content: string(body),
	}, "user", "")
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, part := range msg.Material.Parts {
		text.WriteString(part.Text)
	}
	if msg.Content != "question" || text.String() != msg.Content {
		t.Fatal("material kept unbounded whitespace excluded from current-input validation")
	}
}

func TestPostLinksPreserveURLInCurrentTextAndMaterials(t *testing.T) {
	const address = "https://example.com/im-link-check?case=LINK-POST-8461&v=1#details"
	const question = "请不要打开网页，只返回“查看方案”对应的完整链接地址；如果没有收到地址，请明确说明。"
	key, id, name := "@_user_2", "ou_other", "Alice"
	mentions := []*larkim.MentionEvent{{Key: &key, Id: &larkim.UserId{OpenId: &id}, Name: &name}}
	for _, tc := range []struct {
		name     string
		elements []postElement
		want     string
	}{
		{"live baseline", []postElement{{Tag: "a", Text: "查看方案", Href: address}}, "查看方案（" + address + "）"},
		{"same label", []postElement{{Tag: "a", Text: address, Href: address}}, address},
		{"no label", []postElement{{Tag: "a", Href: address}}, address},
		{"no address", []postElement{{Tag: "a", Text: "查看方案"}}, "查看方案"},
		{"empty", []postElement{{Tag: "a"}}, ""},
		{
			"raw address",
			[]postElement{{
				Tag: "a", Text: "原值", Href: "https://example.com/(Plan)?q=%26%2f&literal=&amp;&owner=@_user_2#片段",
			}},
			"原值（https://example.com/(Plan)?q=%26%2f&literal=&amp;&owner=@_user_2#片段）",
		},
		{
			"label changed before deduplication",
			[]postElement{{
				Tag: "a", Text: "https://example.com/@_user_2", Href: "https://example.com/@_user_2",
			}},
			"https://example.com/@Alice（https://example.com/@_user_2）",
		},
		{"repeated labels and targets", []postElement{
			{Tag: "text", Text: "先看 "},
			{Tag: "a", Text: "方案", Href: "https://example.com/a"},
			{Tag: "text", Text: "，再看 "},
			{Tag: "a", Text: "方案", Href: "https://example.com/b"},
			{Tag: "text", Text: "，重复 "},
			{Tag: "a", Text: "方案", Href: "https://example.com/a"},
		}, "先看 方案（https://example.com/a），再看 方案（https://example.com/b），重复 方案（https://example.com/a）"},
	} {
		for _, region := range []Region{RegionFeishu, RegionLark} {
			for _, format := range []string{"content", "content_v2", "both", "locale"} {
				t.Run(tc.name+"/"+string(region.Platform)+"/"+format, func(t *testing.T) {
					rows := [][]postElement{tc.elements, {{Tag: "text", Text: question}}}
					body := postBody{Title: "INK-POST-8461", Content: rows}
					if format == "content_v2" || format == "both" {
						body.ContentV2 = rows
						body.Content = nil
						if format == "both" {
							body.Content = rows
						}
					}
					var payload any = body
					if format == "locale" {
						payload = map[string]postBody{"zh_cn": body}
					}
					raw, err := json.Marshal(payload)
					if err != nil {
						t.Fatal(err)
					}
					a := &Adapter{region: region}
					msg, err := a.parseIncoming(t.Context(), &feishuMessage{
						MessageType: "post", Content: string(raw), Mentions: mentions,
					}, "sender", "")
					if err != nil || msg == nil || msg.Material == nil {
						t.Fatalf("post missing: msg=%+v err=%v", msg, err)
					}
					want := "INK-POST-8461\n" + tc.want + "\n" + question
					var materialText strings.Builder
					for _, part := range msg.Material.Parts {
						materialText.WriteString(part.Text)
					}
					if msg.Content != want || materialText.String() != want {
						t.Fatalf("link content lost or changed: query=%q material=%q want=%q",
							msg.Content, materialText.String(), want)
					}
				})
			}
		}
	}
}

func TestPostLinkTargetsDoNotAuthorizeMentionsOrAlterCommands(t *testing.T) {
	key, bot := "@_user_1", "ou_bot"
	const target = "https://example.com/@_user_1?command=/clear"
	for _, label := range []string{"", "查看方案", "/stop later"} {
		for _, realMention := range []bool{false, true} {
			row := []postElement{{Tag: "a", Text: label, Href: target}}
			if realMention {
				row = append([]postElement{{Tag: "at", UserID: bot}}, row...)
			}
			raw, err := json.Marshal(postBody{Content: [][]postElement{row}})
			if err != nil {
				t.Fatal(err)
			}
			a := &Adapter{region: RegionFeishu, botOpenID: bot}
			msg, err := a.parseIncoming(t.Context(), &feishuMessage{
				MessageType: "post", ChatType: "group", Content: string(raw),
				Mentions: []*larkim.MentionEvent{{Key: &key, Id: &larkim.UserId{OpenId: &bot}}},
			}, "sender", "")
			if err != nil || (msg != nil) != realMention {
				t.Fatalf("URL changed group authorization: label=%q mention=%t msg=%+v err=%v",
					label, realMention, msg, err)
			}
			if msg == nil {
				continue
			}
			var materialText strings.Builder
			for _, part := range msg.Material.Parts {
				materialText.WriteString(part.Text)
			}
			if !strings.Contains(materialText.String(), target) {
				t.Fatal("URL was replaced as a mention")
			}
			if label == "/stop later" {
				if msg.Content != label || msg.SkipCommand {
					t.Fatalf("URL changed the current command: %+v", msg)
				}
			} else if msg.Content != materialText.String() || msg.SkipCommand {
				t.Fatal("non-command query differs from its validated material text")
			}
		}
	}
}

func TestCurrentMentionsKeepExistingCommandRecognition(t *testing.T) {
	bot, other, botKey, otherKey, name := "ou_bot", "ou_other", "@_user_1", "@_user_2", "Alice"
	mentions := []*larkim.MentionEvent{
		{Key: &botKey, Id: &larkim.UserId{OpenId: &bot}},
		{Key: &otherKey, Id: &larkim.UserId{OpenId: &other}, Name: &name},
	}
	registry := im.NewCommandRegistry()
	registry.Register(&im.StopCommand{})
	for _, tc := range []struct{ kind, body, want string }{
		{"text", `{"text":"@_user_1 @_user_2 /stop"}`, "/stop"},
		{"text", `{"text":"@_user_2 @_user_1 /stop"}`, "/stop"},
		{"text", `{"text":"@_user_1 /stop @_user_2"}`, "/stop @_user_2"},
		{"post", `{"content":[[{"tag":"text","text":"  @_user_1 @_user_2 /stop  "}]]}`, "/stop"},
		{"post", `{"content":[[{"tag":"at","user_id":"ou_bot"},
			{"tag":"at","user_id":"ou_other","user_name":"Alice"},{"tag":"text","text":"/stop"}]]}`, "/stop"},
		{"post", `{"content":[[{"tag":"at","user_id":"ou_bot"},
			{"tag":"at","user_id":"ou_other","user_name":"Alice"},
			{"tag":"text","text":" /api/v2/users"}]]}`, "@Alice /api/v2/users"},
		{"text", `{"text":"@_user_1 请告诉 @_user_2 /stop 的用法"}`, "请告诉 @Alice /stop 的用法"},
	} {
		t.Run(tc.kind+"/"+tc.want, func(t *testing.T) {
			a := &Adapter{region: RegionFeishu, botOpenID: bot}
			msg, err := a.parseIncoming(t.Context(), &feishuMessage{
				MessageType: tc.kind, ChatType: "group", Content: tc.body, Mentions: mentions,
			}, "sender", "")
			if err != nil || msg == nil || msg.Content != tc.want {
				t.Fatalf("current command changed: msg=%+v err=%v want=%q", msg, err, tc.want)
			}
			_, _, parsed := registry.Parse(msg.Content)
			wantCommand := strings.HasPrefix(tc.want, "/stop")
			if (!msg.SkipCommand && parsed) != wantCommand ||
				(!msg.SkipCommand && registry.IsRegistered(msg.Content)) != wantCommand {
				t.Fatal("command dispatch and rate-limit bypass disagree with the current request")
			}
			if !wantCommand && msg.Material != nil {
				var materialText strings.Builder
				for _, part := range msg.Material.Parts {
					materialText.WriteString(part.Text)
				}
				if materialText.String() != msg.Content {
					t.Fatal("QA received current material excluded from input validation")
				}
			}
		})
	}
}

func TestPlainTextDoesNotBecomeReferenceMaterial(t *testing.T) {
	for _, region := range []Region{RegionFeishu, RegionLark} {
		for _, parent := range []string{"", "referenced"} {
			for _, content := range []string{"普通问题", ""} {
				a := &Adapter{region: region}
				body, err := json.Marshal(map[string]string{"text": content})
				if err != nil {
					t.Fatal(err)
				}
				msg, err := a.parseIncoming(t.Context(), &feishuMessage{
					MessageType: "text", ParentID: parent, Content: string(body),
				}, "sender", "")
				if err != nil || msg == nil || msg.Content != content {
					t.Fatalf("plain text changed: msg=%+v err=%v", msg, err)
				}
				if (msg.Material != nil) != (parent != "" || content == "") {
					t.Fatalf("wrong material scope: parent=%q content=%q", parent, content)
				}
			}
		}
	}
}

func TestNewPostContentDoesNotBecomeAnIMCommand(t *testing.T) {
	registry := im.NewCommandRegistry()
	registry.Register(&im.ClearCommand{})
	for _, tag := range []string{"code_block", "md", "a"} {
		t.Run(tag, func(t *testing.T) {
			element := postElement{Tag: tag, Text: "/clear"}
			if tag == "a" {
				element.Text, element.Href = "", "/clear"
			}
			body, err := json.Marshal(postBody{Content: [][]postElement{{
				{Tag: "at", UserID: "ou_bot"}, element,
			}}})
			if err != nil {
				t.Fatal(err)
			}
			a := &Adapter{region: RegionFeishu, botOpenID: "ou_bot"}
			msg, err := a.parseIncoming(t.Context(), &feishuMessage{
				MessageType: "post", ChatType: "group", Content: string(body),
			}, "sender", "")
			if err != nil || msg == nil || msg.Material == nil {
				t.Fatalf("material was dropped: msg=%+v err=%v", msg, err)
			}
			if !msg.SkipCommand && registry.IsRegistered(msg.Content) {
				t.Fatalf("%s material expanded the native command entry: %q", tag, msg.Content)
			}
			if msg.Content != "/clear" || msg.Material.Parts[0].Text != "/clear" {
				t.Fatal("command gating changed material text or current-input length")
			}
		})
	}
}
