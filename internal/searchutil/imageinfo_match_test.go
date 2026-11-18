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

func TestPruneMarkdownImagesByImageInfoIgnoresShiftedOffsets(t *testing.T) {
	content := "a manually inserted prefix that shifts every parser offset\n\n" +
		"![p1](u1)\n\nbody\n\n![p2](u2)"
	raw, err := json.Marshal([]types.ImageInfo{{URL: "u2"}})
	if err != nil {
		t.Fatal(err)
	}
	got := PruneMarkdownImagesByImageInfo(content, string(raw))
	if strings.Contains(got, "u1") {
		t.Fatalf("unscoped image remained: %q", got)
	}
	if !strings.Contains(got, "![p2](u2)") {
		t.Fatalf("scoped image was removed: %q", got)
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

// Issue: https://github.com/Tencent/WeKnora/issues/5606
// An image_caption / image_ocr chunk is bare caption/OCR text: no Markdown
// or <img> exists for the injection pass to enrich, so the image's URL never
// reached the model and a vision model invented resource:// handles that
// rendered as broken images. The extras pass must append the image with its
// handle and description together.
func TestEnrichContentWithImageInfoForChat_BareImageChunkAppendsWithHandle(t *testing.T) {
	content := "三角形的费马点示意图，点 P 位于三角形 ABC 内部"
	raw, _ := json.Marshal([]types.ImageInfo{{
		URL:     "resource://AbCdEfGhIjKlMnOpQrStUv",
		Caption: "费马点问题图 1",
		OCRText: "三角形 ABC 内部一点 P",
	}})

	got := EnrichContentWithImageInfoForChat(content, string(raw))

	for _, want := range []string{
		content,
		"![费马点问题图 1](resource://AbCdEfGhIjKlMnOpQrStUv)",
		"> **Image caption:** 费马点问题图 1",
		"> **Image text (OCR):** 三角形 ABC 内部一点 P",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in enriched bare chunk:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<image") {
		t.Fatalf("chat context should be Markdown-only: %s", got)
	}
}

// Issue: https://github.com/Tencent/WeKnora/issues/5606
// OriginalURL-only entries (legacy chunks that persisted the provider URL)
// must also be appended when content is a bare image chunk.
func TestEnrichContentWithImageInfoForChat_BareImageChunkUsesOriginalURLFallback(t *testing.T) {
	content := "双曲函数证明中的公式截图"
	raw, _ := json.Marshal([]types.ImageInfo{{
		OriginalURL: "local://images/formula-2.png",
		OCRText:     "sinh x = (e^x - e^-x) / 2",
	}})

	got := EnrichContentWithImageInfoForChat(content, string(raw))

	if !strings.Contains(got, "![image](local://images/formula-2.png)") {
		t.Fatalf("expected the OriginalURL handle in the extras: %s", got)
	}
	if !strings.Contains(got, "> **Image text (OCR):** sinh x = (e^x - e^-x) / 2") {
		t.Fatalf("expected the OCR text beside the handle: %s", got)
	}
}

// Issue: https://github.com/Tencent/WeKnora/issues/5606
// Empty content with image_info (an OCR chunk whose text is blank must not
// happen in practice, but the chat enrich must not emit a leading blank).
func TestEnrichContentWithImageInfoForChat_BareEmptyContentHasNoLeadingBlank(t *testing.T) {
	raw, _ := json.Marshal([]types.ImageInfo{{
		URL:     "resource://AbCdEfGhIjKlMnOpQrStUv",
		Caption: "only a caption",
	}})

	got := EnrichContentWithImageInfoForChat("", string(raw))

	if !strings.HasPrefix(got, "![only a caption](resource://AbCdEfGhIjKlMnOpQrStUv)") {
		t.Fatalf("expected the image block to start the passage: %s", got)
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

func TestClearImageInfoTextMatchingBody_ClearsOnlyHitField(t *testing.T) {
	raw, err := json.Marshal([]types.ImageInfo{
		{URL: "u1", OCRText: "page one ocr body", Caption: "page one caption"},
		{URL: "u2", OCRText: "page two ocr body", Caption: "page two caption"},
	})
	if err != nil {
		t.Fatal(err)
	}

	got := ClearImageInfoTextMatchingBody(string(raw), "page one ocr body", string(types.ChunkTypeImageOCR))
	var infos []types.ImageInfo
	if err := json.Unmarshal([]byte(got), &infos); err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("entries: %d", len(infos))
	}
	if infos[0].URL != "u1" || infos[0].OCRText != "" || infos[0].Caption != "page one caption" {
		t.Fatalf("hit entry: %+v", infos[0])
	}
	if infos[1].URL != "u2" || infos[1].OCRText != "page two ocr body" {
		t.Fatalf("sibling entry stripped: %+v", infos[1])
	}

	gotCaption := ClearImageInfoTextMatchingBody(string(raw), "page two caption", string(types.ChunkTypeImageCaption))
	if err := json.Unmarshal([]byte(gotCaption), &infos); err != nil {
		t.Fatal(err)
	}
	if infos[1].Caption != "" || infos[1].OCRText != "page two ocr body" || infos[0].Caption != "page one caption" {
		t.Fatalf("caption clear: %+v", infos)
	}
}

func TestClearImageInfoTextMatchingBody_UnchangedWhenNoMatch(t *testing.T) {
	raw, err := json.Marshal([]types.ImageInfo{{URL: "u1", OCRText: "kept ocr"}})
	if err != nil {
		t.Fatal(err)
	}
	got := ClearImageInfoTextMatchingBody(string(raw), "other body", string(types.ChunkTypeImageOCR))
	if got != string(raw) {
		t.Fatalf("expected original JSON, got %q", got)
	}
	if got := ClearImageInfoTextMatchingBody("", "body", string(types.ChunkTypeImageOCR)); got != "" {
		t.Fatalf("empty JSON: %q", got)
	}
}
