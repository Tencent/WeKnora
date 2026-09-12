package feishu

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/im"
)

func TestCardMarkdownImagesExcludeCodeLinksAndRegistryEntries(t *testing.T) {
	content := "![正文图片](img_body) [普通链接](https://example.com/report) " +
		"`![代码](img_inline_code)` ``![代码](img_double_code)`` " +
		"\\![转义](img_escaped) ![URL](https://example.com/image.png)\n" +
		"```md\n![代码块](img_fenced_code)\n```\n![末尾图片](img_last)"
	raw, err := json.Marshal(map[string]any{
		"elements": []any{map[string]any{"tag": "markdown", "content": content}},
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(map[string]any{
		"json_card": json.RawMessage(raw),
		"json_attachment": map[string]any{"images": map[string]any{
			"unused": map[string]any{"origin_key": "img_not_referenced", "auth": "private"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := parseCard(t.Context(), string(envelope), nil)
	var keys []string
	for _, part := range result.Parts {
		if part.Type != "" {
			keys = append(keys, part.FileKey)
		}
	}
	if !reflect.DeepEqual(keys, []string{"img_body", "img_last"}) || strings.Contains(cardTestText(result), "private") {
		t.Fatalf("non-body references became resource reads: %v", keys)
	}
	if strings.Contains(cardTestText(result), "媒体资源未读取") {
		t.Fatal("card text contradicts the attachment results before its images have been read")
	}
}

func TestCardMarkdownImageReadsRespectActualCodeBoundaries(t *testing.T) {
	for _, content := range []string{
		"```text\n示例里的 ``` 是文字\n![代码示例](img_code)\n```\n![真实正文](img_real)",
		"~~~text\n示例里的 ~~~ 是文字\n![代码示例](img_code)\n~~~\n![真实正文](img_real)",
		"未闭合的 ` 是普通文字\n![真实正文](img_real)",
		"未闭合的 `` 是普通文字\n![真实正文](img_real)",
		"注意字符 `\n\n![正文图](img_real)\n\n示例命令 `pwd`",
		"注意字符 `\n# 标题\n![正文图](img_real)\n示例命令 `pwd`",
		"注意字符 `\n- ![正文图](img_real)\n示例命令 `pwd`",
		"> ```text\n> 示例里的 ``` 是文字\n> ![代码](img_code)\n> ```\n![正文](img_real)",
		"> ```text\n> ![代码](img_code)\n![正文](img_real)",
		"> `代码\n> ![示例](img_code)`\n![正文](img_real)",
		"`这是包含 ![代码`示例](img_code) 的代码`\n![真实正文](img_real)",
		"    ![代码](img_code)\n\n![正文](img_real)",
		"- ```md\n  ![代码](img_code)\n  ```\n\n![正文](img_real)",
		"![正文][ref]\n\n[ref]: img_real",
		`![正文\]说明](img_real)`,
		`![正文](<img_real>)`,
		`![正文](img\_real)`,
		`![正文](img&#95;real)`,
		`![正文 ![嵌套替代文本](img_code)](img_real)`,
		"<audio file_key='file_audio'></audio>\n![正文](img_real)",
	} {
		raw, err := json.Marshal(map[string]any{
			"elements": []any{map[string]any{"tag": "markdown", "content": content}},
		})
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, part := range parseCard(t.Context(), string(raw), nil).Parts {
			if part.Type != "" {
				keys = append(keys, part.FileKey)
			}
		}
		if !reflect.DeepEqual(keys, []string{"img_real"}) {
			t.Fatalf("code boundary changed resource reads for %q: %v", content, keys)
		}
	}
}

func TestCardImagesInTablesAndImageOptionsAreMaterialBodies(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		keys []string
	}{
		{
			`{"schema":"2.0","body":{"elements":[{"tag":"markdown",` +
				`"content":"| 项目 | 附件 |\n| --- | --- |\n| ` + "`" +
				` | ![发票](img_invoice) |\n| ` + "`" + ` | other |"}]}}`, []string{"img_invoice"},
		},
		{
			`{"elements":[{"tag":"table","columns":[{"name":"photo","data_type":"markdown"}],` +
				`"rows":[{"photo":"![发票](img_invoice)"},{"photo":"![报表][ref]\n\n[ref]: img_report"}]}]}`,
			[]string{"img_invoice", "img_report"},
		},
		{`{"elements":[{"tag":"table","columns":[{"name":"photo","data_type":"text"}],` +
			`"rows":[{"photo":"![文本示例](img_example)"}]}]}`, nil},
		{
			`{"elements":[{"tag":"select_img","options":[{"img_key":"img_a","value":"a"},` +
				`{"img_key":"img_b","value":"b"}],"behaviors":[{"type":"callback","value":{"secret":"payload"}}]}]}`,
			[]string{"img_a", "img_b"},
		},
	} {
		result := parseCard(t.Context(), tc.raw, nil)
		var keys []string
		for _, part := range result.Parts {
			if part.Type != "" {
				keys = append(keys, part.FileKey)
			}
		}
		if result.Status != "complete" || !reflect.DeepEqual(keys, tc.keys) ||
			strings.Contains(cardTestText(result), "payload") {
			t.Fatalf("nested material resources lost or callback parsed: keys=%v status=%s missing=%v",
				keys, result.Status, result.Missing)
		}
	}
}

func TestCardLimitedMarkdownDoesNotCreateImageDownloads(t *testing.T) {
	for _, raw := range []string{
		`{"elements":[{"tag":"div","text":{"tag":"lark_md","content":"![示例](img_example)"}}]}`,
		`{"elements":[{"tag":"table","columns":[{"name":"v","data_type":"lark_md"}],` +
			`"rows":[{"v":"![示例](img_example)"}]}]}`,
	} {
		result := parseCard(t.Context(), raw, nil)
		if result.Status != "complete" {
			t.Fatal("limited Markdown source was lost")
		}
		for _, part := range result.Parts {
			if part.Type != "" {
				t.Fatal("lark_md text was treated as a rendered image")
			}
		}
	}
}

func TestCardResourceDescriptionsDeferToActualReadResults(t *testing.T) {
	result := parseCard(t.Context(), `{"json_card":{"elements":[{"tag":"img","img_key":"img_body"}]},`+
		`"json_attachment":{"images":{"17":{"origin_key":"img_body"}}}}`, nil)
	if strings.Contains(cardTestText(result), "(资源正文未读取)") {
		t.Fatal("registry metadata contradicts a resource that the worker can read")
	}
	result = parseCard(t.Context(), `{"schema":"2.0","body":{"elements":[`+
		`{"tag":"markdown","content":"<audio file_key='file_audio'></audio>"}]}}`, nil)
	if !strings.Contains(cardTestText(result), "音视频未自动读取") {
		t.Fatal("inline audio did not explain that its body was not read")
	}
}

func TestCardResourceDownloadsStayInsideFeishuAssetAPIs(t *testing.T) {
	useTestHTTPClient(t)
	for _, region := range []Region{RegionFeishu, RegionLark} {
		for _, kind := range []im.MessageType{im.MessageTypeImage, im.MessageTypeFile} {
			for _, card := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/card=%t", region.Label, kind, card), func(t *testing.T) {
					var requests []string
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						requests = append(requests, r.URL.RequestURI())
						if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer same-bot" {
							t.Error("resource read changed identity or HTTP method")
						}
						if strings.HasSuffix(r.URL.Path, "denied") {
							w.WriteHeader(http.StatusBadRequest)
							_, _ = io.WriteString(w, `{"code":234008,"msg":"not uploaded by this bot"}`)
							return
						}
						// Feishu's file API only returns the filename with this request header.
						if !card || kind != im.MessageTypeFile || r.Header.Get("Content-Type") == "application/json" {
							w.Header().Set("Content-Disposition", `attachment; filename="material.txt"`)
						}
						_, _ = io.WriteString(w, "RESOURCE-BODY")
					}))
					defer server.Close()
					a := &Adapter{
						region: region, apiBaseURL: server.URL,
						tokenCache: "same-bot", tokenExpAt: time.Now().Add(time.Hour),
					}
					msg := &im.IncomingMessage{MessageID: "visible-card", FileKey: "asset", MessageType: kind}
					if card {
						msg.Material = &im.MessageMaterial{Type: "interactive", SnapshotSource: "message_read"}
					}
					body, name, err := a.DownloadFile(t.Context(), msg)
					if err != nil {
						t.Fatal(err)
					}
					data, err := io.ReadAll(body)
					if closeErr := body.Close(); closeErr != nil {
						t.Fatal(closeErr)
					}
					want := "/open-apis/im/v1/messages/visible-card/resources/asset?type=" + string(kind)
					if card {
						want = "/open-apis/im/v1/" + string(kind) + "s/asset"
					}
					if err != nil || string(data) != "RESOURCE-BODY" || name != "material.txt" ||
						!reflect.DeepEqual(requests, []string{want}) || msg.FileSize != int64(len(data)) {
						t.Fatalf("resource route/body changed: %v / %q / %s", requests, data, name)
					}
					msg.FileKey = "denied"
					body, _, err = a.DownloadFile(t.Context(), msg)
					if err == nil || body != nil || len(requests) != 2 {
						t.Fatal("resource permission denial was retried through another route")
					}
					for _, key := range []string{"../other", "https://example.com/file"} {
						msg.FileKey = key
						if _, _, err = a.DownloadFile(t.Context(), msg); err == nil || len(requests) != 2 {
							t.Fatal("invalid resource key triggered a network request")
						}
					}
				})
			}
		}
	}
}

