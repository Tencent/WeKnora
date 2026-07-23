package searchutil

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestSliceContentByDocumentRange(t *testing.T) {
	parent := "aaaPAGE1bbbPAGE2ccc"
	got := SliceContentByDocumentRange(parent, 100, 103, 108)
	want := "PAGE1"
	if got != want {
		t.Fatalf("slice: got %q, want %q", got, want)
	}
}

func TestFilterImageInfoByMatchRange(t *testing.T) {
	parent := "![p1](u1)\n\n![p2](u2)\n\n![p3](u3)"
	matchStart := len([]rune("![p1](u1)\n\n"))
	matchEnd := matchStart + len([]rune("![p2](u2)"))
	all := []types.ImageInfo{
		{URL: "u1"}, {URL: "u2"}, {URL: "u3"},
	}
	raw, err := json.Marshal(all)
	if err != nil {
		t.Fatal(err)
	}
	got := FilterImageInfoByMatchRange(parent, 0, matchStart, matchEnd, string(raw))
	var filtered []types.ImageInfo
	if err := json.Unmarshal([]byte(got), &filtered); err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].URL != "u2" {
		t.Fatalf("filtered: %+v", filtered)
	}
}

func TestFilterImageInfoByContentURLs(t *testing.T) {
	content := "intro\n![page3](local://img3.jpg)\noutro"
	all := []types.ImageInfo{
		{URL: "local://img1.jpg", OCRText: "one"},
		{URL: "local://img3.jpg", OCRText: "three"},
	}
	raw, err := json.Marshal(all)
	if err != nil {
		t.Fatal(err)
	}
	got := FilterImageInfoByContentURLs(content, string(raw))
	var filtered []types.ImageInfo
	if err := json.Unmarshal([]byte(got), &filtered); err != nil {
		t.Fatalf("unmarshal filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].URL != "local://img3.jpg" {
		t.Fatalf("filtered: %+v", filtered)
	}
}

func TestPruneMarkdownImagesOutsideRange(t *testing.T) {
	parent := "![p1](u1)\n\n![p2](u2)\n\n![p3](u3)"
	matchStart := len([]rune("![p1](u1)\n\n"))
	matchEnd := matchStart + len([]rune("![p2](u2)"))
	got := PruneMarkdownImagesOutsideRange(parent, 0, matchStart, matchEnd)
	if got != "![p2](u2)" {
		t.Fatalf("prune: got %q", got)
	}
}

func TestEnrichContentWithImageInfoForChat_SkipsUnmatched(t *testing.T) {
	content := "![p1](u1)\n\n![p2](u2)"
	raw, _ := json.Marshal([]types.ImageInfo{{URL: "u2", OCRText: "two"}})
	got := EnrichContentWithImageInfoForChat(content, string(raw))
	if strings.Contains(got, "<image") {
		t.Fatalf("chat context should not contain internal image XML: %s", got)
	}
	if !strings.Contains(got, "![p1](u1)") {
		t.Fatalf("unmatched markdown should remain: %s", got)
	}
	if !strings.Contains(got, "![p2](u2)") {
		t.Fatalf("matched markdown should remain renderable: %s", got)
	}
	if !strings.Contains(got, "> **Image text (OCR):** two") {
		t.Fatalf("matched image should be enriched: %s", got)
	}
	if strings.Count(got, "![") != 2 {
		t.Fatalf("chat enrich should not duplicate markdown images: %s", got)
	}
}

func TestEnrichContentWithImageInfoForChat_UsesMarkdownForMultilineMetadata(t *testing.T) {
	content := "before\n\n![flow](resource://AbCdEfGhIjKlMnOpQrStUv)\n\nafter"
	raw, _ := json.Marshal([]types.ImageInfo{
		{
			URL:     "resource://AbCdEfGhIjKlMnOpQrStUv",
			Caption: "目标说话人提取流程图",
			OCRText: "输入\n目标说话人提取\n输出",
		},
	})

	got := EnrichContentWithImageInfoForChat(content, string(raw))
	for _, want := range []string{
		"![flow](resource://AbCdEfGhIjKlMnOpQrStUv)",
		"> **Image caption:** 目标说话人提取流程图",
		"> **Image text (OCR):** 输入",
		"> 目标说话人提取",
		"> 输出",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in enriched Markdown:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<image") {
		t.Fatalf("chat context should be Markdown-only for image content: %s", got)
	}
}

func TestEnrichContentWithImageInfoForChat_EnrichesRepeatedImagesOnceEach(t *testing.T) {
	content := "![same](u1)\n\n![same](u1)"
	raw, _ := json.Marshal([]types.ImageInfo{{URL: "u1", Caption: "same caption"}})

	got := EnrichContentWithImageInfoForChat(content, string(raw))
	if strings.Count(got, "![same](u1)") != 2 {
		t.Fatalf("expected both Markdown images to remain: %s", got)
	}
	if strings.Count(got, "> **Image caption:** same caption") != 2 {
		t.Fatalf("expected each image to be enriched exactly once: %s", got)
	}
}

func TestBuildImageInfoMarkdownWithURL(t *testing.T) {
	got := BuildImageInfoMarkdownWithURL(
		"resource://AbCdEfGhIjKlMnOpQrStUv",
		&types.ImageInfo{Caption: "流程图 [测试]", OCRText: "输入\n输出"},
	)
	for _, want := range []string{
		`![流程图 \[测试\]](resource://AbCdEfGhIjKlMnOpQrStUv)`,
		"> **Image caption:** 流程图 [测试]",
		"> **Image text (OCR):** 输入",
		"> 输出",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in image Markdown:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<image") {
		t.Fatalf("LLM-facing image context must not use image XML: %s", got)
	}
}

func TestImageURLsInContent(t *testing.T) {
	content := "![a](u1) x ![b](u2)"
	urls := ImageURLsInContent(content)
	if !urls["u1"] || !urls["u2"] || len(urls) != 2 {
		t.Fatalf("urls: %#v", urls)
	}
}