func TestCardJSON1HeaderIconsPreserveImagesAndSymbols(t *testing.T) {
	for _, icons := range []string{
		`"icon":{"img_key":"img_header"}`,
		`"ud_icon":{"token":"chat-forbidden_outlined","style":{"color":"red"}}`,
		`"icon":{"img_key":"img_header"},` +
			`"ud_icon":{"token":"chat-forbidden_outlined","style":{"color":"red"}}`,
	} {
		result := parseCard(t.Context(), `{"header":{"title":{"tag":"plain_text","content":"标题"},`+icons+`}}`, nil)
		if result.Status != "complete" {
			t.Fatalf("official JSON 1.0 header icon was not extracted: %v", result.Missing)
		}
		var keys []string
		for _, part := range result.Parts {
			if part.Type != "" {
				keys = append(keys, part.FileKey)
			}
		}
		if strings.Contains(icons, "img_header") && !reflect.DeepEqual(keys, []string{"img_header"}) {
			t.Fatalf("header image did not reach attachment reading: %v", keys)
		}
		if strings.Contains(icons, "ud_icon") {
			assertCardContains(t, cardTestText(result), "chat-forbidden_outlined", "red")
		}
		if strings.Contains(icons, "img_header") && strings.Contains(icons, "ud_icon") {
			assertCardContains(t, cardTestText(result), "ud_icon优先生效")
		}
	}
}
